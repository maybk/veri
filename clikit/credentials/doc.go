// Package credentials 提供跨平台主密钥（secret.key）、三端文件权限保护与 AES-256-GCM 加密凭据库（secrets.json）。
//
// 依赖边界：仅依赖 Go 标准库、clikit/storage 以及 Windows 平台下的 golang.org/x/sys/windows。
package credentials
