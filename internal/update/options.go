package update

import "strings"

// Option 定义更新器的可选依赖注入项。
type Option func(*Updater)

// WithHTTPClient 注入自定义 HTTP 执行器（优先使用 *httpx.Client 或离线测试替身）。
func WithHTTPClient(client HTTPDoer) Option {
	return func(u *Updater) {
		if client != nil {
			u.client = client
		}
	}
}

// WithProxyURL 配置创建默认 httpx 客户端时使用的网络代理地址。
func WithProxyURL(proxyURL string) Option {
	return func(u *Updater) {
		u.proxyURL = strings.TrimSpace(proxyURL)
	}
}

// WithMirrorPrefix 配置可选的归档下载镜像前缀（元数据与 SHA-256 摘要始终仅从官方源获取）。
func WithMirrorPrefix(prefix string) Option {
	return func(u *Updater) {
		u.mirrorPrefix = strings.TrimSpace(prefix)
	}
}

// WithPlatform 覆盖目标操作系统与架构（主要用于跨平台离线测试）。
func WithPlatform(goos, goarch string) Option {
	return func(u *Updater) {
		if goos != "" {
			u.goos = goos
		}
		if goarch != "" {
			u.goarch = goarch
		}
	}
}

// WithEndpoints 覆盖发布查询与下载地址前缀（仅用于离线测试）。
func WithEndpoints(apiURL, latestURL, downloadBase string) Option {
	return func(u *Updater) {
		if apiURL != "" {
			u.releaseAPIURL = apiURL
		}
		if latestURL != "" {
			u.releaseLatest = latestURL
		}
		if downloadBase != "" {
			u.downloadBaseURL = strings.TrimRight(downloadBase, "/")
		}
	}
}

// WithURLValidators 覆盖官方 URL 白名单校验器（仅用于离线测试）。
func WithURLValidators(releaseFn func(rawURL, tag string) bool, assetFn func(rawURL, tag, assetName string) bool) Option {
	return func(u *Updater) {
		if releaseFn != nil {
			u.validateRelease = releaseFn
		}
		if assetFn != nil {
			u.validateAsset = assetFn
		}
	}
}

// WithInstallDetector 覆盖当前进程安装形态探测器（主要用于离线测试）。
func WithInstallDetector(fn func(version string) (InstallInfo, error)) Option {
	return func(u *Updater) {
		if fn != nil {
			u.detectInstall = fn
		}
	}
}
