# Exa 测试样本 (`internal/provider/exa/testdata`)

存放 Exa `/search` 与 `/contents` 接口契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `search_ok.json`：`/search` 成功响应脱敏样本，含 `highlights`、`highlightScores` 与 `costDollars`。
- `search_empty.json`：`/search` 空结果脱敏样本。
- `contents_ok.json`：`/contents` 正文提取成功响应脱敏样本，含 `statuses` 与 `costDollars`。
- `error.json`：`FEATURE_DISABLED` 错误响应脱敏样本。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入注释；样本来源与用途统一在本 `README.md` 中记录。
- **彻底脱敏**：仅保留合成的测试 `requestId`，禁止包含真实密钥或账号信息。
