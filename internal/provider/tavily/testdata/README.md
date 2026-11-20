# Tavily 测试样本 (`internal/provider/tavily/testdata`)

存放 Tavily `/search` 与 `/extract` 接口契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `search_ok.json`：Tavily Search API 成功响应脱敏样本（含相关性 `score` 与 `usage.credits`）。
- `search_empty.json`：Tavily Search API 空结果脱敏样本。
- `extract_ok.json`：Tavily Extract API 正文提取脱敏样本（省略 `title` 字段以验证 Markdown 一级标题回填逻辑）。
- `error.json`：Tavily OpenAPI 配额超限错误响应脱敏样本。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入注释；样本来源与用途统一在本 `README.md` 中记录。
- **彻底脱敏**：禁止包含真实 `tvly-` 密钥或账户私有信息。
