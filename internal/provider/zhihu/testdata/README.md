# 知乎开放平台测试样本 (`internal/provider/zhihu/testdata`)

存放知乎开放平台 `global_search` 与 `zhihu_search` 离线契约测试使用的脱敏 JSON 响应样本。

## 目录结构

- `global_search_ok.json`：全网搜索成功响应脱敏样本（含 `<em>` 高亮标签、UTM 追踪参数与字符串权威等级）。
- `zhihu_search_ok.json`：知乎站内搜索成功响应脱敏样本（含 `RankingScore`、`VoteUpCount`、`CommentCount` 与 `SearchHashId`）。
- `err_code_30002.json`：HTTP 200 内嵌 `Code=30002`（配额耗尽）业务错误样本。
- `err_code_30003.json`：HTTP 200 内嵌 `Code=30003`（风控拒绝）业务错误样本。

## 维护规范

- **标准 JSON 格式**：所有 `.json` 样本必须为合法的 RFC 8259 JSON，禁止在文件内写入注释；样本来源与用途统一在本 `README.md` 中记录。
- **彻底脱敏**：禁止包含真实 Access Secret 或账户私有信息。
