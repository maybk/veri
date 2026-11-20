// Package local 实现基于纯 Go（go-readability/v2 + internal/htmlmd）与正文质量闸门的通用本地网页提取器。
//
// 依赖边界：仅依赖标准库、internal/{core,htmlmd,httpx}、go-readability/v2 与 golang.org/x/net/html（含 charset）。
package local
