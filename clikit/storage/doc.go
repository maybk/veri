// Package storage 提供三端（macOS / Windows / Linux）统一的用户配置与缓存目录解析、
// 崩溃安全原子写入、私有文件 ACL 收紧与宽权限审计，以及防符号链接与 TOCTOU 的常规文件有界读取。
//
// 依赖边界：仅依赖 Go 标准库，以及 Windows 平台下的 golang.org/x/sys/windows。
package storage
