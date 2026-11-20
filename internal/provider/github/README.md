# GitHub 结构化提取器 (`internal/provider/github`)

基于 GitHub REST API 将仓库主页、源码文件（`blob`）、Issue、Pull Request、Discussion、Commit 与 Release 页面转换为结构化 Markdown，实现 `core.SpecialistFetcher`。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认 API / Raw 端点、超时、并发上限、单文件体积上限与保留顶层路径表。
- `provider.go`：提取器构造、URL 形态识别与路径穿越守卫（`Match`）及可选 `GITHUB_TOKEN` 池注入。
- `wire.go`：README、Issue、Comment、Pull Request、Discussion、Commit 与 Release 响应结构定义。
- `fetch.go`：按 URL 路由分发对应 REST API 请求与多令牌故障切换。
- `render.go`：Issue、PR（含行级代码审查评论）、Discussion 与 Commit 结构化 Markdown 组装。
- `errors.go`：GitHub `401` / `403`（区分配额耗尽 `X-RateLimit-Remaining: 0`） / `404` 到 `core.Code` 的映射。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **零密钥可用**：未配置 `GITHUB_TOKEN` 时以匿名模式工作，仅在配置了有效令牌时附加 `Authorization` 头。
- **动态 User-Agent**：统一通过 `spec.UserAgent(version)` 发送版本化标识以满足 GitHub API 要求。

## 验证

```bash
go test -race ./internal/provider/github/...
```
