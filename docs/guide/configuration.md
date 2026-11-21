# 配置说明

## 本地配置目录与优先级

`veri` 在 macOS、Windows 与 Linux 下统一将配置、加密凭据与主密钥保存在 `~/.config/veri/`（Windows 为 `%USERPROFILE%\.config\veri\`），CLI 与 `veri mcp` 自动共享：

```text
~/.config/veri/
├── config.toml                # 可选：检索链、超时、策略与扩展参数（支持 env: 引用或明文）
├── secrets.json               # veri key set 生成的本地加密凭据库（权限 0600）
└── secret.key                 # 本机生成的本地加密主密钥（自动按系统安全权限保护）
```

多层配置与凭据按以下优先级从高到低生效：

1. **命令行标志 / MCP 请求参数**（如 `-p`、`--strategy`、`--timeout`、`--ui-lang`）
2. **`VERI_*` 行为环境变量**（如 `VERI_PROVIDERS`、`VERI_FETCH_STRATEGY`、`VERI_LANG`）
3. **厂商密钥环境变量**（如 `DEEPSEEK_API_KEY`、`EXA_API_KEY`；含 MCP 客户端 `env` 注入项，始终优先于本地文件）
4. **本地加密凭据库**（`~/.config/veri/secrets.json`，由 `veri key set` 写入）
5. **TOML 配置文件**（`~/.config/veri/config.toml` 中的 `env:` 引用或明文 `api_key`）
6. **内置默认值**

未显式指定 `--config` 时，按顺序取首个非空路径：

| 场景 | 查找顺序 |
| --- | --- |
| 显式指定 | `--config <path>` 或环境变量 `VERI_CONFIG` |
| XDG 覆盖 | `XDG_CONFIG_HOME/veri/config.toml`（显式设置了 `XDG_CONFIG_HOME` 时） |
| 默认路径（macOS / Windows / Linux） | `~/.config/veri/config.toml` |

常用配置与密钥管理命令：

```bash
veri key set deepseek                          # 交互无回显输入并加密保存密钥
printf '%s' "$EXA_API_KEY" | veri key set exa  # 通过管道从标准输入读取并加密保存密钥
veri key list                                  # 查看各源生效凭据来源、加密库状态与脱敏掩码
veri key unset deepseek                        # 移除本地加密密钥库中指定源的密钥
veri config init                               # 按当前界面语言生成带注释的 config.toml 模板（权限 0600，覆盖加 --force）
veri config path                               # 输出当前生效的配置文件路径
veri config show                               # 输出合并并脱敏后的生效配置
```

`config.toml` 启用严格键名校验，遇到拼写错误或未知字段时立即返回 `invalid_argument` 并指出具体键名。

## 核心配置字段

### 1. 界面、检索与提取策略

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `ui.lang` | `"auto"` | 终端界面语言：`auto`（跟随系统，非中文环境回退英文）、`zh`、`en`（兼容 `zh_CN.UTF-8` 等标准 Locale） |
| `search.providers` | `["deepseek", "exa", "zhihu", "tinyfish", "tencent_wsa", "tavily", "brave", "linkup", "parallel", "keenable", "firecrawl"]` | 默认检索优先级链（仅已配置密钥的源参与路由） |
| `search.providers_en` | `[]` | 可选；查询词不含中日韩字符时优先使用的英文检索链 |
| `search.fusion_providers` | `[]` | 可选；`--fusion` 模式默认并发检索源集合（如 `["deepseek", "exa", "tavily"]`；为空时取前 3 个已启用源） |
| `search.strategy` | `"auto"` | `auto`（链式回退）或 `fusion`（多源并发 RRF 融合） |
| `search.count` | `8` | 默认返回条数，范围 `1`–`50` |
| `search.detail` | `"summary"` | 结果详略级别：`snippet`、`summary`、`content` |
| `search.timeout` | `"20s"` | 单次检索总超时 |
| `search.fallback_on_empty` | `true` | 当前检索源无结果时自动回退至下一源（连续 2 个原生过滤源无结果时提前终止） |
| `search.strict_filters` | `false` | `true` 时仅选用原生支持当前过滤条件的检索源，禁止本地后过滤模拟 |
| `search.fusion_k` | `60` | RRF（Reciprocal Rank Fusion）排名平滑常数 |
| `search.summary_max_runes` | `800` | 单条结果 `summary` 最大字符数 |
| `search.content_max_runes` | `0` | 单条结果 `content` 最大字符数（`0` 表示不限制） |
| `search.max_chars` | `0` | CLI 检索整包字符预算上限（`0` 表示不限制） |
| `fetch.strategy` | `"api_first"` | 提取路由策略：`api_first`（云端优先、本地回退）、`smart_local`、`local_only`、`api_only` |
| `fetch.providers` | `["deepseek", "tinyfish", "tavily", "exa", "linkup", "parallel", "keenable", "firecrawl"]` | 默认云端网页提取链（中文站点优先使用；显式配置且未配置 `providers_global` 时统一按此顺序执行） |
| `fetch.providers_global` | `["exa", "tavily", "linkup", "parallel", "tinyfish", "keenable", "firecrawl", "deepseek"]` | 非中文站点优先使用的云端提取链（设为空数组 `[]` 可关闭分链） |
| `fetch.timeout` | `"45s"` | 单次网页提取总超时（作为外层上限覆盖各源 `fetch_timeout`） |
| `fetch.max_bytes` | `"5MB"` | 本地抓取解压后响应体上限 |
| `fetch.min_content_runes` | `300` | 本地提取正文质量闸门最少字符阈值 |
| `fetch.respect_robots` | `false` | 本地抓取前是否检查并遵循目标站点 `robots.txt` |
| `fetch.user_agent` | `"browser"` | 本地抓取 User-Agent：`browser`、`veri` 或自定义字符串 |
| `fetch.max_chars` | `0` | CLI 网页提取默认返回字符上限（`0` 表示返回全文） |

### 2. MCP 服务端、网络与单源参数

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `mcp.text_format` | `"markdown"` | MCP 文本块格式：`markdown` 或 `json` |
| `mcp.search_max_chars` | `24000` | `web_search` 默认字符预算上限 |
| `mcp.fetch_max_chars` | `30000` | `web_fetch` 默认字符预算上限 |
| `mcp.content_max_runes` | `3000` | MCP 检索单条结果 `content` 最大字符数 |
| `mcp.max_count` | `20` | `web_search` 单次最大返回条数 |
| `mcp.cache_ttl` | `"5m"` | MCP 进程内相同请求缓存有效期（设为 `"0s"` 关闭） |
| `mcp.cache_size` | `256` | MCP 进程内 LRU 缓存最大条目数（设为 `0` 关闭） |
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
| `VERI_LOG_LEVEL` | — | 诊断日志级别：`debug` / `info` / `warn`（默认）/ `error` |
| `VERI_LOG_FORMAT` | — | 诊断日志格式：`text`（默认）/ `json` |
| `VERI_UPDATE_MIRROR` | — | `veri update` 与安装脚本的 HTTPS 归档下载镜像前缀（可选） |
| `NO_COLOR` | — | 非空时禁用终端 ANSI 颜色输出 |

## 安全与可靠性机制

1. **SSRF 防护、内网隔离与 Fake-IP 兼容**：本地与 GitHub 抓取在拨号阶段校验目标 IP 并逐跳复验重定向，默认拦截回环、私网、链路本地、云元数据（`169.254.169.254`）与 CGNAT 地址；仅放行经域名解析返回且命中 `network.fake_ip_cidrs` 的 TUN 虚拟地址，URL 字面量直连 Fake-IP 仍被拦截。回环、私网与解析至内网的目标仅在本地处理，不向外部云端提取源转发。API 客户端同步阻断 HTTPS 向 HTTP 的协议降级与向内网回环的跨源重定向。
2. **计费安全重试与冷却轮换**：请求发出后若发生读超时或连接中断，客户端禁止自动重放以防重复扣费；仅对明确未扣费的 `408 / 429 / 500 / 502 / 503 / 504` 或池内备用密钥最多重试 1 次，并按限流、欠费、鉴权失败分级冷却隔离。
3. **全链路凭据脱敏与权限审计**：`veri config show`、错误提示与诊断日志自动遮蔽已知密钥、认证头、代理密码与敏感查询参数；Unix 下若配置文件含明文密钥或凭据库权限宽于 `0600`，启动与 `veri doctor` 均会告警。
4. **外部网页内容隔离与隐形注入清洗**：MCP 文本输出默认包裹在带单次随机边界标识的 `<untrusted_web_content id="...">` 标签内并转义内嵌同名标签以防伪造提前闭合；同时剥离不可见格式控制符/变体选择符与零可见度 CSS 隐藏节点，降低间接提示注入风险。
