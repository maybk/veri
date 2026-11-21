package app

import (
	"fmt"
	"net/url"
	"time"

	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
)

// ProviderStatus 描述单个内置 Provider 的启用状态、凭据来源、目标端点、超时与运行参数。
type ProviderStatus struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Status   string `json:"status"`            // enabled | disabled
	Source   string `json:"source"`            // env:XXX | secrets.json | config.toml | missing | optional | -
	Endpoint string `json:"endpoint"`          // 目标 API 主机或 "-"
	Timeout  string `json:"timeout,omitempty"` // 生效超时（如 10s 或 10s/25s），未启用时为空
	Details  string `json:"details"`           // 生效运行参数或配置指引
	Enabled  bool   `json:"enabled"`
}

// Deps 聚合构造 Provider 所需的共享基础设施依赖。
type Deps struct {
	APIClient   *httpx.Client
	FetchClient *httpx.Client
	Version     string
}

// Factory 是显式 Provider 注册工厂函数签名（禁止使用 init() 隐式注册）。
type Factory func(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error)

var factories = []Factory{
	buildDeepSeek,
	buildKeenable,
	buildZhihu,
	buildExa,
	buildTinyFish,
	buildTencentWSA,
	buildTavily,
	buildBrave,
	buildLinkup,
	buildParallel,
	buildFirecrawl,
	buildGitHub,
	buildLocal,
}

func formatProviderTimeout(searchTimeout, fetchTimeout time.Duration) string {
	switch {
	case searchTimeout > 0 && fetchTimeout > 0 && fetchTimeout != searchTimeout:
		return fmt.Sprintf("%s/%s", searchTimeout, fetchTimeout)
	case searchTimeout > 0:
		return searchTimeout.String()
	case fetchTimeout > 0:
		return fetchTimeout.String()
	default:
		return "-"
	}
}

func disabledByConfigEntry(name, source, endpoint string) ProviderStatus {
	info, _ := spec.LookupProvider(name)
	return ProviderStatus{
		Name:     name,
		Role:     info.Roles.String(),
		Status:   "disabled",
		Source:   source,
		Endpoint: endpoint,
		Details:  spec.DetailDisabledInConfig,
		Enabled:  false,
	}
}

func hostFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "-"
	}
	return u.Host
}

// statusEntry 按 Provider 登记表生成状态行：角色取自登记声明，未启用且需要凭据时给出可直接执行的配置指引。
func statusEntry(name string, enabled bool, source, endpoint, timeout, details string) ProviderStatus {
	info, _ := spec.LookupProvider(name)
	st := "disabled"
	if enabled {
		st = "enabled"
	} else {
		timeout = ""
		if info.NeedsCredential() {
			details = fmt.Sprintf("run `veri key set %s` or set %s", info.Name, info.EnvVars[0])
		}
	}
	return ProviderStatus{
		Name:     name,
		Role:     info.Roles.String(),
		Status:   st,
		Source:   source,
		Endpoint: endpoint,
		Timeout:  timeout,
		Details:  details,
		Enabled:  enabled,
	}
}
