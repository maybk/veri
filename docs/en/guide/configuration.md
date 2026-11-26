# Configuration Guide

## Config Directory and Precedence

On macOS, Windows, and Linux, `veri` stores configuration, encrypted credentials, and the local master key under `~/.config/veri/` (`%USERPROFILE%\.config\veri\` on Windows), shared automatically between the CLI and `veri mcp`:

```text
~/.config/veri/
├── config.toml                # Optional: custom overrides only (unset fields track version defaults)
├── secrets.json               # Encrypted credential store written by veri key (owner-only permissions)
└── secret.key                 # Cross-platform encryption master key (owner-only permissions)
```

> **Cross-device migration**: copy `secrets.json` and `secret.key` (plus optional `config.toml`) from `~/.config/veri/` into the same directory on another macOS / Windows / Linux machine. Never commit these files to a public repository.

Configuration and credentials merge from highest to lowest precedence:

1. **CLI flags / MCP request arguments** (e.g. `-p`, `--strategy`, `--timeout`, `--ui-lang`)
2. **`VERI_*` runtime environment variables** (e.g. `VERI_PROVIDERS`, `VERI_FETCH_STRATEGY`, `VERI_LANG`)
3. **Provider credential environment variables** (e.g. `EXA_API_KEY`, `DEEPSEEK_API_KEY`; includes MCP client `env` overrides, always above local files)
4. **Local encrypted credential store** (`~/.config/veri/secrets.json`, managed by `veri key`)
5. **TOML config file** (`env:` references or plaintext `api_key` in `~/.config/veri/config.toml`)
6. **Built-in defaults**

Without `--config`, `veri` uses the first non-empty path in order:

| Case | Lookup Order |
| --- | --- |
| Explicit path | `--config <path>` or `VERI_CONFIG` |
| XDG override | `XDG_CONFIG_HOME/veri/config.toml` (when `XDG_CONFIG_HOME` is set) |
| Default (macOS / Windows / Linux) | `~/.config/veri/config.toml` |

CLI disk cache lives in `~/.cache/veri/` (`%USERPROFILE%\.cache\veri\` on Windows) with precedence `VERI_CACHE_DIR` > `cache.dir` > `XDG_CACHE_HOME/veri` > `~/.cache/veri`; inspect or clear with `veri cache info` / `veri cache clear`.

Common configuration commands (for API key setup and management, see the [Usage Guide](./usage.md#configure-api-keys)):

```bash
# Single-field overrides (supports Tab completion; unset fields inherit version defaults)
veri config set providers.zhihu_web.search_db realtime        # Update one field
veri config set search.providers exa,parallel,keenable        # Lists accept comma-separated values or TOML arrays
veri config set search.providers_global ""                    # Set empty array to disable non-Chinese split chain
veri config get providers.zhihu_web.search_db                 # Read effective value (add --json for source)
veri config unset providers.zhihu_web.search_db               # Remove override and restore default

# Inspection and reference template
veri config show --overrides                   # Show customized fields only (supports --json)
veri config show                               # Show merged, redacted effective config (supports --json)
veri config init                               # Generate commented template (mode 0600; --force to overwrite)
veri config path                               # Print active config file path
```

`config.toml` enforces strict unknown-key and value validation. `veri config set` refuses plaintext `api_key` / `token` values (only `env:VAR_NAME` is allowed); store keys via `veri key set <provider>` or `veri key import`. `veri key import --from-config` encrypts plaintext keys from `config.toml` and strips plaintext lines (restoring template comments or removing sparse lines) while keeping other settings and `env:` references; supports `--dry-run`, `--overwrite`, and `--keep-config`.

## Core Configuration Fields

### 1. UI, Search, and Fetch Strategy

| Field | Default | Description |
| --- | --- | --- |
| `ui.lang` | `"auto"` | CLI language: `auto` (OS locale, falls back to English for non-Chinese), `zh`, `en` (accepts standard locales like `zh_CN.UTF-8`) |
| `search.providers` | `["deepseek", "keenable", "zhihu", "tencent_wsa", "zhihu_web", "brave", "parallel", "exa", "tavily", "linkup", "tinyfish", "firecrawl"]` | Default search chain (Chinese-first; general `auto` prefers web providers and prioritizes single-site providers on matching `-s`; applies to all languages if set without `providers_global`) |
| `search.providers_global` | `["keenable", "parallel", "exa", "brave", "linkup", "tavily", "deepseek", "tinyfish", "firecrawl", "tencent_wsa", "zhihu_web", "zhihu"]` | Non-Chinese search chain (used for English/CJK/other queries or non-Chinese `--lang`; set `[]` to disable) |
| `search.fusion_providers` | `[]` | `--fusion` provider pool (empty auto-selects up to 3 independent providers from `search.providers`; applies to all languages when set alone) |
| `search.fusion_providers_global` | `[]` | Non-Chinese `--fusion` pool (empty auto-selects up to 3 independent web providers from `search.providers_global`; set `[]` to disable) |
| `search.strategy` | `"auto"` | `auto` (sequential failover) or `fusion` (concurrent RRF fusion) |
| `search.count` | `8` | Default result count (`1`–`50`) |
| `search.detail` | `"summary"` | Result detail level: `snippet`, `summary`, `content` |
| `search.timeout` | `"20s"` | Total timeout per search request |
| `search.fallback_on_empty` | `true` | Fail over to the next provider on empty results (stops early after 2 consecutive native-filter empty responses) |
| `search.strict_filters` | `false` | When `true`, only use providers natively supporting active filters (disables local post-filter simulation) |
| `search.fusion_k` | `60` | RRF (Reciprocal Rank Fusion) smoothing constant |
| `search.summary_max_chars` | `800` | Max characters per item `summary` |
| `search.content_max_chars` | `0` | Max characters per item `content` (`0` = unlimited) |
| `search.max_chars` | `0` | Total character budget for CLI search responses (`0` = unlimited) |
| `fetch.strategy` | `"api_first"` | Fetch strategy: `api_first` (cloud first, local fallback), `smart_local`, `local_only`, `api_only` |
| `fetch.providers` | `["keenable", "tavily", "firecrawl", "parallel", "exa", "linkup", "deepseek", "tinyfish"]` | Default cloud fetch chain (Chinese sites first; applies to all sites if set without `providers_global`) |
| `fetch.providers_global` | `["keenable", "tavily", "parallel", "exa", "linkup", "firecrawl", "tinyfish", "deepseek"]` | Non-Chinese cloud fetch chain (set `[]` to disable split chain) |
| `fetch.timeout` | `"45s"` | Total timeout per fetch request (outer cap over per-provider `fetch_timeout`) |
| `fetch.max_bytes` | `"5MB"` | Max decompressed response body size for local fetch |
| `fetch.min_content_chars` | `300` | Minimum content length threshold for the local extraction quality gate |
| `fetch.respect_robots` | `false` | Whether local fetch checks and honors target `robots.txt` |
| `fetch.user_agent` | `"browser"` | Local fetch User-Agent: `browser`, `veri`, or custom string |
| `fetch.max_chars` | `0` | Default character cap for CLI fetch output (`0` = full text) |

### 2. Cache Configuration

Caching is unified under `[cache]`: CLI uses local disk cache; MCP search uses in-process memory cache while MCP fetch shares the local disk cache (private targets and `Cache-Control: no-store` responses are never written to disk). Unset keys under `[cache.cli]` and `[cache.mcp]` inherit `[cache]`; set `"0s"` or `false` to disable only one side.

| Field | Default | Description |
| --- | --- | --- |
| `cache.enabled` | `true` | Master cache switch (`false` disables both CLI and MCP caching) |
| `cache.dir` | `""` | Disk cache directory (empty uses `~/.cache/veri`) |
| `cache.fetch_ttl` | `"15m"` | Default page cache TTL (shared by CLI and MCP) |
| `cache.search_ttl` | `"0s"` | Default search cache TTL (disabled by default in CLI for freshness) |
| `cache.max_bytes` | `"32MB"` | Disk cache size cap (evicts least-recently-used entries) |
| `cache.cli.enabled` | Inherits `cache.enabled` | Optional override: toggle CLI disk cache only |
| `cache.cli.fetch_ttl` | Inherits `cache.fetch_ttl` | Optional override: set `"0s"` to disable CLI fetch cache only |
| `cache.cli.search_ttl` | Inherits `cache.search_ttl` | Optional override: e.g. `"2m"` to enable CLI search cache |
| `cache.mcp.enabled` | Inherits `cache.enabled` | Optional override: toggle MCP cache only |
| `cache.mcp.fetch_ttl` | Inherits `cache.fetch_ttl` | Optional override: set `"0s"` to disable MCP fetch cache only |
| `cache.mcp.search_ttl` | `"5m"` | MCP in-process search cache TTL (independent of `fetch_ttl`) |
| `cache.mcp.max_entries` | `256` | Max MCP in-memory cache entries (`0` disables) |

### 3. MCP Server, Network, and Provider Settings

| Field | Default | Description |
| --- | --- | --- |
| `mcp.text_format` | `"markdown"` | MCP text block format: `markdown` or `json` |
| `mcp.search_max_chars` | `24000` | Default character budget for `web_search` |
| `mcp.fetch_max_chars` | `30000` | Default character budget for `web_fetch` |
| `mcp.content_max_chars` | `3000` | Max characters per item `content` in MCP search |
| `mcp.max_count` | `20` | Max results per `web_search` call (`1`–`50`) |
| `network.proxy` | `""` | Explicit HTTP/SOCKS5 proxy URL (empty honors `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY`) |
| `network.fake_ip_cidrs` | `["198.18.0.0/15", "2001:2::/48"]` | Static transparent-proxy Fake-IP allowlist (public hostnames only) |
| `network.fake_ip_auto` | `true` | Auto-detect transparent-proxy Fake-IP ranges (never allows loopback, private, metadata, or internal hostnames) |
| `network.allow_private` | `false` | Allow local extractor to reach loopback or private IPs (blocked by default for SSRF defense) |
| `providers.<name>.enabled` | `true` | Enable or disable a provider (`false` disables individually) |
| `providers.<name>.api_key` | `"env:..."` | `env:VAR` reference or key string; comma-separated keys enable round-robin pooling and tiered cooldown |
| `providers.<name>.timeout` / `fetch_timeout` | Per-provider | Upstream search/fetch timeout (search default `10s`; fetch default `10s` for `deepseek`, `45s` for `firecrawl`, `25s` for other cloud extractors) |
| `providers.<name>.concurrency` | `4` (`2` for `firecrawl`) | Max in-process concurrent requests per upstream |
| `providers.<name>.qps` | `0` | Optional in-process QPS limiter (`0` = unlimited) |
| `providers.<name>.price_*` | `""` | Optional unit price for cost estimation (e.g. `USD:0.005`, `CNY:0.005`); omitted when unconfigured and upstream reports no billing |

## Environment Variable Reference

### Provider Credentials

| Variable | Alias | Purpose |
| --- | --- | --- |
| `DEEPSEEK_API_KEY` | — | DeepSeek Search / Fetch API key |
| `KEENABLE_API_KEY` | — | Keenable Search / Fetch API key |
| `ZHIHU_API_KEY` | `ZHIHU_ACCESS_SECRET` | Zhihu Open Platform web & site search Access Secret |
| `EXA_API_KEY` | — | Exa Search / Contents API key |
| `TINYFISH_API_KEY` | — | TinyFish Search / Fetch API key |
| `TENCENT_WSA_API_KEY` | `TENCENTCLOUD_WSA_APIKEY` | Tencent Cloud Web Search API key |
| `TAVILY_API_KEY` | — | Tavily Search / Extract API key |
| `BRAVE_API_KEY` | `BRAVE_SEARCH_API_KEY` | Brave Search API key |
| `LINKUP_API_KEY` | — | Linkup Search / Fetch API key |
| `PARALLEL_API_KEY` | — | Parallel Search / Extract REST API key |
| `FIRECRAWL_API_KEY` | — | Firecrawl v2 Scrape / Search API key (optional for self-hosted `base_url`) |
| `GITHUB_TOKEN` | `GH_TOKEN` | GitHub REST API token (optional; raises anonymous rate limit) |

### Runtime and Behavior Overrides

| Variable | Purpose |
| --- | --- |
| `VERI_CONFIG` | Override config file path |
| `VERI_SECRETS_DIR` | Override encrypted credential store directory (defaults to active config directory) |
| `VERI_LANG` | Override CLI language (`auto` / `zh` / `en`, accepts standard locales) |
| `VERI_PROVIDERS` | Override `search.providers` (comma-separated) |
| `VERI_FETCH_PROVIDERS` | Override `fetch.providers` (comma-separated) |
| `VERI_FETCH_STRATEGY` | Override `fetch.strategy` |
| `VERI_TIMEOUT` | Override `search.timeout` |
| `VERI_CACHE_DIR` | Override disk cache directory (higher precedence than `cache.dir`) |
| `VERI_CACHE_FETCH_TTL` | Override both CLI and MCP fetch cache TTL (`"0s"` disables) |
| `VERI_CACHE_SEARCH_TTL` | Override both CLI and MCP search cache TTL (e.g. `"2m"` enables CLI search cache) |
| `VERI_NO_CACHE` | Set `1` / `true` to disable all caching for the current process |
| `VERI_LOG_LEVEL` | Diagnostic log level: `debug` / `info` / `warn` (default) / `error` |
| `VERI_LOG_FORMAT` | Diagnostic log format: `text` (default) / `json` |
| `VERI_UPDATE_MIRROR` | Optional HTTPS mirror prefix for `veri update` and install scripts |
| `NO_COLOR` | Non-empty disables ANSI color output |

## Security and Reliability

1. **SSRF defense, private network isolation, and TUN proxy support**: local fetch validates resolved IPs at dial time and re-validates every redirect hop, blocking loopback, private, link-local, cloud metadata (`169.254.169.254`), and CGNAT ranges by default. Only public hostnames resolving to `network.fake_ip_cidrs` or auto-detected TUN Fake-IP ranges are allowed; literal IPs and internal suffixes (`.local`, `.internal`) stay blocked. Private targets are handled locally only and never forwarded to cloud extractors; fixed provider and GitHub API endpoints block HTTPS downgrade, loopback redirects, and cross-origin credential leaks.
2. **Billing-safe retries and key-pool cooldown**: read timeouts or dropped connections after request dispatch are never blindly replayed to avoid double billing; at most 1 retry is performed on explicitly unbilled `408 / 429 / 500 / 502 / 503 / 504` responses or when rotating to a healthy backup key, with tiered cooldowns for rate-limit, billing, and auth failures.
3. **End-to-end redaction and permission auditing**: `veri config show` (including `--overrides`), `veri config get`, error messages, and diagnostic logs automatically mask known secrets, auth headers, proxy credentials, and sensitive query parameters. `veri config set` blocks plaintext `api_key` / `token` writes; overly broad file permissions (wider than `0600` on Unix or exposed Windows ACLs) trigger warnings at startup and in `veri doctor`.
4. **Untrusted content isolation and invisible-character stripping**: MCP text outputs wrap external pages in per-response randomized `<untrusted_web_content id="...">` boundaries (escaping embedded matching tags) and strip invisible Unicode control/variation characters and hidden zero-visibility CSS nodes to mitigate indirect prompt injection.
5. **Cache safety boundaries**: disk cache writes atomically with `0700`/`0600` permissions and hashed filenames; refuses to cache private/loopback targets, low-quality fallback pages, partial fusion responses, or `Cache-Control: no-store` responses; GitHub cache keys isolate by active token fingerprint; `veri cache clear` removes managed cache entries only.
