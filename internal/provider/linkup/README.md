# Linkup 检索与提取适配器 (`internal/provider/linkup`)

对接 Linkup `POST /v1/search` 与 `POST /v1/fetch` 接口，同时实现 `core.Searcher` 与 `core.Fetcher`。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认端点、深度、提取模式与域名上限常量。
- `provider.go`：适配器构造、参数校验、能力声明与 PDF/JS `FetchProfile` 声明。
- `wire.go`：`/v1/search`、`/v1/fetch` 请求/响应结构体与错误体定义。
- `search.go`：固定 `outputType=searchResults`、`name` 标题映射、`SEARCH_QUERY_NO_RESULT` 空结果归一与日期夹紧。
- `fetch.go`：调用 `/v1/fetch` 提取 HTML/PDF 正文，遇反爬或 JS 拦截页自动按需升档 `renderJs` 并回填首行标题。
- `errors.go`：按官方线协议错误码精细拆分多义 HTTP `400`、`403` 与 `429`（区分余额耗尽与并发限流）。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **成本守门**：默认 `depth=fast`、`fetch_mode=standard`、`render_js=false`，避免意外触发 10 倍价 `deep` 或高价渲染。
- **多义状态码保护**：HTTP `429` 中的 `INSUFFICIENT_FUNDS_CREDITS` / `EXCEED_BUDGET_LIMIT` 映射为不可重试的 `insufficient_balance`，绝不触发无意义退避重试；HTTP `403` 中的 `IP_NOT_WHITELISTED` / `TASK_TYPE_NOT_SUPPORTED` 绝不误拉黑有效密钥。

## 验证

```bash
go test -race ./internal/provider/linkup/...
```
