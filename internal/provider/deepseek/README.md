# DeepSeek 检索与提取适配器 (`internal/provider/deepseek`)

对接 DeepSeek Search（`/v1/tools/search`）与 Fetch（`/v1/tools/fetch`）接口，同时实现 `core.Searcher` 与 `core.Fetcher`。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认基础端点、超时、并发上限与查询最大字符长度常量。
- `provider.go`：适配器构造、密钥池与并发闸门初始化及原生过滤能力声明。
- `wire.go`：检索与网页提取的请求/响应 JSON 传输结构定义。
- `search.go`：检索操作符拼装（`site:` / `-site:` / `lang:` / `after:` / `before:`）、128 字符长度预检与结果归一化。
- `fetch.go`：网页提取调用与 `core.Page` 结构映射。
- `errors.go`：HTTP 状态码与响应体错误消息到 `core.Code` 的分类映射。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **长度前置校验**：拼装过滤操作符后的查询长度超过 128 个 Unicode 字符时，必须在本地直接返回带 `Provider="deepseek"` 的 `invalid_argument` 错误。
- **可选成本计算**：仅在显式配置了 `price_search` / `price_fetch` 且检索结果非空（或提取成功）时生成 `Cost`。

## 验证

```bash
go test -race ./internal/provider/deepseek/...
```
