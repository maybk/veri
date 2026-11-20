# 敏感凭据遮蔽与脱敏日志 (`clikit/redact`)

提供零外部依赖的密钥前缀掩码、HTTP 认证头识别、URL 凭据与查询参数清洗及结构化 `slog.Logger` 自动脱敏包装器。

## 目录结构

- `doc.go`：包职责与纯标准库边界声明。
- `redact.go`：`MaskKey`、`MaskKeyWithPrefixes`、`IsSensitiveHeader`、`SanitizeURL` 与 `Scrub`/`ScrubWith` 实现，覆盖已知密钥、HTTP 认证头及内嵌 URL 凭据脱敏。
- `logger.go`：`NewLogger` 结构化文本/JSON 自动脱敏日志构造器。

## 开发规范

- **零明文泄露**：短于 12 字符的敏感串统一掩码为 `****`；长令牌仅保留已知前缀与末 4 位且至少遮蔽 6 个字符；无协议前缀或畸形 URL 兜底剥离内嵌账密并清洗查询串。
- **避免误伤正文**：仅对 `Authorization` / `Proxy-Authorization` 认证头执行 `Bearer`/`Basic` 脱敏，对合法 URL 仅替换敏感查询参数值，不改写普通自然语言与非敏感参数。
- **引用保留**：形如 `env:VAR_NAME` 的环境变量引用不含真实密钥，原样保留以支持排障。

## 验证

```bash
go test -race ./clikit/redact/...
```
