package app

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/spec"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

const defaultMaxCachedFetchRunes = 4 << 20 // 约 4M Unicode 字符（控制在约 8-16MB 内存上限内）

type cacheEntry[V any] struct {
	key       string
	value     V
	cost      int
	expiresAt time.Time
}

// ttlLRU 是并发安全、同时支持条目数与总权重（字符数）双上限的内存 TTL-LRU 缓存。
type ttlLRU[V any] struct {
	mu       sync.Mutex
	capacity int
	maxCost  int
	ttl      time.Duration
	usedCost int
	costFn   func(V) int
	ll       *list.List
	items    map[string]*list.Element
}

func newTTLLRU[V any](capacity int, ttl time.Duration) *ttlLRU[V] {
	return newBoundedTTLLRU[V](capacity, 0, ttl, nil)
}

func newBoundedTTLLRU[V any](capacity, maxCost int, ttl time.Duration, costFn func(V) int) *ttlLRU[V] {
	if capacity <= 0 || ttl <= 0 {
		return &ttlLRU[V]{}
	}
	return &ttlLRU[V]{
		capacity: capacity,
		maxCost:  maxCost,
		ttl:      ttl,
		costFn:   costFn,
		ll:       list.New(),
		items:    make(map[string]*list.Element, capacity),
	}
}

func (c *ttlLRU[V]) Get(key string) (V, bool) {
	var zero V
	if c == nil || c.capacity <= 0 || c.ttl <= 0 {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return zero, false
	}
	entry := elem.Value.(*cacheEntry[V])
	if time.Now().After(entry.expiresAt) {
		c.removeElementLocked(elem)
		return zero, false
	}
	c.ll.MoveToFront(elem)
	return entry.value, true
}

func (c *ttlLRU[V]) Set(key string, val V) {
	if c == nil || c.capacity <= 0 || c.ttl <= 0 {
		return
	}
	itemCost := 0
	if c.costFn != nil {
		itemCost = max(c.costFn(val), 0)
	}
	if c.maxCost > 0 && itemCost > c.maxCost {
		c.mu.Lock()
		if elem, ok := c.items[key]; ok {
			c.removeElementLocked(elem)
		}
		c.mu.Unlock()
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.evictExpiredTailLocked(now, 4)

	if elem, ok := c.items[key]; ok {
		c.ll.MoveToFront(elem)
		entry := elem.Value.(*cacheEntry[V])
		c.usedCost += itemCost - entry.cost
		entry.value = val
		entry.cost = itemCost
		entry.expiresAt = now.Add(c.ttl)
	} else {
		elem := c.ll.PushFront(&cacheEntry[V]{
			key:       key,
			value:     val,
			cost:      itemCost,
			expiresAt: now.Add(c.ttl),
		})
		c.items[key] = elem
		c.usedCost += itemCost
	}

	for c.ll.Len() > c.capacity || (c.maxCost > 0 && c.usedCost > c.maxCost && c.ll.Len() > 0) {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.removeElementLocked(oldest)
	}
}

func (c *ttlLRU[V]) removeElementLocked(elem *list.Element) {
	entry := elem.Value.(*cacheEntry[V])
	c.ll.Remove(elem)
	delete(c.items, entry.key)
	c.usedCost = max(c.usedCost-entry.cost, 0)
}

func (c *ttlLRU[V]) evictExpiredTailLocked(now time.Time, maxCheck int) {
	for range maxCheck {
		oldest := c.ll.Back()
		if oldest == nil {
			return
		}
		entry := oldest.Value.(*cacheEntry[V])
		if !now.After(entry.expiresAt) {
			return
		}
		c.removeElementLocked(oldest)
	}
}

type flightCall[V any] struct {
	done     chan struct{}
	val      V
	err      error
	cancel   context.CancelFunc
	waiters  int
	finished bool
}

// flightGroup 合并并发同键请求：单个短超时等待者取消不会中断其余等待者，全部等待者取消时立即释放底层请求，且内置 panic 恢复。
type flightGroup[V any] struct {
	mu    sync.Mutex
	calls map[string]*flightCall[V]
}

func (g *flightGroup[V]) Do(ctx context.Context, key string, fn func(context.Context) (V, error)) (V, bool, error) {
	var zero V
	if err := ctx.Err(); err != nil {
		return zero, false, err
	}

	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*flightCall[V])
	}
	if c, ok := g.calls[key]; ok && !c.finished {
		c.waiters++
		g.mu.Unlock()
		return g.wait(ctx, key, c)
	}

	sharedCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c := &flightCall[V]{
		done:    make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}
	g.calls[key] = c
	g.mu.Unlock()

	go func() {
		defer cancel()
		g.execute(sharedCtx, key, c, fn)
	}()
	return g.wait(ctx, key, c)
}

func (g *flightGroup[V]) execute(sharedCtx context.Context, key string, c *flightCall[V], fn func(context.Context) (V, error)) {
	var val V
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = core.NewError(core.CodeInternal, fmt.Sprintf("recovered internal panic: %v", r))
			}
		}()
		val, err = fn(sharedCtx)
	}()

	g.mu.Lock()
	c.val = val
	c.err = err
	c.finished = true
	if g.calls[key] == c {
		delete(g.calls, key)
	}
	c.cancel()
	close(c.done)
	g.mu.Unlock()
}

