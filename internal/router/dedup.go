package router

import (
	"cmp"
	"fmt"
	"math"
	"net/url"
	"strings"

	"veri/internal/core"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

var derivedMirrorSuffixes = []string{
	".shtml.md", ".html.md", ".htm.md", ".php.md", ".aspx.md", ".asp.md", ".jsp.md",
	".shtml.txt", ".html.txt", ".htm.txt",
}

type resultContentKey struct {
	titleTokens []string
	bodyFP      textnorm.TextFingerprint
	bodyRunes   int
}

// clusterMember 描述内容聚类中的单条来源候选及其来源分支上下文。
type clusterMember struct {
	result   core.Result
	urlKey   string
	branchID int
	group    string
	rank     int
}

func buildResultContentKey(r core.Result) resultContentKey {
	body, bodyRunes := richestResultBody(r)
	return resultContentKey{
		titleTokens: textnorm.NormalizeTitleForDedup(r.Title, r.Site),
		bodyFP:      textnorm.BuildTextFingerprint(body),
		bodyRunes:   bodyRunes,
	}
}

func upgradeAnchorKey(anchor *resultContentKey, incoming resultContentKey) {
	if len(anchor.titleTokens) == 0 && len(incoming.titleTokens) > 0 {
		anchor.titleTokens = incoming.titleTokens
	}
	if incoming.bodyRunes > anchor.bodyRunes {
		anchor.bodyFP = incoming.bodyFP
		anchor.bodyRunes = incoming.bodyRunes
	}
}

func richestResultBody(r core.Result) (string, int) {
	best := r.Content
	bestRunes := textnorm.RuneCount(best)
	if r.Summary != "" {
		if n := textnorm.RuneCount(r.Summary); n > bestRunes {
			best = r.Summary
			bestRunes = n
		}
	}
	if r.Snippet != "" {
		if n := textnorm.RuneCount(r.Snippet); n > bestRunes {
			best = r.Snippet
			bestRunes = n
		}
	}
	return best, bestRunes
}

// dedupResultsByContent 在保留原始排序的前提下，采用两阶段（先聚簇、后统一选代表）按标题+正文指纹折叠跨 URL 同稿转载。
func dedupResultsByContent(items []core.Result) ([]core.Result, int) {
	if len(items) <= 1 {
		return items, 0
	}
	type cluster struct {
		members  []clusterMember
		anchorCK resultContentKey
	}
	clusters := make([]cluster, 0, len(items))
	collapsed := 0

	for idx, item := range items {
		ck := buildResultContentKey(item)
		uKey := urlnorm.Key(item.URL)
		member := clusterMember{
			result:   item.Clone(),
			urlKey:   uKey,
			branchID: 0,
			rank:     idx + 1,
		}

		matched := -1
		if ck.bodyFP.Matchable() {
			for i := range clusters {
				if textnorm.FingerprintsNearDuplicate(clusters[i].anchorCK.titleTokens, clusters[i].anchorCK.bodyFP, ck.titleTokens, ck.bodyFP) {
					matched = i
					break
				}
			}
		}
		if matched < 0 {
			clusters = append(clusters, cluster{
				members:  []clusterMember{member},
				anchorCK: ck,
			})
			continue
		}
		collapsed++
		clusters[matched].members = append(clusters[matched].members, member)
		upgradeAnchorKey(&clusters[matched].anchorCK, ck)
	}

	if collapsed == 0 {
		return items, 0
	}
	out := make([]core.Result, len(clusters))
	for i := range clusters {
		res := resolveClusterResult(clusters[i].members, nil, true)
		res.Rank = i + 1
		out[i] = res
	}
	return out, collapsed
}

// resolveClusterResult 从同稿聚类的全部候选成员中选出规范代表页，并将簇内其余成员的文章级富字段无损合并。
func resolveClusterResult(members []clusterMember, providers []string, singleEngine bool) core.Result {
	if len(members) == 1 {
		out := members[0].result
		if len(providers) > 0 {
			out.Providers = providers
		}
		return out
	}

	urlSet := make(map[string]struct{}, len(members))
	var urlVotes map[string]int
	if !singleEngine {
		urlVotes = make(map[string]int, len(members))
		groupVotes := make(map[string]map[string]struct{}, len(members))
		for _, m := range members {
			if m.urlKey == "" {
				continue
			}
			urlSet[m.urlKey] = struct{}{}
			voter := m.group
			if voter == "" {
				voter = fmt.Sprintf("#%d", m.branchID)
			}
			seen := groupVotes[m.urlKey]
			if seen == nil {
				seen = make(map[string]struct{}, 2)
				groupVotes[m.urlKey] = seen
			}
			if _, dup := seen[voter]; !dup {
				seen[voter] = struct{}{}
				urlVotes[m.urlKey]++
			}
		}
	} else {
		for _, m := range members {
			if m.urlKey != "" {
				urlSet[m.urlKey] = struct{}{}
			}
		}
	}

	hygiene := make([]int, len(members))
	for i, m := range members {
		hygiene[i] = canonicalHygieneScore(m, urlSet)
	}

	winnerIdx := 0
	for i := 1; i < len(members); i++ {
		if compareCanonicalMember(members[i], hygiene[i], members[winnerIdx], hygiene[winnerIdx], urlVotes, singleEngine) < 0 {
			winnerIdx = i
		}
	}

	winner := members[winnerIdx]
	out := winner.result.Clone()
	if len(providers) > 0 {
		out.Providers = providers
	}
	for i, m := range members {
		if i == winnerIdx {
			continue
		}
		sameURL := winner.urlKey != "" && m.urlKey == winner.urlKey
		mergeResultMetadata(&out, m.result, sameURL)
	}
	return out
}

// compareCanonicalMember 严格弱序比较两位候选成员谁更适合作为规范代表页（返回值 < 0 表示 a 优于 b）。
// 优先级：1) 标题与 URL 卫生门禁 -> 2) 多引擎 URL 共识票数 -> 3) 同源校准权威度（仅单引擎）-> 4) 来源排名 -> 5) 元数据完整度 -> 6) 分支序。
func compareCanonicalMember(a clusterMember, hygieneA int, b clusterMember, hygieneB int, urlVotes map[string]int, singleEngine bool) int {
	authCmp := 0
	if singleEngine && a.result.Authority != nil && b.result.Authority != nil {
		authCmp = cmp.Compare(authorityBucket(*b.result.Authority), authorityBucket(*a.result.Authority))
	}
	return cmp.Or(
		cmp.Compare(hygieneB, hygieneA),
		cmp.Compare(urlVotes[b.urlKey], urlVotes[a.urlKey]),
		authCmp,
		cmp.Compare(a.rank, b.rank),
		cmp.Compare(metadataCompleteness(b.result), metadataCompleteness(a.result)),
		cmp.Compare(a.branchID, b.branchID),
	)
}

func authorityBucket(v float64) int {
	return int(math.Round(v * 20))
}

func canonicalHygieneScore(m clusterMember, urlSet map[string]struct{}) int {
	score := 0
	if textnorm.IsCleanResultTitle(m.result.Title) {
		score += 4
	}
	if u, err := url.Parse(strings.TrimSpace(m.result.URL)); err == nil && u.Host != "" {
		if strings.EqualFold(u.Scheme, "https") {
			score += 3
		}
		if !isDerivedMirrorURL(u, m.urlKey, urlSet) {
			score += 3
		}
		if !isMobileShellHost(strings.ToLower(u.Hostname())) {
			score += 2
		}
	}
	return score
}

func metadataCompleteness(r core.Result) int {
	score := 0
	if r.Site != "" {
		score++
	}
	if r.Published != nil {
		score++
	}
	return score
}

// isDerivedMirrorURL 识别新闻/文档站的派生纯文本镜像后缀（如 .shtml.md / .html.md，或同簇存在无 .md 网页原页时的 .md 副本），
// 避免误伤 GitHub README.md 等原生 Markdown 文档。
func isDerivedMirrorURL(u *url.URL, uKey string, urlSet map[string]struct{}) bool {
	trimmedPath := strings.TrimRight(u.Path, "/")
	lowerPath := strings.ToLower(trimmedPath)
	for _, suf := range derivedMirrorSuffixes {
		if strings.HasSuffix(lowerPath, suf) {
			return true
		}
	}
	if strings.HasSuffix(lowerPath, ".md") && len(trimmedPath) > 3 && len(urlSet) > 1 {
		twin := *u
		twin.Path = trimmedPath[:len(trimmedPath)-3]
		twin.RawPath = ""
		twinKey := urlnorm.Key(twin.String())
		if twinKey != "" && twinKey != uKey {
			if _, hasHTMLTwin := urlSet[twinKey]; hasHTMLTwin {
				return true
			}
		}
	}
	return false
}

func isMobileShellHost(host string) bool {
	return strings.HasPrefix(host, "m.") || strings.HasPrefix(host, "wap.") || strings.HasPrefix(host, "mobile.")
}

// mergeResultMetadata 将簇内其余候选的文章级富字段择优并入已选定的代表结果；
// URL 绑定元数据（Site / Icon）仅在 sameURL 为真时回填，严禁将异站转载方的 Site/Icon 混拼到当前代表 URL。
func mergeResultMetadata(dst *core.Result, incoming core.Result, sameURL bool) {
	if dst.Title == "" && incoming.Title != "" {
		dst.Title = incoming.Title
	}
	if dst.Snippet == "" && incoming.Snippet != "" {
		dst.Snippet = incoming.Snippet
	}
	if sameURL {
		if dst.Site == "" && incoming.Site != "" {
			dst.Site = incoming.Site
		}
		if dst.Icon == "" && incoming.Icon != "" {
			dst.Icon = incoming.Icon
		}
	}

	if textnorm.RuneCount(incoming.Summary) > textnorm.RuneCount(dst.Summary) {
		dst.Summary = incoming.Summary
	}
	if textnorm.RuneCount(incoming.Content) > textnorm.RuneCount(dst.Content) {
		dst.Content = incoming.Content
	}
	if len(incoming.Authors) > 0 {
		dst.Authors = mergeAuthors(dst.Authors, incoming.Authors)
	}
	if incoming.Published != nil {
		if dst.Published == nil || incoming.Published.Before(*dst.Published) {
			dst.Published = new(incoming.Published.UTC())
		}
	}
	if incoming.Relevance != nil && (dst.Relevance == nil || *incoming.Relevance > *dst.Relevance) {
		dst.Relevance = new(*incoming.Relevance)
	}
	if incoming.Authority != nil && (dst.Authority == nil || *incoming.Authority > *dst.Authority) {
		dst.Authority = new(*incoming.Authority)
	}
	if len(incoming.Extra) > 0 {
		if dst.Extra == nil {
			dst.Extra = make(map[string]any, len(incoming.Extra))
		}
		for ek, ev := range incoming.Extra {
			if _, exists := dst.Extra[ek]; !exists {
				dst.Extra[ek] = ev
			}
		}
	}
}

func mergeAuthors(existing, incoming []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	out := make([]string, 0, len(existing)+len(incoming))
	for _, a := range existing {
		k := strings.ToLower(strings.TrimSpace(a))
		if k == "" {
			continue
		}
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			out = append(out, a)
		}
	}
	for _, a := range incoming {
		k := strings.ToLower(strings.TrimSpace(a))
		if k == "" {
			continue
		}
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			out = append(out, a)
		}
	}
	return out
}

func formatContentDedupNote(count int) string {
	if count == 1 {
		return "deduped 1 near-duplicate result by content fingerprint"
	}
	return fmt.Sprintf("deduped %d near-duplicate results by content fingerprint", count)
}
