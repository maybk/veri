// Package app 负责装配配置、显式注册内置 Provider、构建检索与提取路由、管理 CLI 磁盘缓存与 MCP 进程内内存缓存及并发合并，并提供环境诊断。
//
// 依赖边界：允许导入 clikit/{buildinfo,redact,storage}、internal/{core,config,router,httpx,urlnorm,textnorm,spec,provider/*} 与 golang.org/x/sync/errgroup；禁止导入 cli、mcpserver、i18n。
package app
