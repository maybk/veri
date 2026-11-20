// Package router 实现检索与提取路由核心：能力匹配降级、链式回退、中英文分链选路、多源并发 RRF 融合、本地提取质量闸门升档与响应预算截断。
//
// 依赖边界：仅依赖标准库、internal/{core,urlnorm} 与 golang.org/x/sync/errgroup。
package router
