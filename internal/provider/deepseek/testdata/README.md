# DeepSeek 测试样本 (`internal/provider/deepseek/testdata`)

存放 DeepSeek Search 与 Fetch 接口契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `search_ok.json`：DeepSeek Search API 成功响应脱敏样本（含相关性 `rerank_score`、权威度 `authority` 与长摘要）。
- `search_empty.json`：DeepSeek Search API 空结果脱敏样本（用于验证空结果零计费）。
- `fetch_ok.json`：DeepSeek Fetch API 网页正文提取成功响应脱敏样本。
- `error.json`：DeepSeek OpenAI 兼容错误响应（余额不足 `Insufficient Balance`）脱敏样本。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入注释；样本来源与用途统一在本 `README.md` 中记录。
- **彻底脱敏**：禁止包含真实 API Key、账户信息或未脱敏会话标识。
