# 语言探测与多语言词表 (`clikit/i18n`)

提供三端零 CGO 系统语言探测，以及基于 `fs.FS` 的多语言词表加载、单遍插值与对称校验。

## 目录结构

- `doc.go`：包级说明与依赖边界。
- `locale.go`：语言标识定义与跨平台语言优先级解析。
- `catalog.go`：多语言词表预加载与按需惰性解码、`{placeholder}` 插值、结构化错误格式化与对称性校验。
- `system_darwin.go`：macOS 系统语言探测实现。
- `system_linux.go`：Linux 系统语言探测实现。
- `system_windows.go`：Windows 系统语言探测实现。
- `system_other.go`：其他平台回退实现。

## 开发规范

- **词表外置**：本包不内嵌具体业务文案，调用方经由 `fs.FS` 注入词表（支持全量加载或按语种惰性解码），并在测试中校验多语种键与占位符对称性。
- **单遍替换**：占位符插值必须单遍替换，防止参数值二次展开。

## 验证

```bash
go test -race ./clikit/i18n/...
```
