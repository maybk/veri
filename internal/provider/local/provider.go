package local

import (
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
)

// Options 配置本地提取器参数。
type Options struct {
	Timeout         time.Duration
	Concurrency     int
	MaxBytes        int64
	MinContentRunes int
	RespectRobots   bool
	UserAgent       string // "browser" | "veri" | 自定义字符串
	Version         string
}

// Provider 实现 core.Fetcher。
type Provider struct {
	timeout         time.Duration
	maxBytes        int64
	minContentRunes int
	respectRobots   bool
	userAgent       string
	client          *httpx.Client
	gate            *httpx.Gate
}

// New 构造本地通用提取器（无需密钥，恒返回 enabled=true）。
func New(opts Options, safeClient *httpx.Client) (*Provider, bool, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	minRunes := opts.MinContentRunes
	if minRunes < 0 {
		minRunes = DefaultMinContentRunes
	}
	conc := opts.Concurrency
	if conc <= 0 {
		conc = DefaultConcurrency
	}

	return &Provider{
		timeout:         timeout,
		maxBytes:        maxBytes,
		minContentRunes: minRunes,
		respectRobots:   opts.RespectRobots,
		userAgent:       resolveUserAgent(opts.UserAgent, opts.Version),
		client:          safeClient,
		gate:            httpx.NewGate(conc, 0),
	}, true, nil
}

// Name 返回 Provider 唯一标识。
func (p *Provider) Name() string { return ProviderName }

// Capabilities 声明为通用提取器。
func (p *Provider) Capabilities() core.Capabilities {
	return core.Capabilities{}
}
