# URL 规范化与去重键 (`internal/urlnorm`)

提供无状态 URL 清洗、常见营销追踪参数剥离与多源检索融合去重键生成。

## 目录结构

- `urlnorm.go`：协议/主机名小写化、默认端口剥离、营销追踪参数过滤（`StripTrackingParams`）与融合比较键（`Key`）提取。
- `target.go`：本地与内网目标判定（`IsLocalOrPrivateTarget`）、PDF 链接识别（`LooksLikePDFURL`）与中文域名判定（`IsLikelyChineseHost`）。

## 开发规范

- **零外部依赖**：仅依赖 Go 标准库，保持确定性纯函数行为，不发起任何网络或 DNS 请求。
- **语义保真**：仅移除已知追踪参数与片段锚点，保留影响页面实际内容的业务查询参数。

## 验证

```bash
go test -race ./internal/urlnorm/...
```
