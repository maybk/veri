// Package deepseek 实现 DeepSeek Search (/v1/tools/search) 与 Fetch (/v1/tools/fetch) 适配器。
//
// 依赖边界：仅依赖标准库与 internal/{core,httpx,keypool,urlnorm}；禁止导入 router、app、cli、mcpserver 或其他厂商包。
package deepseek
