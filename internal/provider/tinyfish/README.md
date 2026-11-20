# TinyFish 检索与动态提取适配器 (`internal/provider/tinyfish`)

对接 TinySearch 与 TinyFetch 双独立端点，提供网页检索与服务端动态渲染（SPA / JS）正文提取。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：检索与提取双端点默认地址、并发参数及边界常量。
- `provider.go`：适配器构造、双并发闸门初始化及检索/提取能力声明（`Capabilities` / `FetchProfile`）。
- `wire.go`：检索结果项与提取双数组响应（`results[]` + `errors[]`）定义。
- `search.go`：查询参数组装、中日韩语言自动补全、时间窗映射与学术字段提取。
- `fetch.go`：调用提取端点并解析成功页面或子项错误。
- `errors.go`：顶层错误码及 `errors[]` 子项（含目标站 HTTP `status`）映射。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **时间窗约束**：常规检索严禁混传 `recency_minutes` 与 `after_date` / `before_date`；`research_paper` 模式仅原生支持年份区间 `pub_year_min` / `pub_year_max`。
- **双数组响应检查**：TinyFetch 在 HTTP `200` 下通过 `errors[]` 返回单页失败原因，须结合 `error` 枚举与可选 `status` 映射错误码。

## 验证

```bash
go test -race ./internal/provider/tinyfish/...
```
