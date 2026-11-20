# Brave 测试样本 (`internal/provider/brave/testdata`)

存放 Brave Search 接口契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `search_ok.json`：LLM Context 成功响应脱敏样本，覆盖 4 元素 `age` 时间戳与空 `age` 数组边界。
- `web_search_ok.json`：Web Search 成功响应脱敏样本，覆盖 `extra_snippets`、`page_age` 与相对 `age` 回退解析。
- `search_empty.json`：LLM Context 空结果脱敏样本。
- `error.json`：HTTP `422` `ErrorResponse` 参数校验错误脱敏样本。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入 `//` 或 `/* */` 注释；样本来源与覆盖场景统一在本 `README.md` 中记录。
- **彻底脱敏**：禁止包含真实订阅令牌、账号标识或私有请求头。
