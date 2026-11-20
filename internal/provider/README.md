# 检索与提取适配器集合 (`internal/provider`)

汇总所有上游联网检索 API、GitHub REST API 与本地纯 Go 网页正文提取器的正交适配器实现及契约测试辅助包。

## 目录结构

- `deepseek/`：DeepSeek Search & Fetch 双能力适配器。
- `keenable/`：Keenable Search & Fetch 双能力适配器。
- `zhihu/`：知乎开放平台全网搜索与站内搜索适配器。
- `exa/`：Exa Search & Contents 适配器。
- `tinyfish/`：TinySearch & TinyFetch 适配器。
- `tencentwsa/`：腾讯云联网搜索 SearchPro 适配器。
- `tavily/`：Tavily Search & Extract 适配器。
- `brave/`：Brave Web Search & LLM Context 适配器。
- `linkup/`：Linkup Search & Fetch 双能力适配器。
- `firecrawl/`：Firecrawl v2 Scrape & Search 适配器（支持官方云端与开源自托管）。
- `github/`：GitHub 仓库、源码文件、Issue、PR、Discussion、Commit 与 Release 结构化提取器。
- `local/`：纯 Go 本地网页抓取、字符集转码、Readability 正文提取与质量闸门。
- `providertest/`：跨适配器共享的离线契约不变量断言与手动 `live` 联调辅助工具。

## 开发规范

- **正交与互不导入**：各适配器子包互不导入，仅允许依赖 `internal/core`、`internal/mdscan`、`internal/spec`、`internal/textnorm`、`internal/httpx`、`internal/keypool` 与 `internal/urlnorm`；`local` 额外封装 HTML 正文提取库。
- **固定布局与标准样本**：每个适配器目录维护固定源码结构、`README.md` 与 `testdata/`；`.json` 样本必须符合 RFC 8259 且禁止包含注释，样本说明记录在对应 `testdata/README.md`。

## 验证

```bash
go test -race ./internal/provider/...
```
