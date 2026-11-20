package update

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
)

// Progress 表示自更新执行过程中的实时阶段与下载进度。
type Progress struct {
	Stage           string `json:"stage"`
	DownloadedBytes int64  `json:"downloaded_bytes,omitzero"`
	TotalBytes      int64  `json:"total_bytes,omitzero"`
	Percent         int    `json:"percent,omitzero"`
}

// ProgressFunc 接收自更新过程中的阶段和进度通知。
type ProgressFunc func(Progress)

// ApplyOptions 控制自更新执行参数。
type ApplyOptions struct {
	TargetVersion string
	Force         bool
	OnProgress    ProgressFunc
}

// ApplyResult 描述一次自更新操作的最终结果。
type ApplyResult struct {
	CurrentVersion  string        `json:"current_version"`
	TargetVersion   string        `json:"target_version"`
	Tag             string        `json:"tag"`
	Updated         bool          `json:"updated"`
	AlreadyLatest   bool          `json:"already_latest"`
	InstallMethod   InstallMethod `json:"install_method"`
	ExecutablePath  string        `json:"executable_path,omitempty"`
	ReleaseURL      string        `json:"release_url,omitempty"`
	AssetName       string        `json:"asset_name,omitempty"`
	AssetSHA256     string        `json:"asset_sha256,omitempty"`
	DownloadedBytes int64         `json:"downloaded_bytes,omitzero"`
}

