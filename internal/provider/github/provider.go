package github

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
	"veri/internal/spec"
)

// Options 配置 GitHub 专用提取器参数。
type Options struct {
	Tokens      []string
	APIBaseURL  string
	RawBaseURL  string
	Timeout     time.Duration
	Concurrency int
	MaxBytes    int64
	Version     string
}

// Provider 实现 core.SpecialistFetcher。
type Provider struct {
	apiBaseURL string
	rawBaseURL string
	timeout    time.Duration
	maxBytes   int64
	userAgent  string
	apiClient  *httpx.Client
	pool       *keypool.Pool
	gate       *httpx.Gate
}

// New 构造 GitHub 专用提取器（无需 Token 即可启用，恒返回 enabled=true）。
// api_base_url 与 raw_base_url 均为经 ResolveAPIBaseURL 校验的固定官方上游端点，统一使用 apiClient。
func New(opts Options, apiClient *httpx.Client) (*Provider, bool, error) {
	apiBase, err := httpx.ResolveAPIBaseURL(opts.APIBaseURL, DefaultAPIBaseURL)
	if err != nil {
		return nil, false, err
	}
	rawBase, err := httpx.ResolveAPIBaseURL(opts.RawBaseURL, DefaultRawBaseURL)
	if err != nil {
		return nil, false, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	conc := opts.Concurrency
	if conc <= 0 {
		conc = DefaultConcurrency
	}

	return &Provider{
		apiBaseURL: apiBase,
		rawBaseURL: rawBase,
		timeout:    timeout,
		maxBytes:   maxBytes,
		userAgent:  spec.UserAgent(opts.Version),
		apiClient:  apiClient,
		pool:       keypool.New(opts.Tokens...),
		gate:       httpx.NewGate(conc, 0),
	}, true, nil
}

// Name 返回 Provider 唯一标识。
func (p *Provider) Name() string { return ProviderName }

// APIBaseURL 返回生效的 GitHub API 根地址。
func (p *Provider) APIBaseURL() string { return p.apiBaseURL }

// Capabilities 返回提取器基础能力声明（特定站点匹配由 SpecialistFetcher.Match 承载）。
func (p *Provider) Capabilities() core.Capabilities {
	return core.Capabilities{}
}

// HasToken 返回是否配置了 GITHUB_TOKEN。
func (p *Provider) HasToken() bool {
	return p.pool != nil && p.pool.Size() > 0
}

// Match 判断目标 URL 是否命中受支持的结构化 GitHub 资源。
func (p *Provider) Match(u *url.URL) bool {
	return parseGitHubURL(u).kind != routeNone
}

type routeKind int

const (
	routeNone routeKind = iota
	routeReadme
	routeBlob
	routeIssue
	routePR
	routeRelease
	routeReleaseLatest
	routeDiscussion
	routeCommit
)

type parsedRoute struct {
	kind      routeKind
	owner     string
	repo      string
	ref       string
	filePath  string
	startLine int
	endLine   int
	number    int
	tag       string
	sha       string
}

var (
	lineRangeFragmentRe = regexp.MustCompile(`^L([1-9]\d*)(?:C\d+)?(?:-L?([1-9]\d*)(?:C\d+)?)?$`)
	commitSHARe         = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
)

func parseGitHubURL(u *url.URL) parsedRoute {
	if u == nil {
		return parsedRoute{}
	}
	host := strings.ToLower(u.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return parsedRoute{}
	}
	escapedPath := u.EscapedPath()
	escapedLower := strings.ToLower(escapedPath)
	if strings.Contains(escapedLower, "%5c") || strings.Contains(escapedLower, "%00") {
		return parsedRoute{}
	}
	rawTrimmed := strings.Trim(escapedPath, "/")
	if rawTrimmed == "" {
		return parsedRoute{}
	}
	rawSegs := strings.Split(rawTrimmed, "/")
	if len(rawSegs) < 2 {
		return parsedRoute{}
	}

	isReleaseTagRoute := len(rawSegs) >= 5 && strings.EqualFold(rawSegs[2], "releases") && strings.EqualFold(rawSegs[3], "tag")
	segs := make([]string, len(rawSegs))
	for i, rs := range rawSegs {
		allowEncodedSlash := isReleaseTagRoute && i >= 4
		if !allowEncodedSlash && strings.Contains(strings.ToLower(rs), "%2f") {
			return parsedRoute{}
		}
		decoded, err := url.PathUnescape(rs)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, `\`) {
			return parsedRoute{}
		}
		if allowEncodedSlash {
			for sub := range strings.SplitSeq(decoded, "/") {
				if sub == "" || sub == "." || sub == ".." {
					return parsedRoute{}
				}
			}
		}
		segs[i] = decoded
	}

	owner, repo := segs[0], strings.TrimSuffix(segs[1], ".git")
	if owner == "" || repo == "" || repo == "." || repo == ".." {
		return parsedRoute{}
	}
	if _, reserved := reservedTopPaths[strings.ToLower(owner)]; reserved {
		return parsedRoute{}
	}
	if len(segs) == 2 {
		return parsedRoute{kind: routeReadme, owner: owner, repo: repo}
	}

	switch segs[2] {
	case "blob":
		if len(segs) >= 5 {
			startLine, endLine := parseLineFragment(u.Fragment)
			return parsedRoute{
				kind:      routeBlob,
				owner:     owner,
				repo:      repo,
				ref:       segs[3],
				filePath:  strings.Join(segs[4:], "/"),
				startLine: startLine,
				endLine:   endLine,
			}
		}
	case "issues":
		if len(segs) >= 4 {
			if n, err := strconv.Atoi(segs[3]); err == nil && n > 0 {
				return parsedRoute{kind: routeIssue, owner: owner, repo: repo, number: n}
			}
		}
	case "pull":
		if len(segs) >= 4 {
			if n, err := strconv.Atoi(segs[3]); err == nil && n > 0 {
				return parsedRoute{kind: routePR, owner: owner, repo: repo, number: n}
			}
		}
	case "discussions":
		if len(segs) >= 4 {
			if n, err := strconv.Atoi(segs[3]); err == nil && n > 0 {
				return parsedRoute{kind: routeDiscussion, owner: owner, repo: repo, number: n}
			}
		}
	case "commit", "commits":
		if len(segs) == 4 && commitSHARe.MatchString(segs[3]) {
			return parsedRoute{kind: routeCommit, owner: owner, repo: repo, sha: segs[3]}
		}
	case "releases":
		if len(segs) == 4 && segs[3] == "latest" {
			return parsedRoute{kind: routeReleaseLatest, owner: owner, repo: repo}
		}
		if len(segs) >= 5 && segs[3] == "tag" {
			return parsedRoute{
				kind:  routeRelease,
				owner: owner,
				repo:  repo,
				tag:   strings.Join(segs[4:], "/"),
			}
		}
	}
	return parsedRoute{}
}

func parseLineFragment(fragment string) (int, int) {
	m := lineRangeFragmentRe.FindStringSubmatch(strings.TrimSpace(fragment))
	if m == nil {
		return 0, 0
	}
	start, err := strconv.Atoi(m[1])
	if err != nil || start <= 0 {
		return 0, 0
	}
	end := start
	if m[2] != "" {
		if parsedEnd, eErr := strconv.Atoi(m[2]); eErr == nil && parsedEnd >= start {
			end = parsedEnd
		}
	}
	return start, end
}
