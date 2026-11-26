# Providers & Routing Guide

> For provider free tiers, signup portals, and everyday setup, see the [Usage Guide](./usage.md).

`veri` uses orthogonal adapter interfaces: search providers implement `core.Searcher` and extractors implement `core.Fetcher`. At startup, adapters activate according to `providers.<name>.enabled` and configured credentials; unconfigured or disabled adapters are skipped silently.

## Built-in Adapter Matrix

| Provider (`name`) | Role | Default Endpoint | Credential Env Vars | Key Technical Traits |
| --- | --- | --- | --- | --- |
| `deepseek` | `search`, `fetch` | `https://api.deepseek.com` | `DEEPSEEK_API_KEY` | `/v1/tools/search` and `/v1/tools/fetch`; builds `site:`, `-site:`, `after:`, and `before:` operators (128 Unicode char query limit); default fetch timeout `10s` |
| `keenable` | `search`, `fetch` | `https://api.keenable.ai` | `KEENABLE_API_KEY` | `/v1/search` (synthesis cards off by default) and `GET /v1/fetch` (supports `--fresh`); domain filters and publish time windows; default policy retains queries and results |
| `zhihu` | `search` | `https://developer.zhihu.com` | `ZHIHU_API_KEY`<br>`ZHIHU_ACCESS_SECRET` | Zhihu site Q&A and articles (`zhihu_search`, max 10 items, `zhihu.com` only); supports vote/comment/time sort, prioritized on `-s zhihu.com`, auto-skipped for non-Zhihu site filters |
| `zhihu_web` | `search` | `https://developer.zhihu.com` | Shared `ZHIHU_API_KEY`<br>`ZHIHU_ACCESS_SECRET` | Zhihu Open Platform Chinese web search (`global_search`, max 20 items); index database switching, time filtering, and highlight/UTM cleanup; shares key with `zhihu` with separate quota |
| `exa` | `search`, `fetch` | `https://api.exa.ai` | `EXA_API_KEY` | `/search` hybrid search and `/contents` HTML/PDF extraction; domain filters, ISO date ranges, content categories, and exact `costDollars` billing in responses |
| `tinyfish` | `search`, `fetch` | `https://api.search.tinyfish.ai`<br>`https://api.fetch.tinyfish.ai` | `TINYFISH_API_KEY` | Dedicated search and fetch endpoints; minute-level freshness, date ranges, academic paper mode, and server-side SPA/JS rendering plus PDF extraction |
| `tencent_wsa` | `search` | `https://api.wsa.cloud.tencent.com` | `TENCENT_WSA_API_KEY`<br>`TENCENTCLOUD_WSA_APIKEY` | Tencent Cloud SearchPro API; supports `lite`, `standard`, and `flagship` modes plus industry classification filters |
| `tavily` | `search`, `fetch` | `https://api.tavily.com` | `TAVILY_API_KEY` | `/search` and `/extract` (dynamic rendering and PDF); 4 search depths, domain/time/language filters, and `usage.credits` accounting |
| `brave` | `search` | `https://api.search.brave.com` | `BRAVE_API_KEY`<br>`BRAVE_SEARCH_API_KEY` | Calls `/res/v1/web/search` for `snippet` and `/res/v1/llm/context` for `summary` / `content`; auto-switches to inline Goggles rules for long site filter lists |
| `linkup` | `search`, `fetch` | `https://api.linkup.so` | `LINKUP_API_KEY` | `/v1/search` (structured results, defaults to `fast` depth) and `/v1/fetch` (HTML/PDF with on-demand JS rendering escalation); domain allow/block lists and date ranges |
| `parallel` | `search`, `fetch` | `https://api.parallel.ai` | `PARALLEL_API_KEY` | `/v1/search` (defaults to `fast` mode; domain/path prefix and start-date filters) and `/v1/extract` (JS rendering, PDF, `--fresh` live fetch); standard terms allow de-identified training use (EU endpoint `eu.parallel.ai` requires `mode="basic"`) |
| `firecrawl` | `search`, `fetch` | `https://api.firecrawl.dev` | `FIRECRAWL_API_KEY` | v2 `/v2/scrape` (`proxy=auto` anti-bot bypass, PDF page-cap guard, inner status validation) and `/v2/search`; cloud requires a key, self-hosted `base_url` runs keyless (`http://` allowed for localhost only; remote/LAN requires `https://`) |
| `github` | `fetch` | `https://api.github.com` | `GITHUB_TOKEN` / `GH_TOKEN` (optional) | Structured Markdown reader for `github.com` repos, `blob` files (including `#L10-L20`), issues, PRs (with inline review comments), repo discussions, commits, and releases (`latest` and slash tags); org-level `/orgs/<org>/discussions/<n>` falls through to general fetch |
| `local` | `fetch` | Direct local HTTP | None | Pure-Go extractor for HTML, Markdown (frontmatter parsing and weak-content HTML fallback), plain text, and JSON with streaming SSR hydration recovery, Readability extraction, and anti-bot challenge detection |