// Apply 检查目标版本、校验安装来源、下载并验证 SHA-256，最后原子替换当前可执行文件。
func (u *Updater) Apply(ctx context.Context, opts ApplyOptions) (ApplyResult, error) {
	install, err := u.detectInstall(u.currentVersion)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := guardInstallMethod(install, u.currentVersion, opts); err != nil {
		return ApplyResult{}, err
	}

	report := func(p Progress) {
		if opts.OnProgress != nil {
			opts.OnProgress(p)
		}
	}

	report(Progress{Stage: StageChecking})
	info, err := u.checkWithInstall(ctx, opts.TargetVersion, install)
	if err != nil {
		return ApplyResult{}, err
	}

	result := ApplyResult{
		CurrentVersion: info.CurrentVersion,
		TargetVersion:  info.LatestVersion,
		Tag:            info.Tag,
		InstallMethod:  info.InstallMethod,
		ExecutablePath: info.ExecutablePath,
		ReleaseURL:     info.ReleaseURL,
		AssetName:      info.AssetName,
		AssetSHA256:    info.AssetSHA256,
	}

	if shouldSkipInstall(info, opts) {
		result.AlreadyLatest = true
		return result, nil
	}

	expectedSum, ok := ParseSHA256Digest(info.AssetSHA256)
	if !ok || info.AssetURL == "" || !u.validateAsset(info.AssetURL, info.Tag, info.AssetName) {
		return ApplyResult{}, errCheckFailed(core.CodeUpstreamBadResponse, "missing verified release asset or SHA-256 digest", nil)
	}

	// 下载前先清理上次可能遗留的 .old 并原子预检目录写权限，避免下载完才发现不可写。
	CleanupOldBackup(install.ExecutablePath)
	if err := CheckWritable(install.ExecutablePath); err != nil {
		return ApplyResult{}, err
	}

	tmpDir, err := os.MkdirTemp("", "veri-update-*")
	if err != nil {
		return ApplyResult{}, core.WrapError(core.CodeInternal, "failed to create temporary update directory", err).
			WithMsgKey("err.update_install_failed")
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	archivePath := filepath.Join(tmpDir, filepath.Base(info.AssetName))
	downloaded, err := u.downloadAndVerify(ctx, info, expectedSum, archivePath, report)
	if err != nil {
		return ApplyResult{}, err
	}
	result.DownloadedBytes = downloaded

	report(Progress{Stage: StageInstalling, DownloadedBytes: downloaded, TotalBytes: downloaded, Percent: 100})
	stagedBin := filepath.Join(tmpDir, BinaryNameForOS(u.goos))
	if err := ExtractBinary(archivePath, u.goos, stagedBin); err != nil {
		return ApplyResult{}, err
	}
	if err := ReplaceExecutable(install.ExecutablePath, stagedBin, u.goos); err != nil {
		return ApplyResult{}, err
	}

	result.Updated = true
	return result, nil
}

func guardInstallMethod(install InstallInfo, currentVersion string, opts ApplyOptions) error {
	if install.IsManagedByPackageManager() {
		return core.NewError(
			core.CodeBlockedByPolicy,
			fmt.Sprintf("veri at %s is managed by %s; run `%s` instead", install.ExecutablePath, install.Method, install.ManagedCommand),
		).
			WithHint(fmt.Sprintf("Run `%s` to upgrade.", install.ManagedCommand)).
			WithI18n("err.update_managed_install", "hint.update_managed_install").
			WithParam("method", string(install.Method)).
			WithParam("path", install.ExecutablePath).
			WithParam("command", install.ManagedCommand)
	}
	if install.Method == MethodSystem {
		return core.NewError(
			core.CodeBlockedByPolicy,
			fmt.Sprintf("veri at %s is in a system-managed directory and cannot self-update", install.ExecutablePath),
		).
			WithHint("Upgrade via your system package manager or install a user-local binary.").
			WithI18n("err.update_system_install", "hint.update_system_install").
			WithParam("path", install.ExecutablePath)
	}
	if install.Method == MethodDev && !opts.Force && strings.TrimSpace(opts.TargetVersion) == "" {
		ver := strings.TrimSpace(currentVersion)
		if ver == "" {
			ver = "dev"
		}
		return core.NewError(
			core.CodeBlockedByPolicy,
			fmt.Sprintf("refusing to overwrite development build (%s) without --force or --version", ver),
		).
			WithHint("Rebuild from source, or pass `--force` / `--version <ver>` to replace it intentionally.").
			WithI18n("err.update_dev_build", "hint.update_dev_build").
			WithParam("version", ver)
	}
	return nil
}

func shouldSkipInstall(info ReleaseInfo, opts ApplyOptions) bool {
	if opts.Force {
		return false
	}
	if strings.TrimSpace(opts.TargetVersion) != "" {
		return info.IsSameVersion
	}
	if info.IsSameVersion {
		return true
	}
	// 非开发版且本地版本号不低于线上最新版本时，无需覆盖。
	if !IsDevVersion(info.CurrentVersion) && !info.HasUpdate {
		return true
	}
	return false
}

func (u *Updater) candidateDownloadURLs(officialURL string) []string {
	if u.mirrorPrefix == "" {
		return []string{officialURL}
	}
	if !strings.HasPrefix(officialURL, "https://github.com/") && !strings.HasPrefix(officialURL, u.downloadBaseURL+"/") {
		return []string{officialURL}
	}
	return []string{u.mirrorPrefix + officialURL, officialURL}
}

func (u *Updater) downloadAndVerify(
	parentCtx context.Context,
	info ReleaseInfo,
	expectedSum, destPath string,
	report ProgressFunc,
) (int64, error) {
	urls := u.candidateDownloadURLs(info.AssetURL)
	var lastErr error
	for _, endpoint := range urls {
		if err := parentCtx.Err(); err != nil {
			return 0, err
		}
		n, err := u.downloadFromEndpoint(parentCtx, endpoint, info, expectedSum, destPath, report)
		if err == nil {
			return n, nil
		}
		// 摘要不匹配属于安全告警，严禁静默重试或回退。
		if se, ok := errors.AsType[*core.Error](err); ok && se.MsgKey == "err.update_checksum_mismatch" {
			return 0, err
		}
		lastErr = err
	}
	return 0, lastErr
}

func (u *Updater) downloadFromEndpoint(
	parentCtx context.Context,
	endpoint string,
	info ReleaseInfo,
	expectedSum, destPath string,
	report ProgressFunc,
) (int64, error) {
	ctx, cancel := context.WithTimeout(parentCtx, downloadRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, u.wrapError(core.CodeUpstreamUnavailable, "failed to create download request", "err.update_download_failed", err)
	}
	req.Header.Set("User-Agent", spec.UserAgent(u.currentVersion))

	resp, err := u.client.Do(req)
	if err != nil {
		if parentCtx.Err() != nil {
			return 0, parentCtx.Err()
		}
		return 0, u.wrapError(core.CodeUpstreamUnavailable, "failed to download update archive", "err.update_download_failed", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return 0, errDownloadFailed(core.CodeUpstreamUnavailable, fmt.Sprintf("update archive download failed with HTTP %d", resp.StatusCode)).
			WithStatus(resp.StatusCode)
	}

	totalBytes := resp.ContentLength
	if totalBytes <= 0 {
		totalBytes = info.AssetSize
	}
	if totalBytes > maxArchiveDownloadBytes {
		return 0, errDownloadFailed(core.CodeUpstreamBadResponse, "update archive exceeds maximum allowed download size")
	}

	outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // destPath 位于受控临时目录
	if err != nil {
		return 0, core.WrapError(core.CodeInternal, "failed to open temporary archive file", err).
			WithMsgKey("err.update_install_failed")
	}

	report(Progress{Stage: StageDownloading, DownloadedBytes: 0, TotalBytes: totalBytes, Percent: 0})

	hasher := sha256.New()
	writer := io.MultiWriter(outFile, hasher)
	limited := io.LimitReader(resp.Body, maxArchiveDownloadBytes+1)
	buf := make([]byte, 32*1024)

	var downloaded int64
	lastPct := -1
	var lastReport time.Time

	for {
		n, readErr := limited.Read(buf)
		if n > 0 {
			downloaded += int64(n)
			if downloaded > maxArchiveDownloadBytes {
				_ = outFile.Close()
				_ = os.Remove(destPath)
				return 0, errDownloadFailed(core.CodeUpstreamBadResponse, "update archive exceeds maximum allowed download size")
			}
			if _, wErr := writer.Write(buf[:n]); wErr != nil {
				_ = outFile.Close()
				_ = os.Remove(destPath)
				return 0, core.WrapError(core.CodeInternal, "failed to write downloaded archive", wErr).
					WithMsgKey("err.update_install_failed")
			}
			pct := 0
			if totalBytes > 0 {
				pct = min(int(downloaded*100/totalBytes), 100)
			}
			now := time.Now()
			if pct != lastPct && (pct == 100 || now.Sub(lastReport) >= progressReportInterval) {
				lastPct = pct
				lastReport = now
				report(Progress{Stage: StageDownloading, DownloadedBytes: downloaded, TotalBytes: totalBytes, Percent: pct})
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			_ = outFile.Close()
			_ = os.Remove(destPath)
			if parentCtx.Err() != nil {
				return 0, parentCtx.Err()
			}
			return 0, u.wrapError(core.CodeUpstreamUnavailable, "interrupted while reading update archive", "err.update_download_failed", readErr)
		}
	}

	if err := outFile.Close(); err != nil {
		_ = os.Remove(destPath)
		return 0, core.WrapError(core.CodeInternal, "failed to finalize downloaded archive", err).
			WithMsgKey("err.update_install_failed")
	}

	report(Progress{Stage: StageVerifying, DownloadedBytes: downloaded, TotalBytes: totalBytes, Percent: 100})
	actualSum := hex.EncodeToString(hasher.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(actualSum)), []byte(strings.ToLower(expectedSum))) != 1 {
		_ = os.Remove(destPath)
		return 0, core.NewError(core.CodeBlockedByPolicy, fmt.Sprintf("SHA-256 checksum mismatch for %s", info.AssetName)).
			WithHint("If using `--mirror`, retry directly from GitHub Releases and verify your network.").
			WithI18n("err.update_checksum_mismatch", "hint.update_checksum_mismatch").
			WithParam("asset", info.AssetName)
	}
	return downloaded, nil
}
