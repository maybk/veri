# Usage Guide

Daily handbook for using `veri`: zero-key page extraction, multi-provider API key setup, command recipes, AI agent integration, upgrades, and troubleshooting.

- [Zero-Key Experience](#zero-key-experience)
- [Configure API Keys](#configure-api-keys)
- [Command Recipes](#command-recipes)
- [AI Agent Integration](#ai-agent-integration)
- [Upgrades and Troubleshooting](#upgrades-and-troubleshooting)

## Zero-Key Experience

Extracting regular web pages and GitHub pages (`veri fetch`) works **out of the box with no API keys**:

```bash
# Extract clean Markdown from a web page
veri fetch https://go.dev/doc/go1.27

# Read GitHub repos, source files (with line ranges), issues, PRs, discussions, commits, and releases
veri fetch https://github.com/modelcontextprotocol/go-sdk
veri fetch https://github.com/modelcontextprotocol/go-sdk/blob/main/README.md#L1-L40
```

## Configure API Keys

To enable web search and cloud-enhanced extraction, **configure any single search provider key**. When multiple providers are configured, `veri` enables all of them and automatically fails over on rate limits, timeouts, or errors.

### Quick Setup

Use `veri key set <provider>` to save keys interactively:

```bash
# 1. Pick one or more provider IDs from the table below, then paste the key at the prompt
veri key set exa
veri key set tavily

# 2. List all supported provider IDs, credential sources, and enabled providers
veri key list
veri providers

# 3. Check local config and network; add --live --yes to send 1 minimal live request per enabled search provider
veri doctor
veri doctor --live --yes
```

> Security rule: `veri key set` rejects plaintext keys passed as positional CLI arguments to prevent leaks in shell history.

### Supported Providers and Signup Portals

| Provider ID | Capabilities | Free Tier | Portal |
| --- | --- | --- | --- |
| `deepseek` | Search, Fetch | Paid (¥5 / 1k) | [DeepSeek Platform](https://platform.deepseek.com/api_keys) |
| `keenable` | Search, Fetch | 100k / month (no card) | [Keenable Console](https://app.keenable.ai/console/api-keys) |
| `zhihu` | Search | ~5k / day verified (2 separate pools) | [Zhihu Developer Portal](https://developer.zhihu.com/profile) |
| `exa` | Search, Fetch | $10 / month + $10 signup (no card) | [Exa Dashboard](https://dashboard.exa.ai/api-keys) |
| `tinyfish` | Search, Fetch | 12k search + 1k fetch / day (no card) | [TinyFish Console](https://agent.tinyfish.ai/api-keys) |
| `tencent_wsa` | Search | 2,000 / month (all tiers) | [Tencent Cloud WSA Console](https://console.cloud.tencent.com/wsa) |
| `tavily` | Search, Fetch | 1,000 credits / month (no card) | [Tavily Dashboard](https://app.tavily.com) |
| `brave` | Search | $5 / month (~1k searches, card required) | [Brave Search API](https://api-dashboard.search.brave.com) |
| `linkup` | Search, Fetch | Topped up to $20 / month (work email, no card) | [Linkup Console](https://app.linkup.so) |
| `parallel` | Search, Fetch | Up to $80 signup (no card) + $5 / month w/ card | [Parallel Platform](https://platform.parallel.ai) |
| `firecrawl` | Search, Fetch | 1,000 credits / month (no card, self-hostable) | [Firecrawl Dashboard](https://www.firecrawl.dev/app/api-keys) |

#### Provider-Specific Notes

| Provider ID | Setup Note |
| --- | --- |
| `zhihu` | Copy **Access Secret** from the console and run `veri key set zhihu` to enable both `zhihu` (Zhihu Q&A/articles) and `zhihu_web` (Chinese web) with independent daily pools |
| `tencent_wsa` | Create a dedicated **WSA service API Key** in the WSA console; do not use Tencent Cloud account `SecretId` / `SecretKey` |
| `firecrawl` | Self-hosted instances need no API key; point to your instance with `veri config set providers.firecrawl.base_url <url>` |
| `github` (optional) | Not a search provider; used only by `veri fetch` to raise GitHub API hourly rate limits and access authorized private repos via `veri key set github` ([GitHub Tokens](https://github.com/settings/tokens)) |

<details>
<summary>Expand detailed pricing, use cases, and privacy notes (verified 2026-11)</summary>

| Provider ID | Free Tier & Reference Pricing | Use Cases & Notes |
| --- | --- | --- |
| `deepseek` | No free tier; both Search and Fetch are **¥5 / 1,000 successful requests (¥0.005/req)**; failed and empty responses are not billed | Fast primary Chinese/English search and Markdown fetch (100 QPS search / 25 QPS fetch) |
| `keenable` | **100k requests/month free, no card** (search/fetch count 1 each); overage $4 / 1,000 ($0.004/req) | High-allowance general search and fetch; default policy retains queries and results |
| `zhihu` / `zhihu_web` | Console quota after identity verification (resets 00:00 daily; community tests show ~5,000/day per independent pool once verified, ~10/day unverified) | One `zhihu` key activates both `zhihu` (site Q&A/articles, included in CN `-F`, prioritized on `-s zhihu.com`) and `zhihu_web` (Chinese web, preferred over site search in `auto`) |
| `exa` | **$10/month free, no card** (resets 1st of month) + $10 signup bonus; overage `auto` search $7 / 1,000 ($0.007/req; `lite` $0.004, `deep` $0.012), fetch $1 / 1,000 pages ($0.001/page) | English technical docs, semantic search, and PDF/content extraction; returns exact billed cost in responses |
| `tinyfish` | **Free, no card**: 12,000 searches/day and 1,000 fetches/day (resets 00:00 UTC) | Multilingual search, academic papers, server-side SPA/JS rendering, and PDF extraction |
| `tencent_wsa` | **2,000 requests/calendar month free across all tiers** (reset per account policy); overage ¥14 / ¥20 / ¥26 per 1,000 (`lite` ¥0.014, `standard` ¥0.020, `flagship` ¥0.026; failures not billed) | Chinese news, government, academic, and finance search (requires WSA service API Key) |
| `tavily` | **1,000 credits/month free, no card** (`basic` search 1 credit, `advanced` 2 credits; extract 1 credit per 5 pages); pay-as-you-go $8 / 1,000 credits ($0.008/credit) | General AI agent, news, and finance search with dynamic rendering and PDF extraction |
| `brave` | Search plan **$5/month free (~1,000 requests, card required)**; overage $5 / 1,000 ($0.005/req) | Independent global web index and fast LLM context snippets (`llm/context`) |
| `linkup` | **Auto-topped up to $20/month** (work email needs no card; consumer email requires card; ~4,000 `fast` searches; failed/empty not billed); overage `fast`/`standard` $5 / 1,000 ($0.005/req, `deep` $0.05/req), fetch $0.001–$0.01/page | English search, HTML/PDF, and on-demand JS rendering (defaults to `fast` to avoid high-cost `deep`) |
| `parallel` | **Up to $80 signup credit (no card; tiered by email domain, higher for work/company domains)** plus **$5/month free with card**; overage `turbo`/`fast` search and Extract are $1 / 1,000 ($0.001/req), `basic`/`advanced` $5 / 1,000 ($0.005/req) | English/general search, domain/path prefix filters, and JS/PDF extraction; standard terms allow de-identified data for model training |
| `firecrawl` | **1,000 credits/month free, no card** (1,000 scrapes or 500 searches; PDF billed per page); open-source self-hosting available | Hard anti-bot bypass, dynamic rendering, and self-hosted extraction (`proxy=auto` at no extra cost) |
| `github` (optional) | Public API free without token; token raises hourly limits and unlocks authorized private repos | Structured reader for repos, source line ranges, issues, PRs, discussions, commits, and releases |

> Free tiers and reference prices were verified in 2026-11 and may change at any time; check each provider's console and pricing page. `veri` never hardcodes list prices and tracks cost only when reported by upstream billing or explicitly configured via `price_*`.

</details>

### Recommended Pairings

- **Zero-cost, no-card starter**: `keenable` + `exa` + `tavily` + `parallel` for English search and extraction (add `linkup` with a work email, and `firecrawl` or self-hosted Firecrawl for heavy anti-bot sites); for Chinese search, pair `keenable` + `zhihu` + `tencent_wsa` (2,000/month).
- **Low-cost, high-quality workhorse**: `parallel` (`fast` search and Extract at $1 / 1,000) + `exa` for English; `deepseek` (fast, low-cost, no charge on failures/empty results) + `zhihu` (one key unlocks both `zhihu` and `zhihu_web`) for Chinese.
- **Privacy-sensitive workflows**: `keenable` retains queries/results by default and `parallel` standard terms allow de-identified training use; disable either on demand with `veri config set providers.keenable.enabled false` or switch providers.

### Advanced Key Management

```bash
# Append additional keys for round-robin pooling with deduplication (default overwrites; comma-separated also works)
veri key set exa -a

# Pipe from stdin, batch-import a .env file, or migrate plaintext keys out of config.toml
printf '%s' "$EXA_API_KEY" | veri key set exa
veri key import .env
veri key import --from-config

# Remove a stored key, or temporarily disable a configured provider
veri key unset exa
veri config set providers.keenable.enabled false
```

- **Safety and dry-run**: `veri config set` refuses plaintext `api_key` / `token` values; add `--dry-run` to preview `veri key import --from-config` before rewriting `config.toml`.
- **More configuration**: environment variable overrides, cross-device migration, and additional settings are documented in the [Configuration Guide](./configuration.md).

## Command Recipes

### Web Search

Running `veri "<query>"` is shorthand for `veri search "<query>"`:

```bash
# Basic search (default 8 summaries across the configured failover chain)
veri "golang mcp sdk official"

# Filter by time window, site, and result count
# -t accepts day|week|month|year, 7d|3m, YYYY-MM-DD.., or YYYY-MM-DD..YYYY-MM-DD
veri search "go 1.27 release notes" -t 6m -s go.dev -n 5

# Exclude domains and set language hint
veri search "kubernetes gateway api" -x medium.com --lang en -n 6

# Control detail level: snippet / summary (default) / content (full text)
veri search "sqlite wal checkpoint" -d content -n 3

# Lock a single provider (no failover) or define an explicit failover chain
veri search "rust async traits" -p exa
veri search "rust async traits" -p exa,tavily

# Pass provider-specific options (-o key=value, repeatable; full list in providers.md)
veri search "raft consensus" -p exa -o exa.search_type=deep

# Concurrent multi-provider RRF fusion (without -p, uses fusion pool or first 3 enabled providers)
veri search "raft leader lease" -F
veri search "raft leader lease" -F -p exa,parallel,brave,tavily

# Machine-readable outputs for scripts and pipes (md / json / jsonl / urls)
veri search "go 1.27" -n 5 --json
veri search "go 1.27" -n 3 --format urls

# Bypass CLI search cache (when enabled)
veri search "go 1.27" --no-cache
```

### Web and GitHub Extraction

```bash
# Basic fetch (prefers GitHub reader and configured cloud extractors, falls back to local extraction)
# CLI caches full pages for 15 minutes (~/.cache/veri) so subsequent --section / --match / --offset reads cost nothing
veri fetch https://example.com/article

# Extract a heading section subtree or keyword match windows (combinable)
veri fetch https://example.com/docs --section "Authentication"
veri fetch https://example.com/docs --match "Retry-After"
veri fetch https://example.com/docs --section "Rate Limits" --match "429"

# Limit character budget and paginate (truncation prints outline and next offset on stderr)
veri fetch https://example.com/long-doc --max-chars 12000
veri fetch https://example.com/long-doc --max-chars 12000 --offset 12000

# Explicit fetch chain (single provider disables fallback; conflicts with --strategy)
veri fetch https://example.com/article -p exa,local

# Bypass caches for a live fetch, or restrict to local extraction only
veri fetch https://example.com/status --fresh
veri fetch https://example.com/article --strategy local_only

# Concurrent batch fetch (up to 10 URLs; positional args or '-' from stdin)
veri fetch https://go.dev/doc/go1.27 https://github.com/modelcontextprotocol/go-sdk
veri search "go 1.27 release" -n 3 --format urls | veri fetch -

# Inspect or clear disk cache
veri cache info
veri cache clear
```

## AI Agent Integration

### MCP Server

`veri mcp` runs a standard stdio MCP server exposing `web_search` and `web_fetch` with built-in in-process caching.

Generate a client-ready JSON snippet:

```bash
veri mcp config              # Uses veri from PATH
veri mcp config --full-path  # Uses the absolute binary path (recommended for GUI hosts)
```

Base configuration:

```json
{
  "mcpServers": {
    "veri": {
      "command": "veri",
      "args": ["mcp"]
    }
  }
}
```

Without a global install, you can also start it via `npx`:

```json
{
  "mcpServers": {
    "veri": {
      "command": "npx",
      "args": ["-y", "@maybk/veri", "mcp"]
    }
  }
}
```

Keys saved locally via `veri key set` apply automatically—no need to duplicate plaintext keys in `env`. If `env` is explicitly set in the MCP config, those values take precedence.

### Agent Skill

Provides routing guidelines and extraction recipes for CLI-capable coding agents (usable standalone or alongside MCP). Recommended via [`npx skills`](https://skills.sh) for unified global installation and updates across agents:

```bash
npx skills add maybk/veri -g
```

In offline or Node-free environments, use the built-in installer:

```bash
veri skill install            # Install to user directory ~/.agents/skills/veri/SKILL.md
veri skill install --project  # Install to current workspace .agents/skills/veri/SKILL.md
veri skill install --force    # Overwrite an existing SKILL.md
veri skill print              # Print the embedded SKILL.md
```

## Upgrades and Troubleshooting

### Upgrading

**Install script or manual download**

```bash
veri update --check           # Check whether a newer release is available
veri update                   # Download official archive, verify SHA-256, and update in place
```

In restricted networks, pass `--mirror <https-prefix>` or set `VERI_UPDATE_MIRROR` for archive downloads.

**Package managers** (upgrade through the same package manager to avoid overwriting managed files)

```bash
npm install -g @maybk/veri@latest   # npm (macOS / Linux / Windows)
brew upgrade veri                   # Homebrew (macOS / Linux)
scoop update veri                   # Scoop (Windows)
mise upgrade npm:@maybk/veri        # mise (or mise upgrade github:maybk/veri)
```

### Common Issues

- **No search provider available**: run `veri providers` to check enabled status. Shell `export` variables only last for the current terminal; use `veri key set <provider>` for persistent encrypted storage.
- **Provider errors, timeouts, or rate limits**: run `veri doctor --live --yes` to diagnose authentication or connectivity. Configuring 2 or more providers enables automatic failover.
- **Incomplete SPA / JS-rendered or anti-bot pages**: local extraction does not execute browser JavaScript; configure a cloud extractor supporting dynamic rendering (`firecrawl`, `parallel`, `linkup`, `tavily`, or `tinyfish`) so `veri` escalates automatically.
- **Proxies and transparent proxy (TUN) setups**: honors `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY` or explicit `network.proxy` in `config.toml`; auto-detects dual-stack TUN Fake-IP ranges (inspect with `veri doctor` or pin via `network.fake_ip_cidrs`).
- **Standard exit codes**: `0` success · `1` internal error or failed check · `2` invalid flags/config · `3` missing/invalid key or quota · `4` transient timeout/rate limit · `5` target not found or blocked by security policy · `130` interrupted.

## Further Reading

- [Configuration Guide](./configuration.md): custom routing chains, language-split pools, proxy, TOML schema, environment variables, and security mechanisms.
- [Providers & Routing Guide](./providers.md): adapter technical matrix, 4 fetch strategies, and `-o key=value` provider options.
- [Architecture & Directory Layout](../../zh/dev/architecture.md) (Chinese): overall architecture and module boundaries.
