// Package zhihu 实现知乎开放平台全网搜索 (/api/v1/content/global_search) 与站内搜索 (/api/v1/content/zhihu_search) 适配器。
//
// 依赖边界：仅依赖标准库、internal/{core,httpx,keypool,spec,textnorm,urlnorm}，不导入其他 provider 子包。
package zhihu
