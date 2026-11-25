package app

import (
	"bytes"
	"compress/gzip"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"veri/clikit/storage"
	"veri/internal/config"
	"veri/internal/core"
)

const (
	diskCacheVersion      = 1
	diskCacheSubdir       = "v1"
	diskCacheKindFetch    = "fetch"
	diskCacheKindSearch   = "search"
	diskCacheMaxEntries   = 256
	diskCacheMaxReadBytes = 16 << 20 // 16 MiB 解压后上限，防御异常压缩膨胀
	diskCacheStaleTempAge = 10 * time.Minute
)

var errNotRegularFile = errors.New("cached entry is not a regular file")

// DiskCacheStats 描述本地磁盘缓存目录中的当前有效条目与体积统计。
type DiskCacheStats struct {
	Dir           string `json:"dir"`
	Entries       int    `json:"entries"`
	FetchEntries  int    `json:"fetch_entries"`
	SearchEntries int    `json:"search_entries"`
	SizeBytes     int64  `json:"size_bytes"`
}

// CacheModeInfo 描述 CLI 或 MCP 单一运行形态经父子继承后的生效缓存策略。
type CacheModeInfo struct {
	Enabled    bool   `json:"enabled"`
	FetchTTL   string `json:"fetch_ttl"`
	SearchTTL  string `json:"search_ttl"`
	MaxEntries int    `json:"max_entries,omitzero"`
}

// CacheInfoResult 是 veri cache info [--json] 的统一输出结构。
type CacheInfoResult struct {
	Enabled  bool           `json:"enabled"`
	MaxBytes string         `json:"max_bytes"`
	CLI      CacheModeInfo  `json:"cli"`
	MCP      CacheModeInfo  `json:"mcp"`
	Disk     DiskCacheStats `json:"disk"`
}

// CacheClearResult 是 veri cache clear [--json] 的统一输出结构。
type CacheClearResult struct {
	Dir            string `json:"dir"`
	RemovedEntries int    `json:"removed_entries"`
	FreedBytes     int64  `json:"freed_bytes"`
}

type diskCacheRecord struct {
	Version   int            `json:"version"`
	Kind      string         `json:"kind"`
	CreatedAt time.Time      `json:"created_at"`
	ExpiresAt time.Time      `json:"expires_at"`
	Envelope  *core.Envelope `json:"envelope"`
}

type diskCache struct {
	mu         sync.Mutex
	rootDir    string
	maxBytes   int64
	maxEntries int
	logger     *slog.Logger
	now        func() time.Time
}

func newDiskCache(rootDir string, maxBytes int64, logger *slog.Logger) *diskCache {
	if strings.TrimSpace(rootDir) == "" || maxBytes <= 0 {
		return nil
	}
	return &diskCache{
		rootDir:    filepath.Clean(rootDir),
		maxBytes:   maxBytes,
		maxEntries: diskCacheMaxEntries,
		logger:     logger,
		now:        time.Now,
	}
}

func (c *diskCache) versionDir() string {
	return filepath.Join(c.rootDir, diskCacheSubdir)
}

func (c *diskCache) entryPath(kind, key string) (string, bool) {
	if !isValidDiskCacheKind(kind) || !isHexSHA256(key) {
		return "", false
	}
	return filepath.Join(c.versionDir(), kind+"-"+key+".json.gz"), true
}

func (c *diskCache) Get(kind, key string) (*core.Envelope, bool) {
	return c.GetWithMaxAge(kind, key, 0)
}

// GetWithMaxAge 读取本地磁盘缓存条目；当 maxAge > 0 时额外校验条目创建时间未超出调用方允许的新鲜度上限。
// 若条目仅超出调用方 maxAge 但尚未超过自身 ExpiresAt，不删除磁盘文件以免误清 CLI 侧仍有效的缓存。
func (c *diskCache) GetWithMaxAge(kind, key string, maxAge time.Duration) (*core.Envelope, bool) {
	if c == nil || c.rootDir == "" {
		return nil, false
	}
	path, ok := c.entryPath(kind, key)
	if !ok {
		return nil, false
	}
	rec, err := readDiskCacheRecord(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errNotRegularFile) {
			_ = os.Remove(path)
		}
		return nil, false
	}
	now := c.now()
	if rec.Version != diskCacheVersion || rec.Kind != kind || rec.Envelope == nil || !now.Before(rec.ExpiresAt) {
		_ = os.Remove(path)
		return nil, false
	}
	if maxAge > 0 && !rec.CreatedAt.IsZero() && !now.Before(rec.CreatedAt.Add(maxAge)) {
		return nil, false
	}
	_ = os.Chtimes(path, now, now)
	return rec.Envelope, true
}

