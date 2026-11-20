# 使用指南

本文面向日常使用 `veri` 的用户，涵盖零密钥网页读取、各检索源 API Key 官方申请入口与加密配置、常用命令行配方、AI Agent 接入以及升级与排查。

## 零密钥开箱体验

提取普通网页与 GitHub 页面（`veri fetch`）**无需任何 API Key**，安装完成后即可直接使用：

```bash
# 提取普通网页正文为干净 Markdown
veri fetch https://go.dev/doc/go1.27

# 结构化读取 GitHub 仓库、源码文件（支持行号范围）、Issue、PR、Discussion、Commit 与 Release
veri fetch https://github.com/modelcontextprotocol/go-sdk
veri fetch https://github.com/modelcontextprotocol/go-sdk/blob/main/README.md#L1-L40
```

## 申请与配置 API Key

启用联网搜索与云端增强提取时，**只需配置任意 1 个检索源密钥即可开始使用**；配置多个源时，`veri` 会自动启用已配置的源，并在单源限流、超时或异常时按顺序自动故障回退。

### 选型与官方申请入口

| 标识 | 能力 | 适用场景 | 官方控制台入口 | 配置命令 |
| --- | --- | --- | --- | --- |
| `deepseek` | 搜索、云端提取 | 中英文通用联网检索与正文提取 | [DeepSeek 开放平台](https://platform.deepseek.com/api_keys) | `veri key set deepseek` |
| `keenable` | 搜索、云端提取 | 通用联网检索与网页 Markdown 提取（默认策略会留存查询与结果） | [Keenable Console](https://app.keenable.ai/console/api-keys) | `veri key set keenable` |
| `zhihu` | 搜索 | 中文全网搜索（默认）与知乎站内问答/文章检索（`-o zhihu.scope=zhihu`） | [知乎数据开放平台](https://developer.zhihu.com/profile) | `veri key set zhihu` |
| `exa` | 搜索、云端提取 | 英文技术文档、语义检索与学术内容 | [Exa Dashboard](https://dashboard.exa.ai/api-keys) | `veri key set exa` |
| `tinyfish` | 搜索、云端提取 | 中英文检索、学术论文与动态网页（SPA / JS）渲染提取 | [TinyFish Console](https://agent.tinyfish.ai/api-keys) | `veri key set tinyfish` |
| `tencent_wsa` | 搜索 | 中文资讯、政务、学术与财经检索（需开通并创建服务 API Key，非通用 SecretId/Key） | [腾讯云联网搜索控制台](https://console.cloud.tencent.com/wsa) | `veri key set tencent_wsa` |
| `tavily` | 搜索、云端提取 | 面向 AI Agent 的通用、新闻、财经检索与正文提取 | [Tavily Dashboard](https://app.tavily.com) | `veri key set tavily` |
| `brave` | 搜索 | 全球网页索引与大模型上下文摘要检索 | [Brave Search API](https://api-dashboard.search.brave.com) | `veri key set brave` |
| `linkup` | 搜索、云端提取 | 英文检索、HTML/PDF 与按需 JS 渲染提取 | [Linkup Console](https://app.linkup.so) | `veri key set linkup` |
| `firecrawl` | 搜索、云端提取 | 高难反爬与动态网页提取，支持开源自托管实例 | [Firecrawl Dashboard](https://www.firecrawl.dev/app/api-keys) | `veri key set firecrawl` |
| `github`（可选） | GitHub 读取 | 公开仓库无需令牌；配置后可提高限额并读取有权限的私有仓库 | [GitHub Tokens](https://github.com/settings/tokens) | `veri key set github` |

> 各上游服务的计费方式、套餐与调用限额请以对应官方控制台为准。

### 加密保存与验证

推荐通过 `veri key set <provider>` 交互式写入密钥。命令会自动隐藏终端输入并将凭据加密保存在本机 `~/.config/veri/secrets.json`（Windows 为 `%USERPROFILE%\.config\veri\secrets.json`），CLI 与 `veri mcp` 自动共享，避免明文密钥残留在 Shell 历史或共享配置中：

```bash
# 1. 交互式无回显保存密钥（按需替换为上表任意标识）
veri key set deepseek

# 也支持从标准输入管道导入（适合自动化或密码管理器联动）
printf '%s' "$EXA_API_KEY" | veri key set exa

# 2. 查看当前已生效的密钥来源与启用的源
veri key list
veri providers

# 3. 离线检查配置与网络环境；加 --live --yes 可发起一次最小真实请求验证密钥可用性
veri doctor
veri doctor --live --yes
```

常用密钥管理与临时覆盖：

- **移除已保存密钥**：`veri key unset <provider>`
- **临时用环境变量覆盖**：直接导出对应环境变量（如 `export DEEPSEEK_API_KEY="sk-..."`、`export EXA_API_KEY="exa_..."`），优先级高于本地加密库与 `config.toml`。完整变量名见 [配置说明](./configuration.md)。
- **安全约束**：禁止将明文密钥作为命令行位置参数传入，`veri key set` 会拒绝多余参数以防泄露到命令历史。

## 常用命令速查

### 联网检索

直接执行 `veri "<查询词>"` 等价于 `veri search "<查询词>"`：

```bash
# 基础搜索（默认返回 8 条摘要，按已配置检索源链路自动执行）
veri "golang mcp sdk official"

# 限定时间范围、站点与返回条数
# -t 支持 day|week|month|year、7d|3m 或精确区间 YYYY-MM-DD..YYYY-MM-DD
veri search "go 1.27 release notes" -t 6m -s go.dev -n 5

# 排除指定站点并限定语言
veri search "kubernetes gateway api" -x medium.com --lang en -n 6

# 指定结果详略级别：snippet（精简）/ summary（默认）/ content（含正文）
veri search "sqlite wal checkpoint" -d content -n 3

# 强制指定单一检索源（不自动切换），或按指定顺序回退
veri search "rust async traits" -p exa
veri search "rust async traits" -p exa,tavily

# 多源并发融合去重（不传 -p 时使用 search.fusion_providers 或前 3 个已启用源）
veri search "raft leader lease" -F
veri search "raft leader lease" -F -p deepseek,exa,tavily,brave

# 输出结构化格式，便于脚本与管道联动（支持 md / json / jsonl / urls）
veri search "go 1.27" -n 5 --json
veri search "go 1.27" -n 3 --format urls
```

### 网页与 GitHub 正文提取

```bash
# 基础抓取（优先 GitHub 读取器与已配置的云端提取源，未配置或失败时自动回退本地提取）
veri fetch https://example.com/article

# 长文按标题章节截取，或按关键词定位相关段落与代码行窗口（两者可组合使用）
veri fetch https://example.com/docs --section "Authentication"
veri fetch https://example.com/docs --match "Retry-After"
veri fetch https://example.com/docs --section "Rate Limits" --match "429"

# 限制单次输出字符数并分段续读（截断时 stderr 会附带大纲与续读参数）
veri fetch https://example.com/long-doc --max-chars 12000
veri fetch https://example.com/long-doc --max-chars 12000 --offset 12000

# 显式指定提取源顺序（单值禁用回退；与 --strategy 互斥）
veri fetch https://example.com/article -p exa,local

# 要求上游强制实时抓取（CLI 无本地磁盘缓存，MCP 模式下同时绕过进程内缓存），或仅本地直连
veri fetch https://example.com/status --fresh
veri fetch https://example.com/article --strategy local_only

# 批量并发抓取（单次最多 10 个 URL；支持多参数或通过 '-' 从标准输入逐行读取）
veri fetch https://go.dev/doc/go1.27 https://github.com/modelcontextprotocol/go-sdk
veri search "go 1.27 release" -n 3 --format urls | veri fetch -
```

## 接入 AI Agent

### MCP 服务端

`veri mcp` 提供标准 stdio MCP 服务，暴露 `web_search` 与 `web_fetch` 两个工具并自带进程内短时缓存。

执行以下命令导出配置片段并粘贴到支持 MCP 的客户端配置中：

```bash
veri mcp config              # 使用 PATH 中的 veri
veri mcp config --full-path  # 客户端无法继承 PATH 时，输出二进制绝对路径
```

导出的基础配置如下：

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

未全局安装时，也可通过 `npx` 直接拉起 MCP 服务：

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

已在 `~/.config/veri/` 通过 `veri key set` 保存密钥时，MCP 配置中无需重复填写明文 `env`；如在 MCP 配置中显式传入 `env`，将以传入的环境变量为准。

### Agent Skill

推荐使用 [`npx skills`](https://skills.sh) 跨多 Agent 宿主统一安装与更新：

```bash
npx skills add maybk/veri
```

无 Node.js 或离线环境可使用内置命令直接安装：

```bash
veri skill install            # 安装到用户目录 ~/.agents/skills/veri/SKILL.md
veri skill install --project  # 安装到当前工作目录 .agents/skills/veri/SKILL.md
veri skill install --force    # 覆盖更新已存在的 SKILL.md
veri skill print              # 直接输出内置 SKILL.md 原文
```

## 版本升级与常见问题

### 升级版本

**一键脚本或手动下载安装**

```bash
veri update --check           # 仅检查是否存在新版本
veri update                   # 下载官方发布包、校验 SHA-256 并原地升级
```

网络受限时可通过 `--mirror <https-prefix>` 或环境变量 `VERI_UPDATE_MIRROR` 指定归档下载镜像前缀。

**包管理器安装**（请使用对应包管理器升级，避免覆盖托管目录）

```bash
npm install -g @maybk/veri@latest   # npm（macOS / Linux / Windows）
brew upgrade veri                   # Homebrew（macOS / Linux）
scoop update veri                   # Scoop（Windows）
mise upgrade npm:@maybk/veri        # mise（或 mise upgrade github:maybk/veri）
```

### 常见问题

- **提示未配置可用检索源**：运行 `veri providers` 确认对应检索源处于已启用状态；若仅在当前终端执行过 `export ...`，新开终端不会保留，建议改用 `veri key set <provider>` 持久化。
- **单源报错、超时或触发限流**：运行 `veri doctor --live --yes` 定位具体源的鉴权或连通性问题；配置 2 个及以上检索源后，`veri` 会在故障时自动切换至下一可用源。
- **动态渲染页面（SPA / JS）或反爬页面抓取不全**：纯本地提取不运行浏览器脚本；配置支持动态渲染与抗反爬的云端提取源（`firecrawl`、`linkup`、`tavily`、`tinyfish`）后，默认策略会自动优先使用云端提取并在遇拦截页时自动升档。
- **代理与透明代理（TUN）环境**：自动遵循 `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY`，也可在 `~/.config/veri/config.toml` 的 `network.proxy` 显式配置；默认兼容常见透明代理的 TUN Fake-IP 网段。
- **标准退出码（便于脚本判断）**：`0` 成功 · `1` 内部错误或诊断未通过 · `2` 参数或配置错误 · `3` 密钥缺失/失效或配额不足 · `4` 限流/超时等临时故障 · `5` 目标不存在或被安全策略拦截 · `130` 用户中断。

## 进一步阅读

- [配置说明](./configuration.md)：自定义检索/提取路由链、中英文分链、代理、TOML 全量字段与安全机制。
- [检索与提取源说明](./providers.md)：适配器技术能力矩阵、4 种提取策略与 `-o key=value` 厂商扩展参数。
- [项目概览与目录结构](./overview.md)：整体架构与模块边界说明。
