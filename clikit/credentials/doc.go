// Package credentials 提供基于 AES-256-GCM 与平台原生密钥保护的本地凭据密封（secret.key）与版本化加密凭据库（secrets.json）。
//
// 依赖边界：仅依赖 Go 标准库、clikit/storage 以及 Windows 平台下的 golang.org/x/sys/windows。
package credentials