func (g *flightGroup[V]) wait(ctx context.Context, key string, c *flightCall[V]) (V, bool, error) {
	select {
	case <-c.done:
		return c.val, true, c.err
	case <-ctx.Done():
		g.mu.Lock()
		c.waiters--
		if c.waiters <= 0 && !c.finished {
			c.finished = true
			if g.calls[key] == c {
				delete(g.calls, key)
			}
			c.cancel()
		}
		g.mu.Unlock()
		var zero V
		return zero, false, ctx.Err()
	}
}

func envelopeContentRunes(env *core.Envelope) int {
	if env == nil || env.Page == nil {
		return 0
	}
	if env.Page.TotalChars > 0 {
		return env.Page.TotalChars
	}
	return textnorm.RuneCount(env.Page.Content)
}

func buildSearchCacheKey(req *core.SearchRequest, searchCfg config.SearchConfig, defaultMaxChars int) string {
	type canonical struct {
		Query        string            `json:"q"`
		Question     string            `json:"question,omitempty"`
		Count        int               `json:"count"`
		Detail       string            `json:"detail"`
		Sites        []string          `json:"sites,omitempty"`
		Excludes     []string          `json:"excludes,omitempty"`
		Lang         []string          `json:"lang,omitempty"`
		FreshAfter   string            `json:"after,omitempty"`
		FreshEnd     string            `json:"before,omitempty"`
		FreshUnit    string            `json:"unit,omitempty"`
		FreshN       int               `json:"n,omitzero"`
		Providers    []string          `json:"providers,omitempty"`
		Strategy     string            `json:"strategy"`
		Strict       bool              `json:"strict"`
		IncludeExtra bool              `json:"extra,omitzero"`
		MaxChars     int               `json:"max_chars"`
		Options      map[string]string `json:"opts,omitempty"`
	}
	count := req.Count
	if count <= 0 {
		count = searchCfg.Count
	}
	detail := strings.ToLower(strings.TrimSpace(string(req.Detail)))
	if detail == "" {
		detail = strings.ToLower(strings.TrimSpace(searchCfg.Detail))
	}
	strategy := strings.ToLower(strings.TrimSpace(string(req.Strategy)))
	if strategy == "" {
		strategy = strings.ToLower(strings.TrimSpace(searchCfg.Strategy))
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	provs := normalizeCacheProviders(req.Providers)
	var afterStr, beforeStr, unitStr string
	var freshN int
	if f := req.Filters.Freshness; f != nil {
		if !f.After.IsZero() {
			afterStr = f.After.UTC().Format("2006-01-02")
		}
		if !f.Before.IsZero() {
			beforeStr = f.Before.UTC().Format("2006-01-02")
		}
		unitStr = string(f.Unit)
		freshN = f.N
	}
	raw, _ := json.Marshal(canonical{
		Query:        strings.TrimSpace(req.Query),
		Question:     strings.TrimSpace(req.Question),
		Count:        count,
		Detail:       detail,
		Sites:        urlnorm.CleanDomainFilters(req.Filters.Sites),
		Excludes:     urlnorm.CleanDomainFilters(req.Filters.ExcludeSites),
		Lang:         urlnorm.CleanLangFilters(req.Filters.Lang),
		FreshAfter:   afterStr,
		FreshEnd:     beforeStr,
		FreshUnit:    unitStr,
		FreshN:       freshN,
		Providers:    provs,
		Strategy:     strategy,
		Strict:       req.StrictFilters || searchCfg.StrictFilters,
		IncludeExtra: req.IncludeExtra,
		MaxChars:     maxChars,
		Options:      normalizeCacheOptions(req.Options, ""),
	}, json.Deterministic(true))
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func buildFetchCacheKey(rawURL string, strategy core.FetchStrategy, providers []string, options map[string]string) string {
	type canonical struct {
		URL       string            `json:"u"`
		Strategy  string            `json:"s"`
		Providers []string          `json:"p,omitempty"`
		Options   map[string]string `json:"o,omitempty"`
	}
	raw, _ := json.Marshal(canonical{
		URL:       canonicalFetchTargetKey(rawURL),
		Strategy:  strings.ToLower(strings.TrimSpace(string(strategy))),
		Providers: normalizeCacheProviders(providers),
		Options:   normalizeCacheOptions(options, spec.OptFetchFresh),
	}, json.Deterministic(true))
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizeCacheOptions(options map[string]string, skipKey string) map[string]string {
	out := spec.NormalizeOptions(options)
	if skipKey != "" {
		delete(out, skipKey)
		if len(out) == 0 {
			return nil
		}
	}
	return out
}

func isFreshFetchBypass(opts map[string]string) bool {
	if spec.IsFreshFetchOption(opts) {
		return true
	}
	return spec.LookupOption(opts, spec.OptTinyFishTTL, spec.OptTinyFishTTLAlias, spec.OptExaMaxAgeHours, spec.OptExaMaxAgeAlias) == "0"
}

func canonicalFetchTargetKey(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return trimmed
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.User = nil
	return u.String()
}

func normalizeCacheProviders(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if norm, ok := spec.NormalizeProviderName(item); ok {
			out = append(out, norm)
		} else if trimmed := strings.ToLower(strings.TrimSpace(item)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func cloneCachedSearchEnvelope(src *core.Envelope) *core.Envelope {
	cp := src.Clone()
	cp.Meta.Cached = true
	cp.Meta.Cost = []core.Cost{}
	cp.Meta.DurationMS = 0
	for i := range cp.Meta.Providers {
		cp.Meta.Providers[i].Cost = nil
	}
	return cp
}
