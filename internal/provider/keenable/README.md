# Keenable 检索与提取适配器 (`internal/provider/keenable`)

对接 Keenable `POST /v1/search` 与 `GET /v1/fetch` 接口，同时实现 `core.Searcher` 与 `core.Fetcher`。

## 目录结构

- `doc.go`：包职责说明。
- `defaults.go`：默认端点、模式、条数、字符预算与超时常量。
- `provider.go`：适配器构造、模式校验、能力声明与 `usage.paid` 精确零成本记账。
- `wire.go`：请求/响应结构体、错误体及 ISO 8601 / Unix 秒时间戳解析。
- `search.go`：检索过滤组装（固定 `disable_cards=true`）、按粒度设定摘要上限与结果清洗。
- `fetch.go`：`GET /v1/fetch` 查询参数组装、`--fresh` 实时抓取透传与页面正文归一化。
- `errors.go`：按扁平错误 `type` 精准分类，严格拆分 HTTP 403 鉴权失败与目标站拦截。
- `testdata/`：脱敏的上游响应测试样本目录。

## 开发规范

- **云端必须配 Key**：缺 Key 时静默禁用，绝不回退 `/public` 共享匿名池。
- **403 精细隔离**：`permission_error` 中仅凭证/权限类错误归 `auth_failed` 隔离密钥，`private_url_not_allowed`、特性未开通及 `target_error` 绝不拉黑健康密钥。
- **隐私提示**：Keenable 官方默认数据策略会留存查询与结果，敏感查询应禁用本源。

## 验证

```bash
go test -race ./internal/provider/keenable/...
```
