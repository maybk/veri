# 本地主密钥与加密凭据库 (`clikit/credentials`)

管理跨平台 256 位主密钥（`secret.key`）与 AES-256-GCM 加密凭据库（`secrets.json`），文件权限与防符号链接读写委托 `clikit/storage`。

## 目录结构

- `doc.go`：包级说明与依赖边界。
- `sealer.go`：主密钥原子生成、BOM/CRLF 容错读取、敏感字节归零、缓存校验与 AES-256-GCM 加解密。
- `vault.go`：`secrets.json` 加密键值原子读写、防混钥保护、删除与枚举。

## 开发规范

- **三端通用**：macOS、Windows 与 Linux 统一存储标准 Base64 编码的 32 字节随机主密钥，成对复制 `secrets.json` 与 `secret.key` 即可跨端通用。
- **防竞态与零脏写**：生成 `secret.key` 与更新 `secrets.json` 均先收紧临时文件权限再原子发布，严格拒绝符号链接与非常规文件；只读解密路径绝不新建或删除主密钥文件。
- **领域解耦**：仅管理归一化的键值字符串，业务级名称校验由上层调用方负责。

## 验证

```bash
go test -race ./clikit/credentials/...
```
