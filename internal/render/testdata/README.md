# `internal/render/testdata`

存放 `internal/render` 黄金输出样本（`*.golden`），用于校验 CLI Markdown、批量抓取与 MCP 文本渲染契约不发生意外漂移。可通过 `go test ./internal/render -update` 在有意调整版式后刷新样本。

## 目录结构

- `cli_search_md.golden`：CLI 检索 Markdown 渲染样本（含空标题回退、作者、日期与成本）。
- `cli_fetch_batch_md.golden`：CLI 批量网页提取 Markdown 渲染样本（含部分失败与空标题回退）。
- `mcp_search_md.golden`：MCP `web_search` 文本渲染样本（含不可信边界标签与空标题回退）。
- `mcp_fetch_batch_md.golden`：MCP `web_fetch` 批量文本渲染样本（含去重 Provider 头部标签与错误项）。