func (c *diskCache) Set(kind, key string, env *core.Envelope, ttl time.Duration) {
	if c == nil || c.rootDir == "" || env == nil || ttl <= 0 {
		return
	}
	path, ok := c.entryPath(kind, key)
	if !ok {
		return
	}
	now := c.now()
	rec := diskCacheRecord{
		Version:   diskCacheVersion,
		Kind:      kind,
		CreatedAt: now.UTC(),
		ExpiresAt: now.Add(ttl).UTC(),
		Envelope:  env,
	}
	compressed, err := encodeDiskCacheRecord(&rec)
	if err != nil {
		return
	}
	if int64(len(compressed)) > c.maxBytes {
		_ = os.Remove(path)
		return
	}
	if err := storage.WriteFileAtomic(path, compressed, 0o700, 0o600); err != nil {
		if c.logger != nil {
			c.logger.Debug("disk_cache_write_failed", "kind", kind, "err", err)
		}
		return
	}
	_ = os.Chtimes(path, now, now)
	c.prune(now, filepath.Base(path))
}

type cacheFileMeta struct {
	path    string
	name    string
	size    int64
	modTime time.Time
}

func (c *diskCache) prune(now time.Time, protectName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	dir := c.versionDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var files []cacheFileMeta
	var totalBytes int64
	for _, de := range entries {
		if !de.Type().IsRegular() {
			continue
		}
		name := de.Name()
		info, infoErr := de.Info()
		if infoErr != nil {
			continue
		}
		fullPath := filepath.Join(dir, name)
		if isDiskCacheTempFile(name) {
			if now.Sub(info.ModTime()) > diskCacheStaleTempAge {
				_ = os.Remove(fullPath)
			}
			continue
		}
		if _, ok := parseDiskCacheEntryName(name); !ok {
			continue
		}
		sz := max(info.Size(), 0)
		totalBytes += sz
		files = append(files, cacheFileMeta{
			path:    fullPath,
			name:    name,
			size:    sz,
			modTime: info.ModTime(),
		})
	}

	if totalBytes <= c.maxBytes && len(files) <= c.maxEntries {
		return
	}

	slices.SortFunc(files, func(a, b cacheFileMeta) int {
		if a.modTime.Before(b.modTime) {
			return -1
		}
		if a.modTime.After(b.modTime) {
			return 1
		}
		return strings.Compare(a.name, b.name)
	})

	remaining := len(files)
	for _, f := range files {
		if totalBytes <= c.maxBytes && remaining <= c.maxEntries {
			break
		}
		if f.name == protectName {
			continue
		}
		_ = os.Remove(f.path)
		totalBytes = max(totalBytes-f.size, 0)
		remaining--
	}
}

// InspectCache 读取统一缓存策略并统计当前磁盘缓存目录中的有效条目数与占用空间（自动忽略并清理过期/损坏文件）。
func InspectCache(loaded *config.Loaded) (CacheInfoResult, error) {
	if loaded == nil {
		var err error
		loaded, err = config.Load("")
		if err != nil {
			return CacheInfoResult{}, err
		}
	}
	cfg := loaded.Config.Cache
	cliPol := cfg.EffectiveCLI()
	mcpPol := cfg.EffectiveMCP()
	maxBytesRaw, _ := cfg.MaxBytes.MarshalText()

	stats := statDiskCache(loaded.CacheDir, time.Now())
	return CacheInfoResult{
		Enabled:  cfg.Enabled,
		MaxBytes: string(maxBytesRaw),
		CLI: CacheModeInfo{
			Enabled:   cliPol.Enabled,
			FetchTTL:  cliPol.FetchTTL.String(),
			SearchTTL: cliPol.SearchTTL.String(),
		},
		MCP: CacheModeInfo{
			Enabled:    mcpPol.Enabled,
			FetchTTL:   mcpPol.FetchTTL.String(),
			SearchTTL:  mcpPol.SearchTTL.String(),
			MaxEntries: mcpPol.MaxEntries,
		},
		Disk: stats,
	}, nil
}

