# 跨平台目录与安全文件存储 (`clikit/storage`)

封装 macOS、Windows 与 Linux 三端 `~/.config/<app>/`、`~/.cache/<app>/` 路径解析、原子写入、私有文件权限保护，以及防符号链接与 TOCTOU 的常规文件有界读取。

## 目录结构

- `doc.go`：包职责与平台依赖边界。
- `storage.go`：三端配置/缓存目录解析与 `~` 主目录展开。
- `atomic.go`：UTF-8 BOM 剥离、支持重命名前钩子的原子落盘与私有文件写入。
- `regular.go`：跨平台无跟随常规文件打开与有界读取入口。
- `regular_unix.go`：类 Unix 无跟随、非阻塞常规文件打开。
- `regular_windows.go`：Windows 无跟随重解析点常规文件打开。
- `regular_other.go`：其他平台常规文件身份复核打开兜底。
- `acl_windows.go`：Windows 句柄级 NTFS Protected DACL 收紧、宽权限检查与只读属性清理。
- `acl_other.go`：类 Unix 句柄级 `0600` 权限收紧与宽权限检查。

## 开发规范

- **三端统一路径**：未显式指定路径或环境变量时，三端统一落位 `~/.config/<app>/` 与 `~/.cache/<app>/`。
- **句柄级防 TOCTOU**：敏感文件读取、权限收紧与宽权限检查均基于不跟随符号链接与重解析点的句柄完成，避免路径替换竞态。
- **原子落盘**：通过同目录临时文件刷盘后原子替换写入，私有文件在重命名前完成权限收紧。

## 验证

```bash
go test -race ./clikit/storage/...
```
