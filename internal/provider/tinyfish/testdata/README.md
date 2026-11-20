# TinyFish 测试样本 (`internal/provider/tinyfish/testdata`)

存放 TinySearch 与 TinyFetch 双端点契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `search_ok.json`：TinySearch 成功响应脱敏样本。
- `search_empty.json`：TinySearch 空结果脱敏样本。
- `fetch_ok.json`：TinyFetch 提取成功响应脱敏样本，含 `final_url` 重定向与双数组结构。
- `error.json`：TinyFetch 单 URL 抓取失败脱敏样本，HTTP `200` 内嵌 `errors[].error = "bot_blocked"`。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入注释；样本来源与用途统一在本 `README.md` 中记录。
- **彻底脱敏**：禁止包含真实 API Key 或私有站点凭据。
