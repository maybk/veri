# Parallel 检索与提取适配器 (`internal/provider/parallel`)

对接 Parallel 官方正式 REST API `POST /v1/search` 与 `POST /v1/extract`，同时实现 `core.DynamicSearcher`、`core.Fetcher` 与 `core.FetchProfiler`。

## 目录结构

- `doc.go`：包职责与安全边界说明。
- `defaults.go`：默认端点、模式、条数、站点上限与 `fetch_policy` 边界常量。
- `provider.go`：适配器构造、`mode`/`location` 校验、按 `mode` 动态收紧能力边界（`DynamicSearcher`）与 PDF/JS `FetchProfile` 声明。
- `wire.go`：严格对齐 OpenAPI 3.1.0 的 `/v1/search`、`/v1/extract` 请求/响应与错误体定义。
- `search.go`：默认显式传 `mode=fast`、遵循 `objective` 与 `search_queries` 防重复约束、`source_policy` 规范化与冲突拦截、`after_date` 及结果映射。
- `fetch.go`：固定 `advanced_settings.full_content=true`、常规抓取保留服务端智能缓存、`--fresh` 映射 `fetch_policy` 与预算钳制、`errors[]` 精确映射、过滤全文提取无关摘录警告及 `excerpts` 兜底。
- `errors.go`：外层 HTTP 错误与 Extract HTTP 200 内层 `errors[]` 双层错误分类。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **成本与模式守门**：显式默认 `mode=fast`，绝不省略 `mode` 触发上游默认的 5 倍价 `advanced`；`turbo` 模式通过 `CapabilitiesFor` 自动声明 `SiteHostOnly=true`。
- **认证与路径边界**：仅接正式 REST 并强制校验 `PARALLEL_API_KEY`，不接入匿名托管 MCP；`source_policy` 保留路径大小写敏感性并前置拦截黑白名单同项冲突。
- **不误拉黑健康密钥**：HTTP `403` 中明确指向模式/端点不支持或策略拦截的错误归为 `invalid_argument` / `blocked_by_policy`，不触发密钥池拉黑。

## 验证

```bash
go test -race ./internal/provider/parallel/...
```
