# Firecrawl v2 测试样本 (`internal/provider/firecrawl/testdata`)

存放 Firecrawl `/v2/scrape` 与 `/v2/search` 离线契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `scrape_ok.json`：Scrape API 成功响应脱敏样本（含重定向最终 URL 与 200 内部状态码）。
- `scrape_array_meta.json`：Scrape API 多态元数据脱敏样本（`metadata.title` 为 `[]string` 数组形态）。
- `scrape_404_inner.json`：外层 HTTP 200 + `success: true` 但内层 `metadata.statusCode: 404` 的双层错误样本。
- `search_ok.json`：Search API 成功响应脱敏样本。
- `err_402.json`：HTTP 402 `INSUFFICIENT_CREDITS` 配额耗尽错误样本。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入注释；样本来源与用途统一在本 `README.md` 中记录。
- **彻底脱敏**：禁止包含真实 `fc-` 密钥或账户私有信息。
