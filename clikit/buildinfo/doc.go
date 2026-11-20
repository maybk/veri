// Package buildinfo 解析内嵌 build/config.yml 应用元信息，并合并链接期 -ldflags 与 Go VCS 构建元数据。
//
// 依赖边界：仅依赖 Go 标准库。
package buildinfo
