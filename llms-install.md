# veri

> Pure-Go single-binary multi-provider web search and clean Markdown extraction tool (CLI, stdio MCP server, and Agent Skill).

I want you to install and configure `veri` for me. Reply in my language and execute the steps below autonomously.

## OBJECTIVE

Install or update the native `veri` binary, configure the host integration, verify zero-key extraction and local health, and hand off safe local API key setup if needed.

## DONE WHEN

- `veri --version` exits `0` and prints the installed version.
- Integration succeeds: Agent Skill is installed globally (via `npx -y skills add maybk/veri -g -y` or fallback `veri skill install`) with exit `0`, and native MCP is merged cleanly only when requested or required by the host.
- `veri fetch --max-chars 1500 https://github.com/modelcontextprotocol/go-sdk` exits `0` with non-empty Markdown, and `veri doctor` exits `0`.
- Final status is reported, with exact local `veri key set <provider>` instructions if no search provider is enabled yet.

## TODO

- [ ] Step 1: Install or update the native binary
- [ ] Step 2: Configure host integration (default: CLI + Agent Skill; optional: MCP)
- [ ] Step 3: Run zero-key smoke test, diagnostics, and credential check
- [ ] Step 4: Report status and guide local API key setup if needed

## Credential Guardrail

Keep all API keys and tokens strictly on my machine: always direct me to run `veri key set <provider>` in my own terminal (hidden input, encrypted in `~/.config/veri/secrets.json`, shared automatically by CLI and `veri mcp`). Never request secrets in chat, pass plaintext keys as CLI arguments, write them into `config.toml`, or copy them into MCP `env` blocks.

## Step 1: Install or Update Native Binary

1. Require shell command execution; if disabled, ask me to enable it and stop.
2. Check existing installation:
   ```sh
   veri --version
   ```
   - **Exits `0` (already installed)**: run `veri update`. If `veri` reports management by an external package manager, report or follow its printed upgrade command instead of forcing overwrite.
   - **Not found**: install the official native single binary (zero runtime dependencies, SHA-256 verified):
     - **macOS / Linux**:
       ```sh
       curl -fsSL https://raw.githubusercontent.com/maybk/veri/main/scripts/install.sh | sh
       ```
       Default binary: `$HOME/.local/bin/veri`. If not on `PATH`, run `$HOME/.local/bin/veri` directly and tell me to add `$HOME/.local/bin` to `PATH`.
     - **Windows (PowerShell)**:
       ```powershell
       irm https://raw.githubusercontent.com/maybk/veri/main/scripts/install.ps1 | iex
       ```
       Default binary: `%LOCALAPPDATA%\veri\bin\veri.exe`. If not on `PATH`, run `%LOCALAPPDATA%\veri\bin\veri.exe` directly and tell me to add `%LOCALAPPDATA%\veri\bin` to `PATH`.
3. **Gate**: run `veri --version` and confirm exit `0` before moving on.

## Step 2: Configure Host Integration

Choose automatically by host without stopping for a multi-option questionnaire:

### Default (Coding Agents with Shell Access): Agent Skill

Prefer `npx skills` when `npx` is available so the skill is symlinked across all detected coding agents and managed centrally; pass both `-y` flags to stay non-interactive and `-g` for user-global scope:

```sh
npx -y skills add maybk/veri -g -y
```

If `npx` is unavailable or offline, fall back to the built-in native installer (writes `~/.agents/skills/veri/SKILL.md`; add `--force` when updating an existing file, or `--project` only when I ask for workspace scope):

```sh
veri skill install
```

**Gate**: confirm exit `0`.

### Optional (MCP Requested, Non-Shell Clients, or Cline MCP Marketplace): Native MCP Server

Wire `veri mcp` only when I ask for MCP, when installing for a non-shell client (e.g. Claude Desktop), or via Cline MCP Marketplace:

1. Run `veri mcp config --full-path` to obtain the exact stdio JSON entry using the absolute binary path.
2. Merge `veri` into the host's `mcpServers` config without altering existing servers.

## Step 3: Verify Installation & Inspect Credentials

Run these immediately without waiting for API keys (`veri fetch` works with zero keys):

```sh
veri fetch --max-chars 1500 https://github.com/modelcontextprotocol/go-sdk
veri doctor
veri key list
veri providers
```

**Gate**: confirm `veri fetch` exits `0` with non-empty Markdown and `veri doctor` exits `0`. If either fails, diagnose from the output (PATH, proxy, or network) and report the exact blocker rather than claiming success.

## Step 4: Report & Guide Local API Key Setup

1. Report concisely:
   - Installed `veri` version and binary path
   - Active integration (CLI + Agent Skill by default; note in one line that native MCP can be wired on request for in-process search caching, untrusted-content isolation tags, and tool calls without shell approval prompts)
   - Enabled providers
   - Smoke-test and `veri doctor` results
2. If no search provider is enabled yet (web search requires any 1 search provider key), show the table below, recommend 1–2 zero-card free options matching my language, and ask me to run `veri key set <provider>` in my own terminal:

| Provider ID | Capabilities | Free Tier | Portal |
| --- | --- | --- | --- |
| `deepseek` | search, fetch | Paid (¥5 / 1k) | https://platform.deepseek.com/api_keys |
| `keenable` | search, fetch | 100k / month (no card) | https://app.keenable.ai/console/api-keys |
| `zhihu` | search | ~5k / day verified (CN) | https://developer.zhihu.com/profile |
| `exa` | search, fetch | $10 / month + $10 signup (no card) | https://dashboard.exa.ai/api-keys |
| `tinyfish` | search, fetch | 12k search + 1k fetch / day (no card) | https://agent.tinyfish.ai/api-keys |
| `tencent_wsa` | search | 2k / month (CN) | https://console.cloud.tencent.com/wsa |
| `tavily` | search, fetch | 1k credits / month (no card) | https://app.tavily.com |
| `brave` | search | $5 / month (card required) | https://api-dashboard.search.brave.com |
| `linkup` | search, fetch | Topped up to $20 / month (work email, no card) | https://app.linkup.so |
| `parallel` | search, fetch | Up to $80 signup (no card) + $5 / month w/ card | https://platform.parallel.ai |
| `firecrawl` | search, fetch | 1k credits / month (no card, self-hostable) | https://www.firecrawl.dev/app/api-keys |

Provider notes:
- `zhihu`: copy **Access Secret** from the console; `veri key set zhihu` activates both `zhihu` (site Q&A) and `zhihu_web` (Chinese web).
- `tencent_wsa`: create a dedicated **WSA service API Key** (Tencent Cloud `SecretId`/`SecretKey` will not work).
- `firecrawl`: self-hosted instances need no key; run `veri config set providers.firecrawl.base_url <url>`.
- `github` (optional, not a search provider): run `veri key set github` only to raise GitHub API limits or read private repos via `veri fetch` (https://github.com/settings/tokens).

3. After I confirm running `veri key set <provider>`, re-run `veri providers` to confirm activation; only if I approve sending 1 minimal live request per enabled search provider, run `veri doctor --live --yes`.

## EXECUTE NOW

Complete the TODO list above until every `DONE WHEN` condition holds, then report the observed results.
