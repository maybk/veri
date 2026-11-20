# 本地网页正文提取器 (`internal/provider/local`)

使用纯 Go 完成安全 HTTP 抓取、字符集探测转码、Readability 正文抽取、Markdown 转换与低质量/反爬页面判定，实现 `core.Fetcher`。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认抓取超时、响应字节上限、正文最少字符阈值与 User-Agent 模式默认值。
- `provider.go`：提取器构造与安全抓取客户端绑定。
- `fetch.go`：抓取主入口、响应头媒体类型前置校验与安全响应拉取。
- `extract_html.go`：HTML 字符集转码、Readability 正文抽取与语义容器回退渲染。
- `extract_text.go`：Markdown 协商、弱内容 HTML 自动降级、`text/plain` Markdown 保守嗅探与 JSON 格式化提取。
- `metadata.go`：Markdown YAML Frontmatter 解析与 HTML `<meta>` / `<time datetime>` 发布日期回退提取。
- `robots.go`：RFC 9309 多 User-Agent 组与最长前缀 `Allow` / `Disallow` 匹配。
- `errors.go`：网络超时、SSRF 拦截与非文本内容错误包装。
- `testdata/`：静态文章、SPA 空壳与过短页面 HTML 测试样本目录。

## 开发规范

- **第三方解析库收敛**：全仓仅允许在本包导入 `go-readability`，HTML 预处理与 Markdown 渲染统一委托 `internal/htmlmd`。
- **SSRF 默认开启**：底层必须使用 `httpx.NewSafeFetchClient`，默认阻断一切回环、内网与元数据地址访问。

## 验证

```bash
go test -race ./internal/provider/local/...
```
