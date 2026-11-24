# 检索与提取源说明

> 寻找各检索源的免费额度、参考价格、API Key 申请入口与日常用法，请先看 [使用指南](./usage.md)。

`veri` 采用正交适配器架构：检索源实现 `core.Searcher`，网页提取源实现 `core.Fetcher`。启动时按 `providers.<name>.enabled` 与已配置的凭据启用对应适配器，未启用或未配置密钥的适配器静默跳过。

## 内置适配器矩阵

| 标识 (`name`) | 角色 | 默认端点 | 凭据环境变量 | 主要技术特性 |
| --- | --- | --- | --- | --- |
| `deepseek` | `search`, `fetch` | `https://api.deepseek.com` | `DEEPSEEK_API_KEY` | 提供 `/v1/tools/search` 与 `/v1/tools/fetch`；检索支持 `site:`、`-site:`、`after:`、`before:` 操作符拼装，查询总长上限 128 个 Unicode 字符；提取默认超时 `10s` |
| `keenable` | `search`, `fetch` | `https://api.keenable.ai` | `KEENABLE_API_KEY` | 提供 `/v1/search`（默认关闭合成卡片）与 `GET /v1/fetch`（支持 `--fresh`）；支持域名过滤与发布时间窗；默认数据策略会留存查询与结果 |
| `zhihu` | `search` | `https://developer.zhihu.com` | `ZHIHU_API_KEY`<br>`ZHIHU_ACCESS_SECRET` | 知乎站内问答与专栏搜索（`zhihu_search`，单次最多 10 条，仅限 `zhihu.com`）；支持赞同/评论/时间排序，`-s zhihu.com` 自动优先，非知乎站点过滤自动跳过 |
| `zhihu_web` | `search` | `https://developer.zhihu.com` | 共享 `ZHIHU_API_KEY`<br>`ZHIHU_ACCESS_SECRET` | 知乎开放平台中文全网搜索（`global_search`，单次最多 20 条）；支持索引库切换、时间过滤与高亮/UTM 清洗，与 `zhihu` 共用密钥且配额独立 |
| `exa` | `search`, `fetch` | `https://api.exa.ai` | `EXA_API_KEY` | 提供 `/search` 混合检索与 `/contents` 正文/PDF 提取；支持域名过滤、ISO 日期区间与内容类别筛选，响应返回 `costDollars` 实收账单 |
| `tinyfish` | `search`, `fetch` | `https://api.search.tinyfish.ai`<br>`https://api.fetch.tinyfish.ai` | `TINYFISH_API_KEY` | 检索与抓取双端点；检索支持分钟级相对时效、日期区间与学术论文模式，抓取支持服务端 SPA / JS 动态渲染与 PDF |
| `tencent_wsa` | `search` | `https://api.wsa.cloud.tencent.com` | `TENCENT_WSA_API_KEY`<br>`TENCENTCLOUD_WSA_APIKEY` | 腾讯云联网搜索 SearchPro 接口；支持 `lite`、`standard`、`flagship` 版本切换及行业分类筛选 |
| `tavily` | `search`, `fetch` | `https://api.tavily.com` | `TAVILY_API_KEY` | 提供 `/search` 与 `/extract`（支持动态渲染与 PDF）；支持四档检索深度、域名/时间/语言过滤及 `usage.credits` 点数核算 |
| `brave` | `search` | `https://api.search.brave.com` | `BRAVE_API_KEY`<br>`BRAVE_SEARCH_API_KEY` | `snippet` 调用 `/res/v1/web/search`，`summary` / `content` 调用 `/res/v1/llm/context`；超长站点列表自动切换内联 Goggles 规则 |
| `linkup` | `search`, `fetch` | `https://api.linkup.so` | `LINKUP_API_KEY` | 提供 `/v1/search`（固定结构化结果、默认 `fast` 深度）与 `/v1/fetch`（支持 HTML/PDF 与按需 JS 渲染升档）；支持域名白/黑名单与日期范围过滤 |
| `parallel` | `search`, `fetch` | `https://api.parallel.ai` | `PARALLEL_API_KEY` | 提供 `/v1/search`（默认 `fast` 模式，支持域名/路径前缀与起始日期过滤）与 `/v1/extract`（支持 JS 渲染与 PDF，`--fresh` 实时抓取）；标准条款允许去标识化用于模型训练（EU 端点 `eu.parallel.ai` 需配 `mode="basic"`） |
| `firecrawl` | `search`, `fetch` | `https://api.firecrawl.dev` | `FIRECRAWL_API_KEY` | 提供 v2 `/v2/scrape`（默认 `proxy=auto` 抗反爬、PDF 页数上限保护与内层状态校验）与 `/v2/search`；云端强制配 Key，自托管 `base_url` 支持无 Key 运行（仅本机回环放行 `http://`，局域网/远程实例需经 `https://`） |
| `github` | `fetch` | `https://api.github.com` | `GITHUB_TOKEN` / `GH_TOKEN`（可选） | 识别 `github.com` 仓库主页、`blob` 源码文件（含 `#L10-L20` 行范围）、Issue、PR（含行级审查评论）、仓库级 Discussion、Commit 与 Release（含 `latest` 及含斜杠 Tag），输出结构化 Markdown；组织级 `/orgs/<org>/discussions/<n>` 自动交由通用提取链处理 |
| `local` | `fetch` | 本地直连抓取 | 无需密钥 | 纯 Go 本地抓取 HTML、Markdown（含 Frontmatter 解析与弱内容自动降级 HTML）、纯文本与 JSON，内置流式 SSR 还原、Readability 正文提取与反爬拦截页检测 |

