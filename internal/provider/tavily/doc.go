// Package tavily 实现 Tavily Search (/search) 与 Extract (/extract) 适配器。
//
// 依赖边界：仅依赖标准库与 internal/{core,httpx,keypool,urlnorm}；禁止导入 router、app、cli、mcpserver 或其他厂商包。
package tavily
