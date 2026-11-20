# MCP 服务端 (`internal/mcpserver`)

基于官方 `modelcontextprotocol/go-sdk` 实现标准 stdio 传输的 MCP 服务端，提供 `web_search` 与 `web_fetch` 工具。

## 目录结构

- `doc.go`：包级说明与依赖边界。
- `prompts.go`：MCP 服务端系统指引与标准工具英文描述提示词常量。
- `schema.go`：工具输入结构体、字段边界常量与输入/输出 JSON Schema 定义。
- `server.go`：MCP 服务端构造、stdio 取消信号即时中断桥接、工具注册与结构化响应封装。

## 开发规范

- **标准输出专用于协议帧**：`stdio` 模式下 `stdout` 仅输出 JSON-RPC 帧，诊断日志一律写入 `stderr`。
- **英文契约稳定**：工具描述、字段名、错误码与操作提示固定使用英文。

## 验证

```bash
go test -race ./internal/mcpserver/...
```
