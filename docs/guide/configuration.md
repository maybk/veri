# 配置说明

## 本地配置目录与优先级

`veri` 在 macOS、Windows 与 Linux 下统一将配置、加密凭据与主密钥保存在 `~/.config/veri/`（Windows 为 `%USERPROFILE%\.config\veri\`），CLI 与 `veri mcp` 自动共享：

```text
~/.config/veri/
├── config.toml                # 可选：仅存放自定义配置项（未设置项随版本自动更新）
├── secrets.json               # veri key 保存的本地加密凭据库（权限 0600）
└── secret.key                 # 本机生成的本地加密主密钥（自动按系统安全权限保护）
```

多层配置与凭据按以下优先级从高到低生效：

1. **命令行标志 / MCP 请求参数**（如 `-p`、`--strategy`、`--timeout`、`--ui-lang`）
2. **`VERI_*` 行为环境变量**（如 `VERI_PROVIDERS`、`VERI_FETCH_STRATEGY`、`VERI_LANG`）
3. **厂商密钥环境变量**（如 `DEEPSEEK_API_KEY`、`EXA_API_KEY`；含 MCP 客户端 `env` 注入项，始终优先于本地文件）
4. **本地加密凭据库**（`~/.config/veri/secrets.json`，由 `veri key` 写入）
5. **TOML 配置文件**（`~/.config/veri/config.toml` 中的 `env:` 引用或明文 `api_key`）
6. **内置默认值**

未显式指定 `--config` 时，按顺序取首个非空路径：

| 场景 | 查找顺序 |
| --- | --- |
| 显式指定 | `--config <path>` 或环境变量 `VERI_CONFIG` |
| XDG 覆盖 | `XDG_CONFIG_HOME/veri/config.toml`（显式设置了 `XDG_CONFIG_HOME` 时） |
| 默认路径（macOS / Windows / Linux） | `~/.config/veri/config.toml` |

CLI 磁盘缓存三端统一存放在 `~/.cache/veri/`（Windows 为 `%USERPROFILE%\.cache\veri\`），优先级：`VERI_CACHE_DIR` > `cache.dir` > `XDG_CACHE_HOME/veri` > `~/.cache/veri`；可用 `veri cache info` / `veri cache clear` 查看或清理。

常用配置与密钥管理命令：

```bash
# 凭据管理（无需 config.toml）
veri key set deepseek                          # 交互隐藏输入并覆盖保存密钥（多把用逗号分隔）
veri key set deepseek -a                       # 追加新密钥并自动去重（不覆盖原密钥）
printf '%s' "$EXA_API_KEY" | veri key set exa  # 从标准输入保存密钥
veri key import --from-config                  # 加密迁入 config.toml 明文密钥并清理明文
veri key import .env                           # 从 .env 批量加密导入（`-` 表示标准输入）
veri key list                                  # 查看各源凭据来源与加密状态
veri key unset deepseek                        # 移除本地加密密钥

# 单项配置（支持 Tab 补全，未设置项随版本自动更新）
veri config set providers.zhihu_web.search_db realtime        # 修改单个配置项
veri config set search.providers deepseek,zhihu_web,keenable  # 列表支持逗号分隔或 TOML 数组
veri config set search.providers_global ""                    # 设为空数组以关闭非中文分链
veri config get providers.zhihu_web.search_db                 # 查看当前生效值（加 --json 含来源）
veri config unset providers.zhihu_web.search_db               # 移除自定义项并恢复默认值

