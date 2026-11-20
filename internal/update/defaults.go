package update

import (
	"time"

	"veri/internal/spec"
)

const (
	// StageChecking 表示正在查询最新版本元数据与校验清单。
	StageChecking = "checking"
	// StageDownloading 表示正在下载目标平台发布归档。
	StageDownloading = "downloading"
	// StageVerifying 表示正在校验下载归档的 SHA-256 摘要。
	StageVerifying = "verifying"
	// StageInstalling 表示正在安全解压并原子替换当前可执行文件。
	StageInstalling = "installing"

	defaultReleaseAPIURL    = "https://api.github.com/repos/" + spec.RepoSlug + "/releases/latest"
	defaultReleaseLatestURL = "https://github.com/" + spec.RepoSlug + "/releases/latest"
	defaultDownloadBaseURL  = "https://github.com/" + spec.RepoSlug + "/releases/download"
	sha256SumsAssetName     = "SHA256SUMS.txt"

	checkRequestTimeout     = 10 * time.Second
	downloadRequestTimeout  = 5 * time.Minute
	progressReportInterval  = 80 * time.Millisecond
	maxReleaseJSONBytes     = 1 << 20   // 1 MiB
	maxChecksumFileBytes    = 64 << 10  // 64 KiB
	maxArchiveDownloadBytes = 64 << 20  // 64 MiB
	maxExtractedBinaryBytes = 128 << 20 // 128 MiB
)