## 检索选路模式

1. **链式自动回退（`auto`，默认）**：中文走 `search.providers`，非中文走 `search.providers_global`（设 `[]` 或仅显式配置 `search.providers` 时统一走主链）。通用搜优先全网源、单站源兜底，`-s` 命中单站域名（如 `zhihu.com`）时自动置顶；带过滤条件或 `-d content` 时优先原生支持源，遇错误或空结果（`fallback_on_empty`）自动回退。
2. **显式单源（`-p <name>`）**：仅调用指定检索源，失败时直接返回该源的结构化错误，不隐式切换其他源。
3. **多源并发融合（`--fusion` / `-F`）**：并发调用多路检索源（可用 `-p a,b` 或 `search.fusion_providers` / `search.fusion_providers_global` 指定；未指定时自动挑选最多 3 家独立检索源，非中文分池跳过未命中单站源），去重后以加权 RRF 排序（同密钥源不重复加权），并标注多源共识；任一路成功即返回，失败支路记入 `meta.providers`。
4. **过滤降级与严格模式**：无原生支持源或原生源失败回退时，默认在本地执行后过滤并在 `meta.notes` 标注降级；启用 `--strict`（或 `strict: true`）则跳过不原生支持的检索源，不回退降级源。

## 网页提取策略

`veri fetch` 与 MCP `web_fetch` 支持四种策略（`github.com` 结构化页面均优先走 `github` 读取器；本机与内网地址仅由 `local` 处理，绝不发往外部云端源）：

| 策略 | 行为说明 |
| --- | --- |
| `api_first`（默认） | 公网页面优先调用云端提取链（中文站走 `fetch.providers`，非中文站默认走 `fetch.providers_global`，支持 PDF 优先选路与拦截页自动回退）；未配置云端提取源或云端全部失败时回退至 `local` |
| `smart_local` | 公网非 PDF 页面先由 `local` 抓取并做正文质量检测，遇到动态空壳、拦截页或正文过短时切换至云端提取链 |
| `local_only` | 仅使用 `github` 与 `local`，不调用外部云端提取 API |
| `api_only` | 仅使用 `github` 与云端提取链，禁用 `local` 直连抓取（本机与内网地址直接拒绝） |

## 厂商扩展选项

可通过 CLI `-o key=value`（提取同时支持 `--fresh`）或 MCP `options` 字典（及 `web_fetch` 的 `fresh` 字段）按次传入专属参数，不属于当前命中源的参数会被忽略：

