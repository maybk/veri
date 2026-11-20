# 知乎开放平台检索适配器 (`internal/provider/zhihu`)

对接知乎开放平台全网搜索（`GET /api/v1/content/global_search`）与知乎站内搜索（`GET /api/v1/content/zhihu_search`），实现 `core.Searcher` 与 `core.DynamicSearcher`。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认端点、作用域、索引库、条数上限与请求头常量。
- `provider.go`：适配器构造、作用域/索引库校验与 `CapabilitiesFor` 动态能力声明。
- `wire.go`：响应信封结构体及权威等级多态解析。
- `search.go`：全网/站内参数组装、时间过滤映射、`<em>` 高亮剥离、UTM 参数清洗与结果归一化。
- `errors.go`：HTTP 200 内嵌业务错误码（`Code != 0`）与标准状态码映射。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **精确请求头**：每次请求携带 `Authorization: Bearer`、`X-Request-Timestamp`（秒级 Unix 时间戳）与 `Content-Type: application/json`。
- **站点过滤边界**：官方 `global_search` 的 `host==` 不支持子域匹配且禁用 `zhihu.com`，统一声明 `Sites: SiteNone` 交由路由层精确后过滤；站内专搜通过 `-o zhihu.scope=zhihu` 显式切换。
- **风控不重试**：`Code=30003` 映射为不可重试的 `blocked_by_policy`，`Code=30002` 映射为 `insufficient_balance` 触发自动回退。

## 验证

```bash
go test -race ./internal/provider/zhihu/...
```
