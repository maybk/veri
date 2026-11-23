# 知乎开放平台检索适配器 (`internal/provider/zhihu`)

单包双实例对接知乎开放平台站内搜索（`zhihu`：`GET /api/v1/content/zhihu_search`）与全网搜索（`zhihu_web`：`GET /api/v1/content/global_search`），实现 `core.Searcher` 与 `core.DynamicSearcher`。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认端点、索引库、条数上限、作用域域名与请求头常量。
- `provider.go`：站内与全网双构造器（`New` / `NewWeb`）、索引库校验及静态/动态能力边界声明。
- `wire.go`：响应信封结构体及权威等级多态解析。
- `search.go`：全网/站内参数组装、时间过滤映射、`<em>` 高亮剥离、UTM 参数清洗与结果归一化。
- `errors.go`：HTTP 200 内嵌业务错误码（`Code != 0`）与标准状态码按实例归因映射。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **精确请求头**：每次请求携带 `Authorization: Bearer`、`X-Request-Timestamp`（秒级 Unix 时间戳）与 `Content-Type: application/json`。
- **站点过滤边界**：`zhihu` 声明 `ScopeDomains: ["zhihu.com"]`（天然限定知乎站内）；`zhihu_web` 因官方 `global_search` 的 `host==` 不支持子域匹配，声明 `Sites: SiteNone` 交由路由层后过滤。
- **风控不重试**：`Code=30003` 映射为不可重试的 `blocked_by_policy`，`Code=30002` 映射为 `insufficient_balance` 触发自动回退与独立冷却。

## 验证

```bash
go test -race ./internal/provider/zhihu/...
```
