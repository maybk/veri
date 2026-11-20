package tencentwsa

import (
	"fmt"
	"strings"

	"veri/internal/spec"
)

const (
	// ProviderName 是腾讯云元宝搜索适配器的唯一标识。
	ProviderName = spec.ProviderTencentWSA

	// DefaultBaseURL 是元宝搜索 API Key 接入端点根地址。
	DefaultBaseURL = spec.DefaultTencentWSABaseURL
	// DefaultEdition 默认版本档位（standard）。
	DefaultEdition = spec.WSAEditionStandard
	// DefaultTimeout 默认检索超时时间。
	DefaultTimeout = spec.DefaultProviderSearchTimeout
	// DefaultConcurrency 默认并发上限。
	DefaultConcurrency = spec.DefaultProviderConcurrency

	liteMaxCount      = 10
	maxExcludeSites   = 5
	maxAuthorityLevel = 5.0
)

var wsaCountSteps = []int{10, 20, 30, 40, 50}

func normalizeEdition(edition string) (string, error) {
	ed := strings.ToLower(strings.TrimSpace(edition))
	if ed == "" {
		ed = DefaultEdition
	}
	switch ed {
	case spec.WSAEditionLite, spec.WSAEditionStandard, spec.WSAEditionFlagship:
		return ed, nil
	default:
		return "", fmt.Errorf("unsupported tencent_wsa edition %q", edition)
	}
}
