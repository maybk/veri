# Exa 检索与正文提取适配器 (`internal/provider/exa`)

对接 Exa `/search` 与 `/contents` 接口，实现语义/关键词混合检索与网页正文提取。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认端点、检索类型、过滤上限常量与类别归一化函数。
- `provider.go`：适配器构造、密钥池初始化及检索/提取能力声明（`Capabilities` / `FetchProfile`）。
- `wire.go`：`/search` 与 `/contents` 请求体、响应体及 `costDollars` 账单结构定义。
- `search.go`：组装内联内容与过滤参数、映射检索结果并提取精确账单。
- `fetch.go`：调用 `/contents` 提取正文并核验单 URL 状态项 `statuses[]`。
- `errors.go`：HTTP 状态码与 `statuses[].error` 到 `core.Code` 的映射。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **实收账单记账**：仅在响应返回 `costDollars.total` 时转换为微美元记录 `Cost`（`Estimated=false`），不硬编码估算单价。
- **类别过滤互斥守卫**：`category` 为 `company` 或 `people` 时禁用发布日期过滤，`people` 同时禁用 `excludeDomains`，防止触发 HTTP `400`。
- **提取状态核验**：`/contents` 返回 HTTP `200` 时仍须检查 `statuses[]` 中的单 URL 错误标记。

## 验证

```bash
go test -race ./internal/provider/exa/...
```