## Search Routing Modes

1. **Sequential failover (`auto`, default)**: Chinese queries use `search.providers`; non-Chinese queries use `search.providers_global` (set `[]` or configure `search.providers` alone to unify). General queries prefer web providers with single-site fallback; matching `-s` domains (e.g. `zhihu.com`) promote the site provider to the top. Active filters or `-d content` prefer native-capable providers and fail over on errors or empty results (`fallback_on_empty`).
2. **Explicit single provider (`-p <name>`)**: calls only the named provider and returns its structured error directly without implicit failover.
3. **Concurrent RRF fusion (`--fusion` / `-F`)**: queries multiple providers in parallel (configured via `-p a,b` or `search.fusion_providers` / `search.fusion_providers_global`; defaults to up to 3 independent providers, skipping unmatched single-site providers on non-Chinese queries), deduplicates URLs, ranks via weighted RRF (without double-weighting shared-key pools), and records multi-provider consensus; returns as soon as any branch succeeds while recording branch status in `meta.providers`.
4. **Filter fallback and strict mode**: when no native-filter provider is available or native providers fail, `veri` simulates filters locally and notes the downgrade in `meta.notes`; `--strict` (or `strict: true`) skips non-native providers without falling back.

## Fetch Strategies

`veri fetch` and MCP `web_fetch` support four strategies (`github.com` structured URLs always prefer the `github` reader; localhost and private targets are handled by `local` only and never sent to external cloud APIs):

| Strategy | Behavior |
| --- | --- |
| `api_first` (default) | Public pages try the cloud fetch chain first (`fetch.providers` for Chinese sites, `fetch.providers_global` for non-Chinese sites, with PDF-aware routing and block-page failover), falling back to `local` if unconfigured or all cloud extractors fail |
| `smart_local` | Non-PDF public pages try `local` first with content quality checks, escalating to cloud extractors on JS shells, anti-bot pages, or insufficient content |
| `local_only` | Uses `github` and `local` only; never calls external cloud fetch APIs |
| `api_only` | Uses `github` and cloud extractors only; disables `local` direct fetch (rejects localhost and private targets) |

## Provider Extension Options

Pass provider-specific parameters per call via CLI `-o key=value` (fetch also supports `--fresh`) or MCP `options` (`web_fetch` also exposes `fresh`); keys belonging to other providers are ignored:

| Option Key | Alias | Applies To | Values & Description |
| --- | --- | --- | --- |
| `exa.search_type` | `exa_type` | `exa` (search) | `lite`, `auto`, `fast`, `instant`, `deep-lite`, `deep`, `deep-reasoning` |
| `exa.category` | `exa_category` | `exa` (search) | `publication`, `company`, `news`, `pdf`, `github`, `personal site`, `financial report`, `people` |
| `tavily.search_depth` | `tavily_depth` | `tavily` (search) | `ultra-fast`, `fast`, `basic`, `advanced` |
| `tavily.topic` | `tavily_topic` | `tavily` (search) | `general`, `news`, `finance` |
| `brave.mode` | `brave_mode` | `brave` (search) | `auto`, `context`, `web` |
| `brave.threshold` | `brave_threshold` | `brave` (search) | `strict`, `balanced`, `lenient`, `disabled` (relevance filter in `context` mode) |
| `brave.goggles` | `brave_goggles` | `brave` (search) | Inline Brave Goggles reranking rules or hosted URL |
| `tinyfish.domain_type` | `tinyfish_domain_type` | `tinyfish` (search) | `web`, `news`, `research_paper` |
| `tencent_wsa.industry` | `wsa_industry` | `tencent_wsa` (search) | `gov`, `news`, `acad`, `finance` |
| `tencent_wsa.mode` | `wsa_mode` | `tencent_wsa` (search) | `0`, `1`, `2` |
| `keenable.mode` | `keenable_mode` | `keenable` (search) | `pro`, `realtime` |
| `zhihu_web.search_db` | `zhihu_web_search_db` | `zhihu_web` (search) | `all`, `realtime`, `static` |
| `zhihu.sort` | `zhihu_sort` | `zhihu` (search) | Zhihu site sort/range expression (e.g. `VoteUpCount:desc`) |
| `linkup.depth` | `linkup_depth` | `linkup` (search) | `flash`, `fast` (default), `standard`, `deep` |
| `parallel.mode` | `parallel_mode` | `parallel` (search) | `turbo` (EN/JA only, no path prefix), `fast` (default), `basic`, `advanced` |
| `parallel.location` | `parallel_location` | `parallel` (search) | 2-letter ISO country code (e.g. `us`, `gb`, `jp`, `cn`) |
| `firecrawl.category` | `firecrawl_category` | `firecrawl` (search) | `research`, `pdf`, `developer`, `gov` (`developer` and `gov` cannot combine with other categories) |
| `fresh` | CLI `--fresh` | `tinyfish` / `exa` / `keenable` / `parallel` / `firecrawl` / CLI & MCP cache (fetch) | `true` / `1`: bypass CLI/MCP and upstream caches for a live fetch and refresh local cache |
| `tavily.extract_depth` | `tavily_extract_depth` | `tavily` (fetch) | `basic`, `advanced` (auto-escalates to `advanced` on anti-bot or JS challenge pages unless explicitly set) |
| `tinyfish.ttl` | `tinyfish_ttl` | `tinyfish` (fetch) | Non-negative integer seconds; `0` forces live rendered fetch |
| `exa.max_age_hours` | `exa_max_age_hours` | `exa` (fetch) | Integer `-1`–`720` hours; `0` forces live fetch, `-1` reads index cache only |
| `linkup.fetch_mode` | `linkup_fetch_mode` | `linkup` (fetch) | `standard` (default), `pro` |
| `linkup.render_js` | `linkup_render_js` | `linkup` (fetch) | `true` / `false` (auto-enabled on anti-bot or JS challenge escalation unless explicitly set) |
| `firecrawl.proxy` | `firecrawl_proxy` | `firecrawl` (fetch) | `auto` (default), `basic`, `enhanced` |
| `firecrawl.max_pages` | `firecrawl_max_pages` | `firecrawl` (fetch) | Integer `1`–`500`; caps parsed PDF pages per request (default `20`) |

## Status and Connectivity Diagnostics

```bash
veri providers            # Inspect adapter status, credential sources, endpoints, timeouts, and runtime settings
veri doctor               # Offline checks for config permissions, active chains, DNS resolution, and Fake-IP detection
veri doctor --live --yes  # Send 1 minimal live request per enabled search provider to verify connectivity
```