// ClearDiskCache 仅精准删除 <cacheDir>/v1/ 下符合 veri 缓存文件命名规范的条目与临时文件，严禁对目录执行无差别递归删除。
func ClearDiskCache(cacheDir string) (CacheClearResult, error) {
	cleanDir := filepath.Clean(strings.TrimSpace(cacheDir))
	res := CacheClearResult{Dir: cleanDir}
	if cleanDir == "" || cleanDir == "." {
		return res, nil
	}
	vDir := filepath.Join(cleanDir, diskCacheSubdir)
	entries, err := os.ReadDir(vDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return res, nil
		}
		return res, err
	}
	for _, de := range entries {
		if !de.Type().IsRegular() {
			continue
		}
		name := de.Name()
		_, isEntry := parseDiskCacheEntryName(name)
		isTemp := isDiskCacheTempFile(name)
		if !isEntry && !isTemp {
			continue
		}
		var sz int64
		if info, infoErr := de.Info(); infoErr == nil && info.Size() > 0 {
			sz = info.Size()
		}
		if rmErr := os.Remove(filepath.Join(vDir, name)); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return res, rmErr
		}
		if isEntry {
			res.RemovedEntries++
			res.FreedBytes += sz
		}
	}
	return res, nil
}

func statDiskCache(cacheDir string, now time.Time) DiskCacheStats {
	cleanDir := filepath.Clean(strings.TrimSpace(cacheDir))
	stats := DiskCacheStats{Dir: cleanDir}
	if cleanDir == "" || cleanDir == "." {
		return stats
	}
	vDir := filepath.Join(cleanDir, diskCacheSubdir)
	entries, err := os.ReadDir(vDir)
	if err != nil {
		return stats
	}
	for _, de := range entries {
		if !de.Type().IsRegular() {
			continue
		}
		name := de.Name()
		kind, ok := parseDiskCacheEntryName(name)
		if !ok {
			continue
		}
		fullPath := filepath.Join(vDir, name)
		rec, readErr := readDiskCacheRecord(fullPath)
		if readErr != nil || rec.Version != diskCacheVersion || rec.Kind != kind || rec.Envelope == nil || !now.Before(rec.ExpiresAt) {
			_ = os.Remove(fullPath)
			continue
		}
		stats.Entries++
		switch kind {
		case diskCacheKindFetch:
			stats.FetchEntries++
		case diskCacheKindSearch:
			stats.SearchEntries++
		}
		if info, infoErr := de.Info(); infoErr == nil && info.Size() > 0 {
			stats.SizeBytes += info.Size()
		}
	}
	return stats
}

func encodeDiskCacheRecord(rec *diskCacheRecord) ([]byte, error) {
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := gz.Write(raw); err != nil {
		_ = gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func readDiskCacheRecord(path string) (*diskCacheRecord, error) {
	cleanPath := filepath.Clean(path)
	fi, err := os.Lstat(cleanPath)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegularFile
	}
	f, err := os.Open(cleanPath) // #nosec G304 -- path constrained to managed cacheDir/v1/<kind>-<sha256>.json.gz
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()

	raw, err := io.ReadAll(io.LimitReader(gz, diskCacheMaxReadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > diskCacheMaxReadBytes {
		return nil, errors.New("cached entry exceeds decompressed size safety limit")
	}
	var rec diskCacheRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func isValidDiskCacheKind(kind string) bool {
	return kind == diskCacheKindFetch || kind == diskCacheKindSearch
}

func parseDiskCacheEntryName(name string) (string, bool) {
	const suffix = ".json.gz"
	if !strings.HasSuffix(name, suffix) {
		return "", false
	}
	stem := strings.TrimSuffix(name, suffix)
	kind, hexKey, ok := strings.Cut(stem, "-")
	if !ok || !isValidDiskCacheKind(kind) || !isHexSHA256(hexKey) {
		return "", false
	}
	return kind, true
}

func isDiskCacheTempFile(name string) bool {
	return (strings.HasPrefix(name, "."+diskCacheKindFetch+"-") || strings.HasPrefix(name, "."+diskCacheKindSearch+"-")) &&
		strings.Contains(name, ".tmp-")
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