| 参数键 | 兼容简写 | 适用源 | 可选值与说明 |
| --- | --- | --- | --- |
| `exa.search_type` | `exa_type` | `exa`（检索） | `lite`、`auto`、`fast`、`instant`、`deep-lite`、`deep`、`deep-reasoning` |
| `exa.category` | `exa_category` | `exa`（检索） | `publication`、`company`、`news`、`pdf`、`github`、`personal site`、`financial report`、`people` |
| `tavily.search_depth` | `tavily_depth` | `tavily`（检索） | `ultra-fast`、`fast`、`basic`、`advanced` |
| `tavily.topic` | `tavily_topic` | `tavily`（检索） | `general`、`news`、`finance` |
| `brave.mode` | `brave_mode` | `brave`（检索） | `auto`、`context`、`web` |
| `brave.threshold` | `brave_threshold` | `brave`（检索） | `strict`、`balanced`、`lenient`、`disabled`（`context` 模式相关性过滤阈值） |
| `brave.goggles` | `brave_goggles` | `brave`（检索） | 自定义 Brave Goggles 内联重排规则或托管 URL |
| `tinyfish.domain_type` | `tinyfish_domain_type` | `tinyfish`（检索） | `web`、`news`、`research_paper` |
| `tencent_wsa.industry` | `wsa_industry` | `tencent_wsa`（检索） | `gov`、`news`、`acad`、`finance` |
| `tencent_wsa.mode` | `wsa_mode` | `tencent_wsa`（检索） | `0`、`1`、`2` |
| `keenable.mode` | `keenable_mode` | `keenable`（检索） | `pro`、`realtime` |
| `zhihu_web.search_db` | `zhihu_web_search_db` | `zhihu_web`（检索） | `all`、`realtime`、`static` |
| `zhihu.sort` | `zhihu_sort` | `zhihu`（检索） | 知乎站内排序/区间表达式（如 `VoteUpCount:desc`） |
| `linkup.depth` | `linkup_depth` | `linkup`（检索） | `flash`、`fast`（默认）、`standard`、`deep` |
| `parallel.mode` | `parallel_mode` | `parallel`（检索） | `turbo`（仅英/日文且不支持路径前缀）、`fast`（默认）、`basic`、`advanced` |
| `parallel.location` | `parallel_location` | `parallel`（检索） | 2 位 ISO 国家码（如 `us`、`gb`、`jp`、`cn`） |
| `firecrawl.category` | `firecrawl_category` | `firecrawl`（检索） | `research`、`pdf`、`developer`、`gov`（`developer` 与 `gov` 均不可与其他类别混用） |
| `fresh` | CLI `--fresh` | `tinyfish` / `exa` / `keenable` / `parallel` / `firecrawl` / CLI 与 MCP 缓存（提取） | `true` / `1`：绕过 CLI/MCP 缓存与上游缓存，强制实时抓取并刷新缓存 |
| `tavily.extract_depth` | `tavily_extract_depth` | `tavily`（提取） | `basic`、`advanced`（未显式指定且因反爬或 JS 拦截页升档时自动切换为 `advanced`） |
| `tinyfish.ttl` | `tinyfish_ttl` | `tinyfish`（提取） | 非负整数秒数；`0` 表示强制实时渲染抓取 |
| `exa.max_age_hours` | `exa_max_age_hours` | `exa`（提取） | `-1`–`720` 整数小时；`0` 强制实时抓取，`-1` 仅读取索引缓存 |
| `linkup.fetch_mode` | `linkup_fetch_mode` | `linkup`（提取） | `standard`（默认）、`pro` |
| `linkup.render_js` | `linkup_render_js` | `linkup`（提取） | `true` / `false`（未显式指定且因反爬或 JS 拦截页升档时自动启用） |
| `firecrawl.proxy` | `firecrawl_proxy` | `firecrawl`（提取） | `auto`（默认）、`basic`、`enhanced` |
| `firecrawl.max_pages` | `firecrawl_max_pages` | `firecrawl`（提取） | `1`–`500` 整数；限制单次提取解析的最大 PDF 页数（默认 `20`） |

## 状态检查与连通性诊断

```bash
veri providers            # 查看各适配器启用状态、凭据来源、端点、超时与运行参数
veri doctor               # 离线检查配置权限、已启用链路、DNS 解析与 Fake-IP 识别
veri doctor --live --yes  # 对每个已启用的检索源发起 1 次真实最小请求验证连通性
```
