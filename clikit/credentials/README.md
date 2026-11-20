# 本地主密钥与加密凭据库 (`clikit/credentials`)

管理本机主密钥（`secret.key`）与版本化加密凭据库（`secrets.json`）。

## 目录结构

- `doc.go`：包级说明与依赖边界。
- `sealer.go`：本机主密钥原子生成、缓存与 AES-256-GCM 加解密。
- `vault.go`：`secrets.json` 加密键值读写、删除与列表枚举。
- `key_protection_windows.go`：Windows 平台 DPAPI 主密钥密封实现。
- `key_protection_other.go`：非 Windows 平台主密钥权限保护实现。

## 开发规范

- **防竞态创建**：首次生成 `secret.key` 使用 `O_CREATE|O_EXCL`，避免并发启动覆盖主密钥。
- **领域解耦**：仅管理归一化的键值字符串，业务级名称校验由上层调用方负责。

## 验证

```bash
go test -race ./clikit/credentials/...
```
