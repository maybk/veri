# 多源路由、融合与预算裁剪 (`internal/router`)

实现检索与网页提取的选路编排、故障自动回退、多源并发 RRF 融合、过滤条件降级模拟与响应字符预算截断。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：路由层融合召回下限、慢源宽限期、软降权熔断与大纲/段落切片内部默认阈值。
- `router.go`：路由器配置与实例装配。
- `errors.go`：多源链式失败报告与确定性主错误码汇总。
- `search.go`：单源指定、语种分链自动回退（`auto` 含同稿指纹折叠）、独立凭据组空结果短路、请求级超时控制与成本汇总。
- `search_plan.go`：`auto` 健康/作用域/原生能力统一排序，以及自动 `--fusion` 池的独立凭据组去重、全网保底与非中文垂直源抑制。
- `fusion.go`：多源并发检索调度（含独立凭据组慢源法定数）与部分超时（`partial`）容错汇聚。
- `dedup.go`：两阶段同稿指纹聚簇、代表页择优（卫生门禁与独立凭据组 URL 共识）及字段合并。
- `rrf.go`：URL 与内容指纹聚簇、同凭据组最大池化防双票放大与加权 RRF 融合排序。
- `fetch.go`：网页提取主入口、请求单点规范化校验、本地/内网隔离与信封构建。
- `fetch_plan.go`：`api_first`（默认）、`smart_local`、`local_only`、`api_only` 提取序列编排与 PDF / 动态渲染重排。
- `fetch_exec.go`：提取序列执行、404 独立确认短路、挑战盾升档与低质量优雅保底。
- `health.go`：进程内瞬态故障与配额/鉴权失效软降权熔断器。
- `page.go`：围栏代码块安全的 Unicode 码点流式分页续读（`SlicePage`）。
- `outline.go`：Markdown 章节大纲（`Outline`）提取、通用网页装饰性标题锚点净化、层级裁剪重算与章节子树（`Section`）视图管线。
- `match.go`：页内关键词匹配窗口（`Match`）及超大单围栏代码块行窗口二次切片。
- `capability.go`：静态与按请求动态能力判定、域名作用域（`ScopeDomains`）匹配、`--strict` 严格筛选及本地后过滤模拟。
- `budget.go`：面向 Agent 上下文窗口的四级渐进字符预算裁剪。

## 开发规范

- **纯净依赖**：仅允许导入标准库、`golang.org/x/sync/errgroup`、`internal/core`、`internal/mdscan`、`internal/spec`、`internal/textnorm` 与 `internal/urlnorm`，不直接感知具体厂商 HTTP 细节或终端语言。
- **优雅降级**：融合检索至少一路成功即返回有效结果；网页提取在上游 API 全部失败时保留有效的本地提取文本并写入 `notes`。

## 验证

```bash
go test -race ./internal/router/...
```
