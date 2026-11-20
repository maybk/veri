# 构建与版本信息 (`clikit/buildinfo`)

从静态资产 `fs.FS` 解析 `build/config.yml`，并合并 `-ldflags` 与 Go `runtime/debug` VCS 构建信息。

## 目录结构

- `doc.go`：包级说明与依赖边界。
- `buildinfo.go`：`AppMeta` / `Info` 结构、`ParseConfig`、`LoadConfig`、`Resolve` 与 `FormatLine` 实现。

## 开发规范

- **零外部依赖**：仅使用 Go 标准库解析 `build/config.yml`，不引入第三方 YAML 库。
- **优先级明确**：链接期显式注入的 `ldVersion` 优先，未注入时读取内嵌 `build/config.yml` 中的 `info.version`。

## 验证

```bash
go test -race ./clikit/buildinfo/...
```
