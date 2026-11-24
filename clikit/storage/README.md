# 跨平台配置/缓存目录与原子文件存储 (`clikit/storage`)

统一封装 macOS、Windows 与 Linux 三端 `~/.config/<app>/` 配置目录与 `~/.cache/<app>/` 缓存目录解析、`~` 主目录展开、崩溃安全原子写文件与敏感文件权限审计。

## 目录结构

- `doc.go`：包职责与纯标准库边界声明。
- `storage.go`：`ExpandHome`、`ResolveConfigFile`、`ResolveConfigDir`、`ResolveCacheDir`、`WriteFileAtomic` 与 `HasBroadPermissions` 实现。

## 开发规范

- **三端统一路径**：未显式传入路径或环境变量时，三端均统一落位 `~/.config/<app>/` 与 `~/.cache/<app>/`，避免跨平台文档与运维心智割裂。
- **原子落盘**：配置文件、密钥库与安装资产统一通过同目录临时文件 `Sync` + `Rename` 原子写入。

## 验证

```bash
go test -race ./clikit/storage/...
```
