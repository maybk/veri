// Package parallel 实现 Parallel 官方正式 REST API（POST /v1/search 与 POST /v1/extract）适配器。
//
// 依赖边界：仅依赖标准库、internal/{core,httpx,keypool,mdscan,spec,textnorm,urlnorm}，不引用其他 provider 包；
// 强制要求配置 PARALLEL_API_KEY 启用，不接入匿名托管 MCP 通道。
package parallel
