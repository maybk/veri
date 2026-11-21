# Parallel 测试样本 (`internal/provider/parallel/testdata`)

存放 Parallel `/v1/search` 与 `/v1/extract` 离线契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `search_ok.json`：Parallel Search API 成功响应样本（含多段 `excerpts`、`publish_date`、`null` 字段与 `warnings`）。
- `extract_ok.json`：Parallel Extract API 全文提取成功样本（含 `full_content`、`publish_date` 与 `extract_id`）。
- `extract_inner_error.json`：Extract HTTP 200 内层 `errors[]` 目标站 404 失败样本。
- `err_402.json`：HTTP 402 余额不足错误样本。
- `err_422.json`：HTTP 422 请求校验失败错误样本。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入注释；样本来源与用途统一在本 `README.md` 中记录。
- **彻底脱敏**：禁止包含真实 API Key 或账户私有信息。
