# Firecrawl v2 提取与检索适配器 (`internal/provider/firecrawl`)

对接 Firecrawl `POST /v2/scrape` 与 `POST /v2/search` 接口，同时实现 `core.Fetcher` 与 `core.Searcher`，支持官方云端与开源自托管实例。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认端点、代理模式、PDF 页数上限、超时与并发常量。
- `provider.go`：适配器构造、官方云端/自托管密钥隔离判定与能力声明。
- `wire.go`：`/v2/scrape`、`/v2/search` 请求/响应结构体及多态元数据字段解析。
- `fetch.go`：固定 Markdown 提取、PDF 页数上限保护、`--fresh` 映射与 `metadata.statusCode` 双层状态检查。
- `search.go`：网页检索（不透传 `scrapeOptions`）、类别校验与 `tbs` 时间映射。
- `errors.go`：Firecrawl 专属错误码与 HTTP 状态码映射。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **云端必须配 Key**：凡指向 `firecrawl.dev` 域的官方端点缺 Key 时静默禁用，绝不回退免 Key 公共池；仅显式配置非官方自托管 `base_url` 时允许无 Key 运行。
- **PDF 成本保护**：默认向 `parsers` 传入 `maxPages=20`（可通过配置或 `-o firecrawl.max_pages` 调整），避免大文档按页扣光免费额度。
- **双层状态检查**：外层 HTTP 200 + `success: true` 仍须校验 `data.metadata.statusCode`，准确识别目标 404 与 403/429 反爬阻断。

## 验证

```bash
go test -race ./internal/provider/firecrawl/...
```
