// Package brave 实现 Brave Search LLM Context (/res/v1/llm/context) 与 Web Search (/res/v1/web/search) 适配器。
//
// 依赖边界：仅依赖标准库与 internal/{core,httpx,keypool,urlnorm}；禁止导入 router、app、cli、mcpserver 或其他厂商包。
package brave
