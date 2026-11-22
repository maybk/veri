# 核心领域模型与契约 (`internal/core`)

定义联网检索与网页提取的请求/响应领域实体、统一输出信封、正交适配器接口、整数微单位成本、日历时间窗与标准错误码。

## 目录结构

- `doc.go`：包级说明与依赖边界。
- `model.go`：检索与提取请求、结果与页面领域结构、枚举解析及深拷贝。
- `sanitize.go`：目标与终态 URL 安全校验、URL 兜底标题推导、结果详略粒度对齐与同源结果清洗。
- `envelope.go`：顶层统一输出信封 `Envelope`、执行元数据 `Meta` 与 Provider 执行报告。
- `provider.go`：`Searcher`、`DynamicSearcher`、`Fetcher`、`SpecialistFetcher`、`FetchProfiler`、`Answerer` 接口与能力声明 `Capabilities`。
- `cost.go`：整数微单位成本 `Cost` 核算、可选单价解析与多币种饱和汇总。
- `freshness.go`：相对时间窗与绝对日期区间的日历解析及月底钳制。
- `errors.go`：13 个稳定机器错误码、结构化错误 `Error`、回退判定与进程退出码映射。

## 开发规范

- **纯净领域边界**：仅依赖 Go 标准库以及底层静态规格包 `internal/spec` 与文本归一化包 `internal/textnorm`，严禁导入第三方库或上层业务模块。

## 验证

```bash
go test -race ./internal/core/...
```
