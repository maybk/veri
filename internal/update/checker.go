package update

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"

	"veri/clikit/redact"
	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
)

// HTTPDoer 抽象最小 HTTP 执行接口，生产环境由 internal/httpx.Client 提供实现。
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ReleaseInfo 描述一次版本更新检查的完整结果。
type ReleaseInfo struct {
	CurrentVersion string        `json:"current_version"`
	LatestVersion  string        `json:"latest_version"`
	Tag            string        `json:"tag"`
	HasUpdate      bool          `json:"has_update"`
	IsSameVersion  bool          `json:"is_same_version"`
	ReleaseURL     string        `json:"release_url,omitempty"`
	AssetName      string        `json:"asset_name,omitempty"`
	AssetURL       string        `json:"asset_url,omitempty"`
	AssetSize      int64         `json:"asset_size,omitzero"`
	AssetSHA256    string        `json:"asset_sha256,omitempty"`
	InstallMethod  InstallMethod `json:"install_method"`
	ExecutablePath string        `json:"executable_path,omitempty"`
	ManagedCommand string        `json:"managed_command,omitempty"`
	CanSelfUpdate  bool          `json:"can_self_update"`
}

// Updater 负责向官方 GitHub Releases 查询版本元数据、校验摘要并执行跨平台安全自更新。
type Updater struct {
	currentVersion  string
	goos            string
	goarch          string
	proxyURL        string
	mirrorPrefix    string
	scrubber        *strings.Replacer
	client          HTTPDoer
	releaseAPIURL   string
	releaseLatest   string
	downloadBaseURL string
	validateRelease func(rawURL, tag string) bool
	validateAsset   func(rawURL, tag, assetName string) bool
	detectInstall   func(version string) (InstallInfo, error)
}

// New 创建自更新服务实例；网络请求默认经由 internal/httpx.NewAPIClient 执行。
func New(currentVersion string, opts ...Option) (*Updater, error) {
	u := &Updater{
		currentVersion:  strings.TrimSpace(currentVersion),
		goos:            runtime.GOOS,
		goarch:          runtime.GOARCH,
		releaseAPIURL:   defaultReleaseAPIURL,
		releaseLatest:   defaultReleaseLatestURL,
		downloadBaseURL: defaultDownloadBaseURL,
		validateRelease: IsValidReleaseURL,
		validateAsset:   IsValidAssetDownloadURL,
		detectInstall:   DetectCurrentInstall,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(u)
		}
	}
	if u.mirrorPrefix != "" {
		validated, err := ValidateMirrorPrefix(u.mirrorPrefix)
		if err != nil {
			return nil, err
		}
		u.mirrorPrefix = validated
	}
	if u.proxyURL != "" {
		if pu, pErr := url.Parse(strings.TrimSpace(u.proxyURL)); pErr == nil && pu.User != nil {
			if pw, ok := pu.User.Password(); ok && pw != "" {
				u.scrubber = redact.NewScrubber([]string{pw})
			}
		}
	}
	if u.client == nil {
		hc, err := httpx.NewAPIClient(httpx.Options{ProxyURL: u.proxyURL})
		if err != nil {
			return nil, u.wrapError(core.CodeInvalidArgument, err.Error(), "", err)
		}
		u.client = hc
	}
	return u, nil
}

const (
	updateCheckRetryHint    = "Check your network or proxy settings and retry."
	updateDownloadRetryHint = "Check your network or proxy settings and retry, or pass `--mirror <https-url>` for archive downloads."
)

func (u *Updater) wrapError(code core.Code, msg, msgKey string, cause error) *core.Error {
	var rep *strings.Replacer
	if u != nil {
		rep = u.scrubber
	}
	safeMsg := redact.ScrubWith(msg, rep)
	safeCause := cause
	if cause != nil {
		raw := cause.Error()
		if cleaned := redact.ScrubWith(raw, rep); cleaned != raw {
			safeCause = errors.New(cleaned)
		}
	}
	se := core.WrapError(code, safeMsg, safeCause)
	switch msgKey {
	case "err.update_check_failed":
		se = se.WithHint(updateCheckRetryHint).WithI18n("err.update_check_failed", "hint.update_check_failed")
	case "err.update_download_failed":
		se = se.WithHint(updateDownloadRetryHint).WithI18n("err.update_download_failed", "hint.update_download_failed")
	case "":
	default:
		se = se.WithMsgKey(msgKey)
	}
	return se
}

func errCheckFailed(code core.Code, msg string, cause error) *core.Error {
	return core.WrapError(code, msg, cause).
		WithHint(updateCheckRetryHint).
		WithI18n("err.update_check_failed", "hint.update_check_failed")
}

