// Package tinyfish 实现 TinySearch (GET api.search.tinyfish.ai) 与 TinyFetch (POST api.fetch.tinyfish.ai) 适配器。
//
// 依赖边界：仅依赖标准库与 internal/{core,httpx,keypool,urlnorm}；禁止导入 router、app、cli、mcpserver 或其他厂商包。
package tinyfish
