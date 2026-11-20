# 语言探测与多语言词表 (`clikit/i18n`)

提供三端零 CGO 系统语言探测，以及基于 `fs.FS` 的多语言词表加载、单遍插值与对称校验。

## 目录结构

- `doc.go`：包级说明与依赖边界。
- `locale.go`：`Locale` 定义与跨平台语言优先级解析。
- `catalog.go`：`Bundle` / `Catalog` 加载、`{placeholder}` 插值、结构化错误格式化与 `ValidateSymmetry` 校验。
- `system_darwin.go`：macOS 读取 `AppleLanguages` 偏好。
- `system_linux.go`：Linux 解析 `/etc/locale.conf`。
- `system_windows.go`：Windows 调用 `GetUserPreferredUILanguages`。
- `system_other.go`：其他平台回退实现。

## 开发规范

- **词表外置**：本包不内嵌具体业务文案，调用方通过 `LoadBundle` 注入词表并通过 `ValidateSymmetry` 校验对称性。
- **单遍替换**：插值统一使用 `strings.NewReplacer` 单遍替换，防止参数值二次展开。

## 验证

```bash
go test -race ./clikit/i18n/...
```
