# 应用装配、缓存与健康诊断 (`internal/app`)

负责显式装配已启用的检索/提取适配器、构建安全网络客户端、管理 CLI 磁盘缓存与 MCP 进程内内存缓存及并发合并，以及执行 `doctor` 诊断。

## 目录结构

- `doc.go`：包职责说明。
- `app.go`：应用生命周期容器（`App`）、客户端与路由装配、带磁盘缓存的 CLI `Search` / `Fetch` 入口、缓存管理及调用日志。
- `mcp.go`：MCP 专属路由按需懒加载、进程内内存缓存检索/抓取（`SearchMCP` / `FetchMCP`）与动态分页视图构造。
- `scrub.go`：应用层文本、错误对象及响应信封全字段凭据脱敏。
- `batch.go`：有界并发保序批量网页抓取（`FetchBatch` / `FetchBatchMCP`）与单页局部错误隔离。
- `registry.go`：适配器工厂表、运行期状态模型（`ProviderStatus`）与公共状态构造辅助函数。
- `registry_builders.go`：14 个内置检索与提取适配器的显式构造函数实现。
- `cache.go`：并发安全的双上限（条数与字符总量）泛型 TTL-LRU 内存缓存、统一缓存键/可缓存性判定与 `flightGroup` 并发合并器。
- `disk_cache.go`：纯 Go gzip 原子本地磁盘缓存、LRU 惰性淘汰、损坏自愈及 `InspectCache` / `ClearDiskCache` 统计与安全清理。
- `doctor.go`：配置文件权限检查、可用链路统计、DNS / Fake-IP 环境探测及可选 `--live` 真实连通性巡检。

## 开发规范

- **禁用隐式 `init()`**：所有适配器必须在 `registry.go` 中通过显式构造函数注册，保证测试隔离与依赖顺序透明。
- **缓存计费与耗时归零**：命中内存或磁盘缓存的响应必须将 `Meta.Cached` 置为 `true`、`Meta.DurationMS` 与 `Meta.Providers[].LatencyMS` 置为 `0`，并清空 `Meta.Cost` 与 `Meta.Providers[].Cost`，避免客户端误计重复开销或展示历史耗时。

## 验证

```bash
go test -race ./internal/app/...
```
