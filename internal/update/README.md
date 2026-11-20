# 自更新引擎 (`internal/update`)

提供官方 GitHub Releases 版本检查、完整性校验、安装形态识别与跨平台原子自更新。

## 目录结构

- `doc.go`：包级说明与职责边界。
- `defaults.go`：更新阶段标识、官方端点、下载与解压上限及超时常量。
- `version.go`：官方 URL 白名单、镜像前缀校验、版本号比较与摘要解析。
- `install_method.go`：安装形态识别（Standalone / Homebrew / Scoop / WinGet / System / Dev）。
- `options.go`：更新器可选依赖注入项。
- `checker.go`：官方 Release 检查（API 优先，限流或故障时降级 302 重定向）与 `SHA256SUMS.txt` 拉取。
- `archive.go`：归档根目录单文件解压、目录可写预检、同目录原子替换回滚与旧备份清理。
- `updater.go`：自更新主流程编排、安装来源守卫、流式下载校验与进度回调。

## 开发规范

- **可信源单向校验**：版本元数据与摘要始终仅从官方 GitHub 获取；镜像仅用于加速下载，校验失败立即中止。
- **网络与边界**：所有出站请求经由 `internal/httpx` 执行，严禁使用 `http.DefaultClient`。
- **包管理器保护**：检测到由 Homebrew、Scoop、WinGet 或系统目录托管时，拒绝覆写并提示对应升级命令。

## 验证

```bash
go test -race ./internal/update/...
```
