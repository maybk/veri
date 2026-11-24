package github

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Fetch 按匹配的 GitHub 路由拉取并组装结构化 Markdown。
func (p *Provider) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	u, err := core.ValidateTargetURL(req.URL, ProviderName, maxFetchURLLength)
	if err != nil {
		return nil, err
	}
	rt := parseGitHubURL(u)
	if rt.kind == routeNone {
		return nil, core.NewError(core.CodeContentUnavailable, "URL is not a supported structured GitHub resource").WithProvider(ProviderName)
	}

	release, gateErr := p.gate.Acquire(ctx)
	if gateErr != nil {
		return nil, core.AsError(gateErr).WithProvider(ProviderName)
	}
	defer release()

	switch rt.kind {
	case routeReadme:
		return p.fetchReadme(ctx, u.String(), rt)
	case routeBlob:
		return p.fetchBlob(ctx, u.String(), rt)
	case routeIssue:
		return p.fetchIssue(ctx, u.String(), rt)
	case routePR:
		return p.fetchPR(ctx, u.String(), rt)
	case routeDiscussion:
		return p.fetchDiscussion(ctx, u.String(), rt)
	case routeCommit:
		return p.fetchCommit(ctx, u.String(), rt)
	case routeRelease, routeReleaseLatest:
		return p.fetchRelease(ctx, u.String(), rt)
	default:
		return nil, core.NewError(core.CodeContentUnavailable, "unsupported GitHub route").WithProvider(ProviderName)
	}
}

type ghResponse struct {
	body    []byte
	reqID   string
	noStore bool
}

func (p *Provider) fetchReadme(ctx context.Context, origURL string, rt parsedRoute) (*core.FetchOutcome, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/readme", p.apiBaseURL, url.PathEscape(rt.owner), url.PathEscape(rt.repo))
	res, err := p.doGitHubGET(ctx, p.apiClient, endpoint, "application/vnd.github.raw+json")
	if err != nil {
		return nil, err
	}
	content := textnorm.CleanPlainText(string(res.body))
	if content == "" {
		return nil, core.NewError(core.CodeContentUnavailable, "repository README is empty").WithProvider(ProviderName)
	}
	return newOutcome(origURL, fmt.Sprintf("%s/%s README", rt.owner, rt.repo), nil, content, core.SourceGitHubReadme, res.reqID, res.noStore), nil
}

func (p *Provider) fetchBlob(ctx context.Context, origURL string, rt parsedRoute) (*core.FetchOutcome, error) {
	escapedSegs := strings.Split(rt.filePath, "/")
	for i, s := range escapedSegs {
		escapedSegs[i] = url.PathEscape(s)
	}
	endpoint := fmt.Sprintf("%s/%s/%s/%s/%s",
		p.rawBaseURL,
		url.PathEscape(rt.owner),
		url.PathEscape(rt.repo),
		url.PathEscape(rt.ref),
		strings.Join(escapedSegs, "/"),
	)
	res, err := p.doGitHubGET(ctx, p.rawClient, endpoint, "text/plain")
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(res.body, 0) >= 0 || !utf8.Valid(res.body) {
		return nil, core.NewError(core.CodeContentUnavailable, "GitHub file is binary or non-UTF-8").
			WithProvider(ProviderName)
	}

	rawText := textnorm.CleanPlainText(string(res.body))
	if rawText == "" {
		return nil, core.NewError(core.CodeContentUnavailable, "GitHub file is empty").WithProvider(ProviderName)
	}

	title := fmt.Sprintf("%s/%s:%s", rt.owner, rt.repo, rt.filePath)
	if rt.startLine > 0 {
		rawText = sliceLines(rawText, rt.startLine, rt.endLine)
		if rawText == "" {
			return nil, core.NewError(core.CodeContentUnavailable, "requested line range is out of bounds").WithProvider(ProviderName)
		}
		if rt.endLine > rt.startLine {
			title = fmt.Sprintf("%s#L%d-L%d", title, rt.startLine, rt.endLine)
		} else {
			title = fmt.Sprintf("%s#L%d", title, rt.startLine)
		}
	}

	ext := strings.TrimPrefix(strings.ToLower(path.Ext(rt.filePath)), ".")
	content := rawText
	if rt.startLine > 0 || (ext != "md" && ext != "markdown" && ext != "mdx") {
		content = fmt.Sprintf("```%s\n%s\n```", ext, rawText)
	}
	return newOutcome(origURL, title, nil, content, core.SourceGitHubFile, res.reqID, res.noStore), nil
}

