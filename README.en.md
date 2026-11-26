<div align="center">

# veri

<p>
  <strong>Multi-provider web search and clean Markdown extraction for AI agents and developers</strong><br>
  Single pure-Go binary with zero runtime dependencies — shipping a <strong>CLI</strong>, <strong>MCP Server</strong>, and <strong>Agent Skill</strong>
</p>

**[简体中文](./README.md)** · **English** &nbsp;|&nbsp; **[Usage Guide](./docs/en/guide/usage.md)**

![Go](https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go&logoColor=white)
![MCP](https://img.shields.io/badge/MCP-stdio-4A5568)
![macOS](https://img.shields.io/badge/macOS-13.0+-000000?logo=apple&logoColor=white)
![Windows](https://img.shields.io/badge/Windows-10%2F11-0078D6?logo=windows&logoColor=white)
![Linux](https://img.shields.io/badge/Linux-Static-FCC624?logo=linux&logoColor=black)

<br>

<img src="assets/demo-en.svg" alt="veri CLI demo" width="880">

</div>

---

## 📦 Download & Installation

### Install with AI

Paste this prompt into your AI agent (Claude Code, Cursor, Codex, Cline, etc.):

```text
Install and configure veri for me: https://raw.githubusercontent.com/maybk/veri/main/llms-install.md
```

### Install Script

**macOS / Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/maybk/veri/main/scripts/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/maybk/veri/main/scripts/install.ps1 | iex
```

### Package Managers

**npm (macOS / Linux / Windows)**

```bash
npm install -g @maybk/veri
```

**Homebrew (macOS / Linux)**

```bash
brew install maybk/tap/veri
```

**Scoop (Windows)**

```powershell
scoop bucket add maybk https://github.com/maybk/homebrew-tap
scoop install maybk/veri
```

**mise (macOS / Linux / Windows)**

```bash
mise use -g npm:@maybk/veri      # or mise use -g github:maybk/veri
```

<!-- Enabled once the official WinGet PR is merged
**WinGet (Windows)**

```powershell
winget install maybk.Veri
```
-->

### Manual Download

Prebuilt archives and `SHA256SUMS.txt` checksums are available on [GitHub Releases](https://github.com/maybk/veri/releases); script or manual binary installs can check and upgrade via `veri update`. To build from source, see the [Development & Build Guide](./docs/zh/dev/build.md) (Chinese).

## 🚀 Quick Start

> [!TIP]
> Web page and GitHub extraction works **out of the box with no API keys**; configuring any single search provider key unlocks web search and cloud extraction. For full walkthroughs, command recipes, and troubleshooting, see the **[Usage Guide](./docs/en/guide/usage.md)**.

### 1. Configure API Keys

Save any search provider key interactively with `veri key set <provider>`; encrypted credentials are shared automatically between the CLI and MCP server ([supported providers and signup portals](./docs/en/guide/usage.md#supported-providers-and-signup-portals)):

```bash
veri key set exa        # Replace with any provider ID (e.g. tavily, parallel, brave, deepseek)
veri key list           # List all supported provider IDs and credential sources
veri providers          # Inspect enabled search and fetch providers
veri doctor             # Verify local configuration and network health
```

### 2. Command-Line Usage

```bash
# Extract web pages or GitHub repos / issues / PRs (no API key required)
veri fetch https://github.com/modelcontextprotocol/go-sdk
veri fetch https://example.com/article --section "Authentication" --max-chars 20000

# Web search (requires any one search provider key; pass a query directly or use 'search' with filter flags)
veri "golang mcp sdk official"
veri search "go 1.27 release notes" -t 6m -s go.dev -n 5

# Concurrent fusion search with JSON output
veri search "raft leader lease" --fusion -p exa,parallel --json
```

### 3. MCP Server Integration

Register `veri mcp` in an MCP-capable client to expose `web_search` and `web_fetch` tools with shared in-process caching (run `veri mcp config` to generate the JSON snippet); keys stored locally take effect automatically with no need to duplicate `env`:

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

Without a global install, you can also run it directly via `npx`:

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

### 4. Agent Skill Installation

Provides routing strategies and extraction recipes for CLI-capable coding agents (usable standalone with the CLI or alongside MCP). Recommended via [`npx skills`](https://skills.sh) for unified global installation and updates across agents:

```bash
npx skills add maybk/veri -g
```

In offline or Node-free environments:

```bash
veri skill install   # Write to the standard .agents/skills directory
veri skill print     # Print the embedded SKILL.md
```

## ✨ Features

### 🎯 Core Capabilities

- **Multi-Provider Search & Fallback** - Built-in support for DeepSeek, Keenable, Zhihu, Exa, TinyFish, Tencent WSA, Tavily, Brave, Linkup, Parallel, and Firecrawl, enabled automatically from configured keys; supports automatic failover, language-aware routing, and concurrent fusion deduplication
- **Clean Web & GitHub Extraction** - Built-in structured GitHub reader and pure-Go local extractor turn web pages and GitHub repos / issues / PRs / discussions / commits / releases into clean Markdown with zero keys; configured cloud extractors add dynamic rendering and PDF support, plus batch fetching, section slicing, and keyword matching
- **Unified Filter Syntax** - One consistent set of flags for time windows, site allow/block lists, and language filters across providers, with strict native enforcement when needed
- **CLI / MCP / Skill in One Binary** - One static executable delivers an interactive/scriptable CLI, a standard stdio MCP server, and Agent Skill integration for AI agents

### 🔧 Security & Reliability

- **SSRF Protection & Proxy Friendly** - Local fetching blocks loopback, private, and cloud metadata targets by default while working smoothly with transparent proxy (TUN) setups
- **Billing-Safe Retries & Key Pools** - Never blindly replays timed-out or dropped requests; supports round-robin multi-key pools with cooldowns and tracks cost only from explicit pricing or upstream billing
- **Encrypted Storage & Safe Outputs** - Encrypts API keys locally, redacts secrets in logs and diagnostics, and isolates external content in MCP outputs to mitigate prompt injection
- **Automatic CLI Localization** - Detects OS language for human CLI output (`zh-CN` / `en-US`) while keeping `--json`, MCP schemas, and `SKILL.md` in deterministic English

## 🖥️ Supported Platforms

- **macOS** - macOS 13.0 (Ventura) or later, Apple Silicon (`arm64`) and Intel (`amd64`)
- **Windows** - Windows 10 / 11 (`amd64` / `arm64`)
- **Linux** - Static binaries for `amd64` and `arm64`

## 🏗️ Architecture

- **Go 1.27+** - Pure-Go static binary with zero runtime dependencies
- **`modelcontextprotocol/go-sdk`** - Official MCP stdio server and structured input/output schemas
- **`spf13/cobra` + `BurntSushi/toml`** - CLI command routing and strict TOML configuration decoding
- **`go-readability` + `x/net/html`** - Pure-Go local main-content extraction and compact Markdown conversion

```text
main.go -> internal/cli | internal/mcpserver -> internal/app -> internal/router -> internal/provider/* -> internal/httpx
```

## 📚 Documentation

- [Usage Guide](./docs/en/guide/usage.md)
- [Configuration Guide](./docs/en/guide/configuration.md)
- [Providers & Routing Guide](./docs/en/guide/providers.md)
- [Architecture & Directory Layout](./docs/zh/dev/architecture.md) (Chinese)
- [Development & Build Guide](./docs/zh/dev/build.md) (Chinese)
- [Release Guide](./docs/zh/dev/release.md) (Chinese)
- [Changelog](./CHANGELOG.md)

## ⚠️ Disclaimer

1. **Compliance** - Use this tool in accordance with applicable laws, target website terms of service, and upstream API policies.
2. **Privacy** - Configuration files and API keys stay in `~/.config/veri/` on your local machine; requests and credentials go directly to enabled upstream providers, with no relay servers or telemetry.
3. **Liability** - Search results and extracted web pages originate from third-party sources; the authors assume no liability for external content accuracy or upstream API charges.

---

<div align="center">
  <p>If you find this project helpful, please consider giving it a ⭐️!</p>
</div>

[![Star History Chart](https://api.star-history.com/svg?repos=maybk/veri&type=Date)](https://www.star-history.com/#maybk/veri&Date)
