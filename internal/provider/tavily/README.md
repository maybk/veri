# Tavily 检索与提取适配器 (`internal/provider/tavily`)

对接 Tavily `/search` 与 `/extract` 接口，同时实现 `core.Searcher` 与 `core.Fetcher`。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认端点、检索/提取深度、条数及域名上限常量。
- `provider.go`：适配器构造、深度枚举校验、检索/提取能力声明与 `usage.credits` 计费换算。
- `wire.go`：`/search`、`/extract` 请求/响应结构与 `usage.credits` 计费字段定义。
- `search.go`：检索参数组装、语言码归一化、严格过滤开关与结果清洗。
- `fetch.go`：调用 `/extract` 提取 Markdown 正文，缺失 `Title` 时从首行一级标题回填。
- `errors.go`：标准状态码与 Tavily 专属状态码（`432` / `433` / `434`）映射。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **专属状态码处理**：`432` / `433` 映射为 `insufficient_balance` 以触发密钥冷却与回退，`434` 映射为 `upstream_unavailable`。
- **可选 Credit 计费**：仅在配置 `price_credit` 时优先按响应 `usage.credits` 核算 `Cost`，未返回 `usage` 时按深度折算。

## 验证

```bash
go test -race ./internal/provider/tavily/...
```
