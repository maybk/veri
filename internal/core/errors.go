package core

import (
	"errors"
	"fmt"
)

// Code 是 veri 对外统一的标准错误码枚举。
type Code string

const (
	// CodeInvalidArgument 参数不合法。
	CodeInvalidArgument Code = "invalid_argument"
	// CodeNoProvider 没有可用的 Provider（未配置密钥或因能力不匹配全部跳过）。
	CodeNoProvider Code = "no_provider"
	// CodeAuthFailed 密钥缺失或鉴权失败。
	CodeAuthFailed Code = "auth_failed"
	// CodeNotEnabled 上游服务或插件未开通。
	CodeNotEnabled Code = "not_enabled"
	// CodeInsufficientBalance 账户余额或配额耗尽。
	CodeInsufficientBalance Code = "insufficient_balance"
	// CodeRateLimited 触发频率或并发限制。
	CodeRateLimited Code = "rate_limited"
	// CodeUpstreamTimeout 上游响应超时。
	CodeUpstreamTimeout Code = "upstream_timeout"
	// CodeUpstreamUnavailable 上游 5xx 故障或网络连接失败。
	CodeUpstreamUnavailable Code = "upstream_unavailable"
	// CodeUpstreamBadResponse 上游返回无法解析的响应格式。
	CodeUpstreamBadResponse Code = "upstream_bad_response"
	// CodeBlockedByPolicy 内容安全拦截或抓取被访问控制策略拒绝。
	CodeBlockedByPolicy Code = "blocked_by_policy"
	// CodeNotFound 目标页面不存在（HTTP 404 / 410）。
	CodeNotFound Code = "not_found"
	// CodeContentUnavailable 页面无正文、非文本类型、被挑战页阻断或被 SSRF 守卫拒绝。
	CodeContentUnavailable Code = "content_unavailable"
	// CodeInternal 程序内部未预期错误。
	CodeInternal Code = "internal"
)

// AllCodes 返回全部标准错误码，供双语词表完整性测试校验。
func AllCodes() []Code {
	return []Code{
		CodeInvalidArgument,
		CodeNoProvider,
		CodeAuthFailed,
		CodeNotEnabled,
		CodeInsufficientBalance,
		CodeRateLimited,
		CodeUpstreamTimeout,
		CodeUpstreamUnavailable,
		CodeUpstreamBadResponse,
		CodeBlockedByPolicy,
		CodeNotFound,
		CodeContentUnavailable,
		CodeInternal,
	}
}

// IsRetryable 返回某错误码默认是否属于可瞬时重试类别。
func (c Code) IsRetryable() bool {
	switch c {
	case CodeRateLimited, CodeUpstreamTimeout, CodeUpstreamUnavailable, CodeUpstreamBadResponse:
		return true
	default:
		return false
	}
}

// ExitCode 返回该错误码对应的标准 CLI 进程退出码。
func (c Code) ExitCode() int {
	switch c {
	case CodeInvalidArgument:
		return 2
	case CodeNoProvider, CodeAuthFailed, CodeNotEnabled, CodeInsufficientBalance:
		return 3
	case CodeRateLimited, CodeUpstreamTimeout, CodeUpstreamUnavailable, CodeUpstreamBadResponse:
		return 4
	case CodeBlockedByPolicy, CodeNotFound, CodeContentUnavailable:
		return 5
	default:
		return 1
	}
}

// 跨包共享的安全拦截与挑战识别提示词表键常量。
const (
	HintKeySSRFPrivateBlocked = "hint.ssrf_private_blocked"
	HintKeySSRFHostNonPublic  = "hint.ssrf_host_non_public"
	HintKeyChallengeDetected  = "hint.challenge_detected"
)

// IsSSRFBlockedHint 判断给定 HintKey 是否对应本地 SSRF 非公网拦截（禁止向公网云端源转发）。
func IsSSRFBlockedHint(hintKey string) bool {
	return hintKey == HintKeySSRFPrivateBlocked || hintKey == HintKeySSRFHostNonPublic
}

// Error 是 veri 内部与边界层统一传递的结构化错误类型。
// Message 与 Hint 固定使用英文，供机器协议、MCP 与日志消费；CLI 人类终端由 i18n 按 Code、MsgKey、HintKey 与 Params 渲染。
type Error struct {
	Code      Code
	Provider  string
	Message   string
	MsgKey    string
	Hint      string
	HintKey   string
	Retryable bool
	Status    int
	Cause     error
	Params    map[string]string
}

// NewError 创建一个新的结构化 Error，并根据 Code 初始化默认 Retryable。
func NewError(code Code, message string) *Error {
	return &Error{
		Code:      code,
		Message:   message,
		Retryable: code.IsRetryable(),
	}
}

// WrapError 包装底层原因错误并创建结构化 Error。
func WrapError(code Code, message string, cause error) *Error {
	e := NewError(code, message)
	e.Cause = cause
	return e
}

// Error 实现标准库 error 接口。
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	prefix := string(e.Code)
	if e.Provider != "" {
		prefix = e.Provider + ": " + prefix
	}
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", prefix, e.Message)
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", prefix, e.Cause)
	}
	return prefix
}

// Unwrap 支持 errors.Is 与 errors.As 链式解包。
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// WithProvider 设置产生该错误的 Provider 标识。
func (e *Error) WithProvider(provider string) *Error {
	e.Provider = provider
	return e
}

// WithHint 设置面向模型与调用方的英文修复提示。
func (e *Error) WithHint(hint string) *Error {
	e.Hint = hint
	return e
}

