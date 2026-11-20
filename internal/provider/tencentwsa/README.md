# 腾讯云联网搜索适配器 (`internal/provider/tencentwsa`)

对接腾讯云联网搜索 SearchPro REST 接口，实现 `core.Searcher`。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认端点、`edition` 版本映射常量与默认超时参数。
- `provider.go`：适配器构造、`edition` 参数校验与原生过滤能力声明。
- `wire.go`：外层 `Response` 包裹结构、内层二次序列化 `Pages[]` JSON 字符串结构及业务错误结构定义。
- `search.go`：请求条数分档映射（`10/20/30/40/50`）、站点包含/排除拼装、双层 JSON 解码与东八区时间归一。
- `errors.go`：HTTP 状态码与 `Response.Error.Code` 错误族前缀及枚举到 `core.Code` 的映射。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **双层 JSON 解码**：`Response.Pages` 的每个元素均为独立 JSON 字符串，必须逐条二次反序列化并做好异常保护。
- **业务错误优先识别**：上游常以 HTTP `200` 携带 `Response.Error` 返回未开通或鉴权错误，须在解析结果前优先拦截。

## 验证

```bash
go test -race ./internal/provider/tencentwsa/...
```
