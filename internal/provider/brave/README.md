# Brave 检索适配器 (`internal/provider/brave`)

对接 Brave `/res/v1/web/search` 与 `/res/v1/llm/context` 双端点，实现 `core.Searcher`。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认端点、查询长度/词数上限、Token 预算与条数常量。
- `provider.go`：适配器构造、运行模式校验及静态/动态能力声明。
- `wire.go`：Web Search 与 LLM Context 请求、响应及错误负载定义。
- `search.go`：双端点自动选路、结果归一化及 `age` 时间字段解析。
- `query.go`：语言码归一化、时间窗转换与超长站点列表内联 Goggles 规则构造。
- `errors.go`：HTTP 状态码（含 `422`）与错误负载到 `core.Code` 的映射。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **查询上限与 Goggles 回退**：拼装 `site:` 超出 400 字符或 50 词上限时自动切换内联 `goggles` 规则，避免触发 `422`。
- **双端点能力与边界**：`web` 模式或 `snippet` 请求动态收紧上限至 20 条且不声明正文能力；解析 `age` 数组前必须校验元素数量。

## 验证

```bash
go test -race ./internal/provider/brave/...
```
