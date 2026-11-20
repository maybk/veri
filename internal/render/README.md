# 输出格式化与内容隔离 (`internal/render`)

将领域信封（`core.Envelope`）渲染为终端 Markdown、JSON、JSONL、URL 列表或带隔离标签的 MCP 文本。

## 目录结构

- `doc.go`：包级说明与依赖边界。
- `display.go`：空标题回退策略、Provider 标签保序去重与大纲单行预览格式化。
- `cli_format.go`：CLI 端 `markdown`、`json`、`jsonl`、`urls` 格式预校验与渲染。
- `mcp_format.go`：MCP 工具英文 Markdown / JSON 文本渲染与错误提示拼装。
- `untrusted.go`：`<untrusted_web_content>` 边界包裹与闭合标签清理。
- `color.go`：终端 TTY 检测与 ANSI 着色控制。
- `testdata/`：CLI 与 MCP 渲染黄金输出测试样本目录。

## 开发规范

- **机器契约稳定**：严禁依赖人类界面本地化词表；JSON 与 MCP 文本输出保持确定性英文格式。

## 验证

```bash
go test -race ./internal/render/...
```
