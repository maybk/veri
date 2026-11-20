// Package firecrawl 实现 Firecrawl v2 Scrape (/v2/scrape) 与 Search (/v2/search) 适配器，支持官方云端与开源自托管实例。
//
// 依赖边界：仅依赖标准库、internal/{core,httpx,keypool,mdscan,spec,textnorm,urlnorm}，不导入其他 provider 子包。
package firecrawl