func sliceLines(text string, startLine, endLine int) string {
	lines := strings.Split(text, "\n")
	if startLine > len(lines) {
		return ""
	}
	end := min(endLine, len(lines))
	return strings.Join(lines[startLine-1:end], "\n")
}

func (p *Provider) fetchIssue(ctx context.Context, origURL string, rt parsedRoute) (*core.FetchOutcome, error) {
	issueURL := fmt.Sprintf("%s/repos/%s/%s/issues/%d", p.apiBaseURL, url.PathEscape(rt.owner), url.PathEscape(rt.repo), rt.number)
	res, err := p.doGitHubGET(ctx, p.apiClient, issueURL, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var issue issueWire
	if err := json.Unmarshal(res.body, &issue); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode GitHub issue JSON", err).WithProvider(ProviderName)
	}
	var labels []string
	for _, l := range issue.Labels {
		if l.Name != "" {
			labels = append(labels, l.Name)
		}
	}
	return p.renderThreadOutcome(ctx, origURL, rt, core.SourceGitHubIssue, res.reqID, res.noStore, discussionDoc{
		title:     issue.Title,
		state:     issue.State,
		author:    issue.User.Login,
		createdAt: issue.CreatedAt,
		labels:    labels,
		body:      issue.Body,
	})
}

func (p *Provider) fetchPR(ctx context.Context, origURL string, rt parsedRoute) (*core.FetchOutcome, error) {
	pullURL := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", p.apiBaseURL, url.PathEscape(rt.owner), url.PathEscape(rt.repo), rt.number)
	res, err := p.doGitHubGET(ctx, p.apiClient, pullURL, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var pr pullWire
	if err := json.Unmarshal(res.body, &pr); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode GitHub pull request JSON", err).WithProvider(ProviderName)
	}
	state := pr.State
	switch {
	case pr.Merged || pr.MergedAt != "":
		state = "merged"
	case pr.Draft && state == "open":
		state = "draft"
	}
	var labels []string
	for _, l := range pr.Labels {
		if l.Name != "" {
			labels = append(labels, l.Name)
		}
	}
	branches := ""
	if pr.Head.Ref != "" && pr.Base.Ref != "" {
		branches = pr.Head.Ref + " -> " + pr.Base.Ref
	}
	return p.renderThreadOutcome(ctx, origURL, rt, core.SourceGitHubPR, res.reqID, res.noStore, discussionDoc{
		title:     pr.Title,
		state:     state,
		branches:  branches,
		author:    pr.User.Login,
		createdAt: pr.CreatedAt,
		labels:    labels,
		body:      pr.Body,
	})
}

func (p *Provider) fetchRelease(ctx context.Context, origURL string, rt parsedRoute) (*core.FetchOutcome, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", p.apiBaseURL, url.PathEscape(rt.owner), url.PathEscape(rt.repo), url.PathEscape(rt.tag))
	if rt.kind == routeReleaseLatest {
		endpoint = fmt.Sprintf("%s/repos/%s/%s/releases/latest", p.apiBaseURL, url.PathEscape(rt.owner), url.PathEscape(rt.repo))
	}
	res, err := p.doGitHubGET(ctx, p.apiClient, endpoint, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var rel releaseWire
	if err := json.Unmarshal(res.body, &rel); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode GitHub release JSON", err).WithProvider(ProviderName)
	}

	heading := textnorm.CleanInline(rel.Name)
	if heading == "" {
		heading = rel.TagName
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s)\n\n", heading, rel.TagName)
	fmt.Fprintf(&b, "- **Repository**: %s/%s\n", rt.owner, rt.repo)
	if rel.Author.Login != "" {
		fmt.Fprintf(&b, "- **Author**: @%s\n", rel.Author.Login)
	}
	if rel.PublishedAt != "" {
		fmt.Fprintf(&b, "- **Published**: %s\n", rel.PublishedAt)
	}
	b.WriteString("\n")
	b.WriteString(textnorm.CleanPlainText(rel.Body))

	pub := textnorm.ParseFlexibleTime(rel.PublishedAt, time.UTC)
	return newOutcome(origURL, fmt.Sprintf("%s/%s %s", rt.owner, rt.repo, heading), pub, strings.TrimSpace(b.String()), core.SourceGitHubRelease, res.reqID, res.noStore), nil
}