# 查看与参考模板
veri config show --overrides                   # 仅查看已自定义的配置项（支持 --json）
veri config show                               # 查看合并并脱敏后的生效配置（支持 --json）
veri config init                               # 生成全注释参考模板（权限 0600，覆盖加 --force）
veri config path                               # 输出生效的配置文件路径
```

`config.toml` 启用严格键名与值校验；`veri config set` 禁止向 `api_key` / `token` 写入明文（仅允许 `env:VAR_NAME`），密钥请通过 `veri key set <provider>` 或 `veri key import` 加密保存。`veri key import --from-config` 导入后会自动清理明文（模板注释段还原为默认注释，稀疏明文行直接移除），保留其他配置与 `env:` 引用，并支持 `--dry-run` 预览、`--overwrite` 覆盖冲突与 `--keep-config` 保留原文件。

## 核心配置字段

### 1. 界面、检索与提取策略

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `ui.lang` | `"auto"` | 终端界面语言：`auto`（跟随系统，非中文环境回退英文）、`zh`、`en`（兼容 `zh_CN.UTF-8` 等标准 Locale） |
| `search.providers` | `["deepseek", "keenable", "zhihu", "tencent_wsa", "zhihu_web", "brave", "parallel", "exa", "tavily", "linkup", "tinyfish", "firecrawl"]` | 默认检索链（中文优先；单源 `auto` 通用搜优先全网、`-s` 命中单站源时自动置顶；显式配置且未设 `providers_global` 时所有语言统一走此链） |
| `search.providers_global` | `["keenable", "parallel", "exa", "brave", "linkup", "tavily", "deepseek", "tinyfish", "firecrawl", "tencent_wsa", "zhihu_web", "zhihu"]` | 非中文查询检索链（英文、日韩等或指定非中文 `--lang` 时启用；设 `[]` 关闭） |
| `search.fusion_providers` | `[]` | `--fusion` 并发源列表（为空按 `search.providers` 自动选最多 3 家独立源；仅配此项时所有语言统一使用） |
| `search.fusion_providers_global` | `[]` | `--fusion` 非中文并发源列表（为空按 `search.providers_global` 自动选最多 3 家独立全网源；设 `[]` 关闭分池） |
| `search.strategy` | `"auto"` | `auto`（链式回退）或 `fusion`（多源并发 RRF 融合） |
| `search.count` | `8` | 默认返回条数，范围 `1`–`50` |
| `search.detail` | `"summary"` | 结果详略级别：`snippet`、`summary`、`content` |
| `search.timeout` | `"20s"` | 单次检索总超时 |
| `search.fallback_on_empty` | `true` | 当前检索源无结果时自动回退至下一源（连续 2 个原生过滤源无结果时提前终止） |
| `search.strict_filters` | `false` | `true` 时仅选用原生支持当前过滤条件的检索源，禁止本地后过滤模拟 |
| `search.fusion_k` | `60` | RRF（Reciprocal Rank Fusion）排名平滑常数 |
| `search.summary_max_chars` | `800` | 单条结果 `summary` 最大字符数 |
| `search.content_max_chars` | `0` | 单条结果 `content` 最大字符数（`0` 表示不限制） |
| `search.max_chars` | `0` | CLI 检索整包字符预算上限（`0` 表示不限制） |
| `fetch.strategy` | `"api_first"` | 提取路由策略：`api_first`（云端优先、本地回退）、`smart_local`、`local_only`、`api_only` |
| `fetch.providers` | `["keenable", "tavily", "firecrawl", "parallel", "exa", "linkup", "deepseek", "tinyfish"]` | 默认云端提取链（中文站优先；显式配置且未设 `providers_global` 时所有站点统一走此链） |
| `fetch.providers_global` | `["keenable", "tavily", "parallel", "exa", "linkup", "firecrawl", "tinyfish", "deepseek"]` | 非中文站云端提取链（设 `[]` 关闭分链） |
| `fetch.timeout` | `"45s"` | 单次网页提取总超时（作为外层上限覆盖各源 `fetch_timeout`） |
| `fetch.max_bytes` | `"5MB"` | 本地抓取解压后响应体上限 |
| `fetch.min_content_chars` | `300` | 本地提取正文质量闸门最少字符阈值 |
| `fetch.respect_robots` | `false` | 本地抓取前是否检查并遵循目标站点 `robots.txt` |
| `fetch.user_agent` | `"browser"` | 本地抓取 User-Agent：`browser`、`veri` 或自定义字符串 |
| `fetch.max_chars` | `0` | CLI 网页提取默认返回字符上限（`0` 表示返回全文） |

### 2. 缓存配置

缓存统一归口 `[cache]`：CLI 使用本地磁盘缓存，MCP 使用进程内内存缓存（退出即释放）；`[cache.cli]` 与 `[cache.mcp]` 未设置项自动继承 `[cache]`，显式设为 `"0s"` 或 `false` 仅关闭对应一端。

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `cache.enabled` | `true` | 缓存总开关（`false` 同时关闭 CLI 与 MCP） |
| `cache.dir` | `""` | 磁盘缓存目录（留空使用 `~/.cache/veri`） |
| `cache.fetch_ttl` | `"15m"` | 默认网页正文缓存有效期（CLI 与 MCP 共享） |
| `cache.search_ttl` | `"0s"` | 默认检索缓存有效期（CLI 默认关闭以保持实时） |
| `cache.max_bytes` | `"32MB"` | 磁盘缓存体积上限（超限按最近访问淘汰） |
| `cache.cli.enabled` | 继承 `cache.enabled` | 可选覆写：仅开关 CLI 磁盘缓存 |
| `cache.cli.fetch_ttl` | 继承 `cache.fetch_ttl` | 可选覆写：设为 `"0s"` 仅关闭 CLI 抓取缓存 |
| `cache.cli.search_ttl` | 继承 `cache.search_ttl` | 可选覆写：如设 `"2m"` 开启 CLI 检索缓存 |
| `cache.mcp.enabled` | 继承 `cache.enabled` | 可选覆写：仅开关 MCP 内存缓存 |
| `cache.mcp.fetch_ttl` | 继承 `cache.fetch_ttl` | 可选覆写：设为 `"0s"` 仅关闭 MCP 抓取缓存 |
| `cache.mcp.search_ttl` | `"5m"` | MCP 进程内检索缓存有效期（与 `fetch_ttl` 独立） |
| `cache.mcp.max_entries` | `256` | MCP 内存缓存最大条目数（`0` 关闭） |

### 3. MCP 服务端、网络与单源参数

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `mcp.text_format` | `"markdown"` | MCP 文本块格式：`markdown` 或 `json` |
| `mcp.search_max_chars` | `24000` | `web_search` 默认字符预算上限 |
| `mcp.fetch_max_chars` | `30000` | `web_fetch` 默认字符预算上限 |
| `mcp.content_max_chars` | `3000` | MCP 检索单条结果 `content` 最大字符数 |
| `mcp.max_count` | `20` | `web_search` 单次最大返回条数 |
| `network.proxy` | `""` | 显式 HTTP/SOCKS5 代理地址；为空时遵循 `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY` |
| `network.fake_ip_cidrs` | `["198.18.0.0/15"]` | 透明代理 TUN Fake-IP 网段（仅放行域名解析返回的该网段地址） |
| `network.allow_private` | `false` | 是否允许本地提取器访问回环或内网地址（默认拦截以防御 SSRF） |
| `providers.<name>.enabled` | `true` | 是否启用该 Provider（设为 `false` 可单独禁用任意检索或提取源） |
| `providers.<name>.api_key` | `"env:..."` | 支持 `env:VAR` 引用或明文密钥，多密钥用逗号分隔以启用轮询与分级冷却 |
| `providers.<name>.timeout` / `fetch_timeout` | 各源默认 | 单次上游搜索/提取超时（搜索默认 `10s`；提取 `deepseek` 默认 `10s`、`firecrawl` 默认 `45s`，其余云端提取源默认 `25s`） |
| `providers.<name>.concurrency` | `4`（`firecrawl` 为 `2`） | 进程内对该上游的最大并发请求数 |
| `providers.<name>.qps` | `0` | 可选进程内速率限制（`0` 表示不限速） |
| `providers.<name>.price_*` | `""` | 可选成本估算单价（如 `USD:0.005`、`CNY:0.005`）；未配置且上游未返回账单时不展示费用 |

## 环境变量参考

| 变量名 | 别名 | 作用 |
| --- | --- | --- |
| `DEEPSEEK_API_KEY` | — | DeepSeek Search / Fetch API 密钥 |
| `KEENABLE_API_KEY` | — | Keenable Search / Fetch API 密钥 |
| `ZHIHU_API_KEY` | `ZHIHU_ACCESS_SECRET` | 知乎开放平台全网 / 站内搜索 Access Secret |
| `EXA_API_KEY` | — | Exa Search / Contents API 密钥 |
| `TINYFISH_API_KEY` | — | TinyFish Search / Fetch API 密钥 |
| `TENCENT_WSA_API_KEY` | `TENCENTCLOUD_WSA_APIKEY` | 腾讯云联网搜索 API 密钥 |
| `TAVILY_API_KEY` | — | Tavily Search / Extract API 密钥 |
| `BRAVE_API_KEY` | `BRAVE_SEARCH_API_KEY` | Brave Search API 密钥 |
| `LINKUP_API_KEY` | — | Linkup Search / Fetch API 密钥 |
| `PARALLEL_API_KEY` | — | Parallel Search / Extract REST API 密钥 |
| `FIRECRAWL_API_KEY` | — | Firecrawl v2 Scrape / Search API 密钥（非官方自托管 `base_url` 可省略） |
| `GITHUB_TOKEN` | `GH_TOKEN` | GitHub REST API 访问令牌（可选，用于提高匿名配额） |
| `VERI_CONFIG` | — | 覆盖配置文件路径 |
| `VERI_LANG` | — | 覆盖终端界面语言（`auto` / `zh` / `en`，兼容标准 Locale） |
| `VERI_PROVIDERS` | — | 覆盖 `search.providers`（逗号分隔） |
| `VERI_FETCH_PROVIDERS` | — | 覆盖 `fetch.providers`（逗号分隔） |
| `VERI_FETCH_STRATEGY` | — | 覆盖 `fetch.strategy` |
| `VERI_TIMEOUT` | — | 覆盖 `search.timeout` |
| `VERI_CACHE_DIR` | — | 覆盖磁盘缓存目录（优先于 `cache.dir`） |
| `VERI_CACHE_FETCH_TTL` | — | 同时覆盖 CLI 与 MCP 的网页抓取缓存有效期（支持 `"0s"` 关闭） |
| `VERI_CACHE_SEARCH_TTL` | — | 同时覆盖 CLI 与 MCP 的检索缓存有效期（如 `"2m"` 临时开启 CLI 搜索缓存） |
| `VERI_NO_CACHE` | — | 设为 `1` / `true` 时禁用本次进程全部缓存 |
| `VERI_LOG_LEVEL` | — | 诊断日志级别：`debug` / `info` / `warn`（默认）/ `error` |
| `VERI_LOG_FORMAT` | — | 诊断日志格式：`text`（默认）/ `json` |
| `VERI_UPDATE_MIRROR` | — | `veri update` 与安装脚本的 HTTPS 归档下载镜像前缀（可选） |
| `NO_COLOR` | — | 非空时禁用终端 ANSI 颜色输出 |

## 安全与可靠性机制

1. **SSRF 防护、内网隔离与 Fake-IP 兼容**：本地与 GitHub 抓取在拨号阶段校验目标 IP 并逐跳复验重定向，默认拦截回环、私网、链路本地、云元数据（`169.254.169.254`）与 CGNAT 地址；仅放行经域名解析返回且命中 `network.fake_ip_cidrs` 的 TUN 虚拟地址，URL 字面量直连 Fake-IP 仍被拦截。回环、私网与解析至内网的目标仅在本地处理，不向外部云端提取源转发。API 客户端同步阻断 HTTPS 向 HTTP 的协议降级与向内网回环的跨源重定向。
2. **计费安全重试与冷却轮换**：请求发出后若发生读超时或连接中断，客户端禁止自动重放以防重复扣费；仅对明确未扣费的 `408 / 429 / 500 / 502 / 503 / 504` 或池内备用密钥最多重试 1 次，并按限流、欠费、鉴权失败分级冷却隔离。
3. **全链路凭据脱敏与权限审计**：`veri config show`（含 `--overrides`）、`veri config get`、错误提示与诊断日志自动遮蔽已知密钥、认证头、代理密码与敏感查询参数；`veri config set` 拒绝写入明文 `api_key` / `token`；Unix 下若手工写入的配置文件含明文密钥或凭据库权限宽于 `0600`，启动与 `veri doctor` 均会告警。
4. **外部网页内容隔离与隐形注入清洗**：MCP 文本输出默认包裹在带单次随机边界标识的 `<untrusted_web_content id="...">` 标签内并转义内嵌同名标签以防伪造提前闭合；同时剥离不可见格式控制符/变体选择符与零可见度 CSS 隐藏节点，降低间接提示注入风险。
5. **缓存安全边界**：磁盘缓存按 `0700`/`0600` 原子写入且以哈希命名；拒绝缓存内网/回环目标、低质保底页、不完整融合结果与 `Cache-Control: no-store` 响应；GitHub 抓取键按活跃令牌指纹隔离；`veri cache clear` 仅清理受管缓存文件。
