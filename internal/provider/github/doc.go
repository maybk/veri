// Package github 实现针对 github.com 仓库 README、代码文件、Issue、PR 与 Release 的结构化专用提取器。
//
// 依赖边界：仅依赖标准库与 internal/{core,httpx,keypool}；严禁调用外部 gh 命令行工具。
package github