func (p *Provider) doGitHubGET(ctx context.Context, client *httpx.Client, targetURL, accept string) (ghResponse, error) {
	if p.pool == nil || p.pool.Size() == 0 {
		return p.execSingleGET(ctx, client, targetURL, accept, keypool.Lease{}, false)
	}

	lease, hasToken := p.pool.Acquire(-1)
	if !hasToken {
		if p.pool.DiagnoseExhaustion() == keypool.ExhaustRateLimited {
			return ghResponse{}, core.NewError(core.CodeRateLimited, "all GitHub tokens are rate-limited and cooling down").WithProvider(ProviderName)
		}
		return p.execSingleGET(ctx, client, targetURL, accept, keypool.Lease{}, false)
	}

	res, err := p.execSingleGET(ctx, client, targetURL, accept, lease, true)
	if err == nil {
		return res, nil
	}

	se := core.AsError(err)
	if se.Code != core.CodeAuthFailed && se.Code != core.CodeRateLimited {
		return ghResponse{reqID: res.reqID}, err
	}

	// 遵循计费与配额安全规范：同池存在其他健康令牌时最多切换重试 1 次。
	if nextLease, more := p.pool.Acquire(lease.ID); more {
		res2, err2 := p.execSingleGET(ctx, client, targetURL, accept, nextLease, true)
		if err2 == nil {
			return res2, nil
		}
		res = res2
		err = err2
		se = core.AsError(err2)
	}

	// 若令牌因 401 鉴权失败且池中已无可用令牌，对公开仓库尝试 1 次匿名读取；若匿名也失败则保留原始 401 报错避免误报私有库 404。
	if se.Code == core.CodeAuthFailed && p.pool.DiagnoseExhaustion() == keypool.ExhaustAuthFailed {
		if anonRes, anonErr := p.execSingleGET(ctx, client, targetURL, accept, keypool.Lease{}, false); anonErr == nil {
			return anonRes, nil
		}
	}
	return ghResponse{reqID: res.reqID}, err
}

func (p *Provider) execSingleGET(ctx context.Context, client *httpx.Client, targetURL, accept string, lease keypool.Lease, hasToken bool) (ghResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return ghResponse{}, core.WrapError(core.CodeInvalidArgument, "invalid GitHub request URL", err).WithProvider(ProviderName)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", p.userAgent)
	if hasToken {
		req.Header.Set(spec.HeaderAuthorization, spec.AuthBearerPrefix+lease.Value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return ghResponse{}, core.AsError(err).WithProvider(ProviderName)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := httpx.ReadBodyLimited(resp, p.maxBytes)
	if readErr != nil {
		return ghResponse{}, core.AsError(readErr).WithProvider(ProviderName)
	}
	reqID := strings.TrimSpace(resp.Header.Get("X-GitHub-Request-Id"))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if hasToken {
			p.pool.ReportSuccess(lease.ID)
		}
		return ghResponse{
			body:    body,
			reqID:   reqID,
			noStore: httpx.HasNoStoreCacheControl(resp.Header),
		}, nil
	}

	classified := classifyGitHubError(resp.StatusCode, resp.Header, body)
	if hasToken {
		switch classified.Code {
		case core.CodeRateLimited:
			retryAfter := httpx.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
			p.pool.ReportRateLimited(lease.ID, retryAfter)
		case core.CodeAuthFailed:
			p.pool.ReportAuthFailed(lease.ID)
		}
	}
	return ghResponse{reqID: reqID}, classified
}

func newOutcome(origURL, title string, pub *time.Time, content, sourceType, reqID string, noStore bool) *core.FetchOutcome {
	return &core.FetchOutcome{
		Page: &core.Page{
			URL:        origURL,
			Title:      title,
			Published:  pub,
			Content:    content,
			TotalChars: textnorm.RuneCount(content),
			Provider:   ProviderName,
			SourceType: sourceType,
		},
		NoStore:   noStore,
		RequestID: reqID,
	}
}
