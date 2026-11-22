package update

import (
	"errors"
	"fmt"
	"strings"

	"veri/clikit/redact"
	"veri/internal/core"
)

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