func errDownloadFailed(code core.Code, msg string) *core.Error {
	return core.NewError(code, msg).
		WithHint(updateDownloadRetryHint).
		WithI18n("err.update_download_failed", "hint.update_download_failed")
}

func errNoPublishedRelease() *core.Error {
	return core.NewError(core.CodeNotFound, "no published release found").
		WithHint("Wait for a published stable release on GitHub Releases, or build from source.").
		WithI18n("err.update_no_release", "hint.update_no_release")
}

func errUnsupportedPlatform(goos, goarch string) *core.Error {
	platform := goos + "/" + goarch
	return core.NewError(core.CodeBlockedByPolicy, fmt.Sprintf("precompiled release asset is not available for %s", platform)).
		WithHint("Build from source, or check GitHub Releases for supported platforms.").
		WithI18n("err.update_unsupported_platform", "hint.update_unsupported_platform").
		WithParam("platform", platform)
}

func errAssetMissing(tag, asset string) *core.Error {
	return core.NewError(core.CodeNotFound, fmt.Sprintf("release %s does not include verified asset %s", tag, asset)).
		WithHint("Check GitHub Releases for your platform archive, or build from source.").
		WithI18n("err.update_asset_missing", "hint.update_asset_missing").
		WithParam("asset", asset).
		WithParam("version", tag)
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

type githubReleasePayload struct {
	TagName    string               `json:"tag_name"`
	HTMLURL    string               `json:"html_url"`
	Draft      bool                 `json:"draft"`
	Prerelease bool                 `json:"prerelease"`
	Assets     []githubReleaseAsset `json:"assets"`
}

// Check 查询最新正式版本或指定正式版本，并校验当前平台对应的发布资产与官方 SHA-256 摘要。
func (u *Updater) Check(ctx context.Context, targetVersion string) (ReleaseInfo, error) {
	install, err := u.detectInstall(u.currentVersion)
	if err != nil {
		return ReleaseInfo{}, err
	}
	return u.checkWithInstall(ctx, targetVersion, install)
}

func (u *Updater) checkWithInstall(ctx context.Context, targetVersion string, install InstallInfo) (ReleaseInfo, error) {
	if strings.TrimSpace(targetVersion) != "" {
		return u.checkExplicitVersion(ctx, targetVersion, install)
	}

	if err := u.ensureSupportedPlatform(); err != nil {
		return ReleaseInfo{}, err
	}

	info, apiErr := u.fetchLatestViaAPI(ctx, install)
	if apiErr == nil {
		return info, nil
	}
	if ctx.Err() != nil {
		return ReleaseInfo{}, ctx.Err()
	}
	// API 返回 404 时明确表示仓库尚无正式发布，无需再试重定向；其余限流/网络/解析异常自动降级至 302 重定向。
	if se, ok := errors.AsType[*core.Error](apiErr); ok && se.Code == core.CodeNotFound {
		return ReleaseInfo{}, apiErr
	}
	redirectInfo, redirErr := u.fetchLatestViaRedirect(ctx, install)
	if redirErr == nil {
		return redirectInfo, nil
	}
	if ctx.Err() != nil {
		return ReleaseInfo{}, ctx.Err()
	}
	if se, ok := errors.AsType[*core.Error](redirErr); ok && se.Code == core.CodeNotFound {
		return ReleaseInfo{}, redirErr
	}
	return ReleaseInfo{}, apiErr
}

func (u *Updater) ensureSupportedPlatform() error {
	if IsSupportedPlatform(u.goos, u.goarch) {
		return nil
	}
	return errUnsupportedPlatform(u.goos, u.goarch)
}

func (u *Updater) checkExplicitVersion(ctx context.Context, rawVersion string, install InstallInfo) (ReleaseInfo, error) {
	ver, tag, ok := NormalizeReleaseVersion(rawVersion)
	if !ok {
		return ReleaseInfo{}, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid release version %q (expected MAJOR.MINOR.PATCH)", rawVersion)).
			WithMsgKey("err.update_invalid_version").
			WithParam("version", strings.TrimSpace(rawVersion))
	}
	if err := u.ensureSupportedPlatform(); err != nil {
		return ReleaseInfo{}, err
	}
	assetName, _ := ExpectedAssetName(ver, u.goos, u.goarch)
	return u.resolveFromChecksums(ctx, ver, tag, assetName, install, true)
}

func (u *Updater) fetchLatestViaAPI(ctx context.Context, install InstallInfo) (ReleaseInfo, error) {
	reqCtx, cancel := context.WithTimeout(ctx, checkRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.releaseAPIURL, nil)
	if err != nil {
		return ReleaseInfo{}, u.wrapError(core.CodeUpstreamUnavailable, "failed to build release API request", "err.update_check_failed", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", spec.UserAgent(u.currentVersion))

	resp, err := u.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ReleaseInfo{}, ctx.Err()
		}
		return ReleaseInfo{}, u.wrapError(core.CodeUpstreamUnavailable, "failed to query latest release", "err.update_check_failed", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return ReleaseInfo{}, errNoPublishedRelease()
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return ReleaseInfo{}, errCheckFailed(core.CodeRateLimited, fmt.Sprintf("release API rate limited (HTTP %d)", resp.StatusCode), nil).
			WithStatus(resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return ReleaseInfo{}, errCheckFailed(core.CodeUpstreamUnavailable, fmt.Sprintf("unexpected release API status %d", resp.StatusCode), nil).
			WithStatus(resp.StatusCode)
	}

	var payload githubReleasePayload
	if err := json.UnmarshalRead(io.LimitReader(resp.Body, maxReleaseJSONBytes), &payload); err != nil {
		return ReleaseInfo{}, errCheckFailed(core.CodeUpstreamBadResponse, "invalid release API response", err)
	}

	tag := strings.TrimSpace(payload.TagName)
	latest, normTag, ok := NormalizeReleaseVersion(tag)
	releaseURL := strings.TrimSpace(payload.HTMLURL)
	if !ok || tag != normTag || payload.Draft || payload.Prerelease || !u.validateRelease(releaseURL, tag) {
		return ReleaseInfo{}, errCheckFailed(core.CodeUpstreamBadResponse, "invalid or non-stable release metadata", nil)
	}

	assetName, ok := ExpectedAssetName(latest, u.goos, u.goarch)
	if !ok {
		return ReleaseInfo{}, errUnsupportedPlatform(u.goos, u.goarch)
	}

	info := u.buildBaseReleaseInfo(latest, tag, releaseURL, install)
	asset, found := u.resolveAPIAsset(ctx, tag, assetName, payload.Assets)
	if !found {
		return ReleaseInfo{}, errAssetMissing(tag, assetName)
	}

	info.AssetName = asset.Name
	info.AssetURL = asset.BrowserDownloadURL
	info.AssetSize = asset.Size
	info.AssetSHA256 = asset.Digest
	info.CanSelfUpdate = install.CanSelfUpdate() && info.AssetSHA256 != ""
	return info, nil
}

func (u *Updater) resolveAPIAsset(ctx context.Context, tag, assetName string, assets []githubReleaseAsset) (githubReleaseAsset, bool) {
	byName := make(map[string]githubReleaseAsset, len(assets))
	for _, a := range assets {
		name := strings.TrimSpace(a.Name)
		dlURL := strings.TrimSpace(a.BrowserDownloadURL)
		if name == "" || dlURL == "" {
			continue
		}
		a.Name = name
		a.BrowserDownloadURL = dlURL
		byName[name] = a
	}

	asset, exists := byName[assetName]
	if !exists || !u.validateAsset(asset.BrowserDownloadURL, tag, asset.Name) {
		return githubReleaseAsset{}, false
	}
	if sum, ok := ParseSHA256Digest(asset.Digest); ok {
		asset.Digest = sum
		return asset, true
	}
	sumsAsset, hasSums := byName[sha256SumsAssetName]
	if !hasSums || !u.validateAsset(sumsAsset.BrowserDownloadURL, tag, sha256SumsAssetName) {
		return githubReleaseAsset{}, false
	}
	sum, err := u.fetchChecksumFromURL(ctx, sumsAsset.BrowserDownloadURL, asset.Name, false, tag)
	if err != nil {
		return githubReleaseAsset{}, false
	}
	asset.Digest = sum
	return asset, true
}

func (u *Updater) fetchLatestViaRedirect(ctx context.Context, install InstallInfo) (ReleaseInfo, error) {
	reqCtx, cancel := context.WithTimeout(ctx, checkRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.releaseLatest, nil)
	if err != nil {
		return ReleaseInfo{}, u.wrapError(core.CodeUpstreamUnavailable, "failed to build latest release request", "err.update_check_failed", err)
	}
	req.Header.Set("User-Agent", spec.UserAgent(u.currentVersion))

	resp, err := u.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ReleaseInfo{}, ctx.Err()
		}
		return ReleaseInfo{}, u.wrapError(core.CodeUpstreamUnavailable, "failed to resolve latest release redirect", "err.update_check_failed", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ReleaseInfo{}, errNoPublishedRelease()
	}
	if resp.StatusCode != http.StatusOK || resp.Request == nil || resp.Request.URL == nil {
		return ReleaseInfo{}, errCheckFailed(core.CodeUpstreamUnavailable, fmt.Sprintf("unexpected status %d resolving latest release", resp.StatusCode), nil).
			WithStatus(resp.StatusCode)
	}

	finalURL := resp.Request.URL
	tag, hasTag := extractTagFromReleaseURL(finalURL)
	if !hasTag {
		// 仓库尚未发布任何正式 Release 时，GitHub 会将 /releases/latest 重定向回 /releases。
		if strings.HasSuffix(strings.TrimRight(finalURL.Path, "/"), "/releases") {
			return ReleaseInfo{}, errNoPublishedRelease()
		}
		return ReleaseInfo{}, errCheckFailed(core.CodeUpstreamBadResponse, "unexpected redirect target for latest release", nil)
	}

	ver, normTag, ok := NormalizeReleaseVersion(tag)
	if !ok || tag != normTag || !u.validateRelease(finalURL.String(), tag) {
		return ReleaseInfo{}, errCheckFailed(core.CodeUpstreamBadResponse, fmt.Sprintf("invalid latest release tag %q", tag), nil)
	}
	assetName, ok := ExpectedAssetName(ver, u.goos, u.goarch)
	if !ok {
		return ReleaseInfo{}, errUnsupportedPlatform(u.goos, u.goarch)
	}
	return u.resolveFromChecksums(ctx, ver, tag, assetName, install, false)
}

func (u *Updater) resolveFromChecksums(
	ctx context.Context,
	ver, tag, assetName string,
	install InstallInfo,
	explicitVersion bool,
) (ReleaseInfo, error) {
	sumsURL := u.downloadBaseURL + "/" + tag + "/" + sha256SumsAssetName
	assetURL := u.downloadBaseURL + "/" + tag + "/" + assetName
	if !u.validateAsset(sumsURL, tag, sha256SumsAssetName) || !u.validateAsset(assetURL, tag, assetName) {
		return ReleaseInfo{}, errCheckFailed(core.CodeUpstreamBadResponse, "rejected untrusted release asset URL", nil)
	}

	sum, err := u.fetchChecksumFromURL(ctx, sumsURL, assetName, explicitVersion, tag)
	if err != nil {
		return ReleaseInfo{}, err
	}

	releaseURL, _ := ReleaseTagURL(tag)
	info := u.buildBaseReleaseInfo(ver, tag, releaseURL, install)
	info.AssetName = assetName
	info.AssetURL = assetURL
	info.AssetSHA256 = sum
	info.CanSelfUpdate = install.CanSelfUpdate()
	return info, nil
}

func (u *Updater) fetchChecksumFromURL(ctx context.Context, sumsURL, assetName string, explicitVersion bool, tag string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, checkRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return "", u.wrapError(core.CodeUpstreamUnavailable, "failed to build checksum request", "err.update_check_failed", err)
	}
	req.Header.Set("User-Agent", spec.UserAgent(u.currentVersion))

	resp, err := u.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", u.wrapError(core.CodeUpstreamUnavailable, "failed to download SHA256SUMS.txt", "err.update_check_failed", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		if explicitVersion {
			return "", core.NewError(core.CodeNotFound, fmt.Sprintf("release %s not found", tag)).
				WithHint("Verify the version tag on GitHub Releases, or run `veri update --check` to see the latest release.").
				WithI18n("err.update_version_not_found", "hint.update_version_not_found").
				WithParam("version", tag)
		}
		return "", errAssetMissing(tag, sha256SumsAssetName)
	}
	if resp.StatusCode != http.StatusOK {
		return "", errCheckFailed(core.CodeUpstreamUnavailable, fmt.Sprintf("unexpected status %d downloading SHA256SUMS.txt", resp.StatusCode), nil).
			WithStatus(resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumFileBytes+1))
	if err != nil || len(raw) > maxChecksumFileBytes {
		return "", errCheckFailed(core.CodeUpstreamBadResponse, "failed to read SHA256SUMS.txt within size limit", nil)
	}

	sum, ok := ParseSHA256Sums(string(raw), assetName)
	if !ok {
		return "", errAssetMissing(tag, assetName)
	}
	return sum, nil
}

func (u *Updater) buildBaseReleaseInfo(latest, tag, releaseURL string, install InstallInfo) ReleaseInfo {
	cur := strings.TrimPrefix(strings.TrimSpace(u.currentVersion), "v")
	return ReleaseInfo{
		CurrentVersion: cur,
		LatestVersion:  latest,
		Tag:            tag,
		HasUpdate:      IsNewerVersion(cur, latest),
		IsSameVersion:  IsSameVersion(cur, latest),
		ReleaseURL:     releaseURL,
		InstallMethod:  install.Method,
		ExecutablePath: install.ExecutablePath,
		ManagedCommand: install.ManagedCommand,
	}
}

func extractTagFromReleaseURL(u *url.URL) (string, bool) {
	if u == nil {
		return "", false
	}
	segments := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(segments) >= 2 && segments[len(segments)-2] == "tag" {
		tag := segments[len(segments)-1]
		if isValidReleaseTag(tag) {
			return tag, true
		}
	}
	return "", false
}