// WithMsgKey 设置面向人类 CLI 终端的本地化错误消息词表键。
func (e *Error) WithMsgKey(key string) *Error {
	e.MsgKey = key
	return e
}

// WithHintKey 设置面向人类 CLI 终端的本地化修复提示词表键。
func (e *Error) WithHintKey(key string) *Error {
	e.HintKey = key
	return e
}

// WithI18n 同时设置面向人类 CLI 终端的本地化错误消息键与修复提示键。
func (e *Error) WithI18n(msgKey, hintKey string) *Error {
	e.MsgKey = msgKey
	e.HintKey = hintKey
	return e
}

// WithStatus 设置上游 HTTP 状态码。
func (e *Error) WithStatus(status int) *Error {
	e.Status = status
	return e
}

// WithRetryable 显式覆盖 Retryable 标志。
func (e *Error) WithRetryable(retryable bool) *Error {
	e.Retryable = retryable
	return e
}

// WithParam 添加本地化模板插值参数。
func (e *Error) WithParam(key, value string) *Error {
	if e.Params == nil {
		e.Params = make(map[string]string, 2)
	}
	e.Params[key] = value
	return e
}

// DefaultHint 返回该错误码面向机器协议、--json 与 MCP 的默认英文修复提示。
func (c Code) DefaultHint() string {
	switch c {
	case CodeInvalidArgument:
		return "Check command flags or query syntax with `veri --help`."
	case CodeNoProvider:
		return "Run `veri key set <provider>` (e.g. deepseek, exa, tinyfish) or set its API key environment variable."
	case CodeAuthFailed:
		return "Verify your API key; update via `veri key set <provider>` or inspect sources with `veri key list`."
	case CodeNotEnabled:
		return "Enable the search service or plugin in the provider console and run `veri doctor`."
	case CodeInsufficientBalance:
		return "Top up your account balance or switch to another provider."
	case CodeRateLimited:
		return "Wait briefly before retrying or configure additional API keys."
	case CodeUpstreamTimeout:
		return "Retry with a larger `--timeout` or switch provider."
	case CodeUpstreamUnavailable:
		return "Check network connectivity or try another provider."
	case CodeUpstreamBadResponse:
		return "Retry later or switch provider with `-p`."
	case CodeBlockedByPolicy:
		return "Refine the query or try a different source URL."
	case CodeNotFound:
		return "Verify the URL spelling or check whether the page was moved or removed."
	case CodeContentUnavailable:
		return "Check that the URL is reachable and points to a readable web page."
	default:
		return "Re-run with `VERI_LOG_LEVEL=debug` for diagnostic details."
	}
}

// ToErrorInfo 转换为输出信封中的机器可读 ErrorInfo。
func (e *Error) ToErrorInfo() *ErrorInfo {
	if e == nil {
		return nil
	}
	hint := e.Hint
	if hint == "" {
		hint = e.Code.DefaultHint()
	}
	return &ErrorInfo{
		Code:      e.Code,
		Provider:  e.Provider,
		Message:   e.Message,
		Hint:      hint,
		Retryable: e.Retryable,
		Status:    e.Status,
	}
}

// AsError 从任意 error 提取 *Error；若不是 *Error 则包装为 CodeInternal。
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	if target, ok := errors.AsType[*Error](err); ok && target != nil {
		return target
	}
	return WrapError(CodeInternal, err.Error(), err)
}

// ShouldFallback 判断在 auto 链式路由中遇到该错误时是否应当尝试下一个检索源。
func ShouldFallback(err error) bool {
	if err == nil {
		return false
	}
	se := AsError(err)
	switch se.Code {
	case CodeRateLimited,
		CodeUpstreamTimeout,
		CodeUpstreamUnavailable,
		CodeUpstreamBadResponse,
		CodeAuthFailed,
		CodeInsufficientBalance,
		CodeNotEnabled:
		return true
	case CodeInvalidArgument:
		// 仅厂商特有约束（带有 Provider 归属，如 DeepSeek 128 字上限、Brave 50 词上限）允许回退下一家；
		// 路由层前置校验的全局非法参数（Provider 为空）不回退。
		return se.Provider != ""
	case CodeBlockedByPolicy:
		// 内容安全拦截不跨检索源回退，防止重复触发风控与重复计费。
		return false
	default:
		return false
	}
}

// MostDecisiveCode 从一组错误码中选出最具确定性的主错误码，避免链尾瞬态超时掩盖前序明确的目标或凭据错误。
// 优先级：目标自身属性（not_found > blocked_by_policy > content_unavailable）> 账号/参数错误 > 瞬态网络故障 > internal。
func MostDecisiveCode(codes []Code) Code {
	best := Code("")
	bestRank := 999
	for _, c := range codes {
		if c == "" {
			continue
		}
		if r := codeDecisivenessRank(c); r < bestRank {
			bestRank = r
			best = c
		}
	}
	return best
}

func codeDecisivenessRank(c Code) int {
	switch c {
	case CodeNotFound:
		return 1
	case CodeBlockedByPolicy:
		return 2
	case CodeContentUnavailable:
		return 3
	case CodeAuthFailed, CodeInsufficientBalance, CodeNotEnabled, CodeNoProvider:
		return 4
	case CodeInvalidArgument:
		return 5
	case CodeRateLimited:
		return 6
	case CodeUpstreamUnavailable:
		return 7
	case CodeUpstreamBadResponse:
		return 8
	case CodeUpstreamTimeout:
		return 9
	default:
		return 10
	}
}
