// Package linkup 实现 Linkup Search (/v1/search) 与 Fetch (/v1/fetch) 适配器。
//
// 依赖边界：仅依赖标准库、internal/{core,httpx,keypool,mdscan,spec,textnorm,urlnorm}，不导入其他 provider 子包。
package linkup
