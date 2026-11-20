# 应用装配、缓存与健康诊断 (`internal/app`)

负责显式装配已启用的检索/提取适配器、构建安全网络客户端、管理 MCP 进程内缓存与并发合并，以及执行 `doctor` 诊断。

## 目录结构

- `doc.go`：包职责说明。
- `app.go`：应用生命周期容器（`App`）、客户端与路由装配、CLI `Search` / `Fetch` 入口及调用日志。
- `mcp.go`：MCP 专属路由按需懒加载、进程内缓存检索/抓取（`SearchMCP` / `FetchMCP`）与动态分页视图构造。
- `scrub.go`：应用层文本、错误对象及响应信封全字段凭据脱敏。
- `batch.go`：有界并发保序批量网页抓取（`FetchBatch` / `FetchBatchMCP`）与单页局部错误隔离。
- `registry.go`：显式构造 8 个内置适配器并生成运行期状态列表（`ProviderStatus`）。
- `cache.go`：并发安全的双上限（条数与字符总量）泛型 TTL-LRU 内存缓存及带引用计数取消与异常恢复的 `flightGroup` 并发合并器。
- `doctor.go`：配置文件权限检查、可用链路统计、DNS / Fake-IP 环境探测及可选 `--live` 真实连通性巡检。

## 开发规范

- **禁用隐式 `init()`**：所有适配器必须在 `registry.go` 中通过显式构造函数注册，保证测试隔离与依赖顺序透明。
- **缓存计费归零**：命中进程内缓存的响应必须将 `Meta.Cached` 置为 `true` 并清空 `Meta.Cost`，避免客户端误计重复开销。

## 验证

```bash
go test -race ./internal/app/...
```
