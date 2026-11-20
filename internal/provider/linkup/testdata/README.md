# Linkup 测试样本 (`internal/provider/linkup/testdata`)

存放 Linkup `/v1/search` 与 `/v1/fetch` 离线契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `search_ok.json`：Linkup Search API 成功响应脱敏样本（含 `name` 标题字段、待过滤的 `image` 类型与 `favicon`）。
- `fetch_ok.json`：Linkup Fetch API 正文提取脱敏样本（无 `title` 字段，验证一级标题回填）。
- `err_400_no_result.json`：HTTP 400 `SEARCH_QUERY_NO_RESULT` 无结果响应样本。
- `err_429_credit.json`：HTTP 429 `INSUFFICIENT_FUNDS_CREDITS` 余额耗尽错误样本。
- `err_429_rate.json`：HTTP 429 `TOO_MANY_REQUESTS` 并发限流错误样本。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入注释；样本来源与用途统一在本 `README.md` 中记录。
- **彻底脱敏**：禁止包含真实 API Key 或账户私有信息。
