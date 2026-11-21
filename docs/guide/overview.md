# 项目概览与目录结构

`veri` 是面向 AI Agent 与开发者的纯 Go 单二进制多源联网检索与网页 Markdown 提取工具，跨平台支持 macOS、Windows 与 Linux，同时提供命令行界面（CLI）、MCP 服务端（`veri mcp`）与 Agent Skill（`veri skill`）。

## 架构

```text
main.go -> internal/cli | internal/mcpserver -> internal/app -> internal/router -> internal/provider/* -> internal/httpx
```

| 模块 | 职责 | 边界约束 |
| --- | --- | --- |
| [`internal/cli`](../../internal/cli/README.md) | Cobra 命令行解析、双语终端词表、密钥管理与架构合规测试 | 薄适配层，不直接承载路由或网络请求 |
| [`internal/mcpserver`](../../internal/mcpserver/README.md) | 基于官方 MCP Go SDK 的 stdio 服务端（`web_search` / `web_fetch`） | 工具描述与结构化输出固定使用英文 |
| [`internal/app`](../../internal/app/README.md) | 显式 Provider 注册装配、进程内缓存、并发请求合并与 `doctor` 诊断 | 禁用包级 `init()` 隐式注册 |
| [`internal/update`](../../internal/update/README.md) | 官方 Release 版本检查、摘要校验、安装形态识别与跨平台原子自更新 | 网络经 `httpx`，元数据与摘要仅信任官方源 |
| [`internal/router`](../../internal/router/README.md) | 链式自动回退、并发 RRF 融合、网页提取选路与 404/空结果短路、质量闸门、过滤模拟与预算截断 | 仅导入内部包 `core` / `mdscan` / `spec` / `textnorm` / `urlnorm` |
| [`internal/provider`](../../internal/provider/README.md) | 各云端检索/提取源与本地/GitHub 网页提取适配器 | 互不导入，不感知外层路由与缓存 |
| [`internal/httpx`](../../internal/httpx/README.md) | 安全 HTTP 客户端、SSRF 拨号拦截、TUN Fake-IP 放行、并发闸门与计费安全重试 | 严禁使用 `http.DefaultClient` |
| [`internal/htmlmd`](../../internal/htmlmd/README.md) | HTML DOM 预处理（代码块、公式/MathML、隐藏节点清洗）与紧凑 GFM Markdown 渲染 | 仅依赖标准库、`golang.org/x/net/html` 与 `mdscan` |
| [`internal/keypool`](../../internal/keypool/README.md) | 同厂商多密钥轮询、按状态退避冷却与失效隔离 | 仅依赖 Go 标准库 |
| [`internal/urlnorm`](../../internal/urlnorm/README.md) | URL 规范化、追踪参数剥离与融合去重键生成 | 仅依赖 Go 标准库 |
| [`internal/textnorm`](../../internal/textnorm/README.md) | 文本归一化、隐形注入剥离、宽容时间解析与正文质量闸门 | 仅依赖标准库与 `mdscan` |
| [`internal/mdscan`](../../internal/mdscan/README.md) | Markdown 围栏代码块状态机与 ATX 标题扫描 | 仅依赖 Go 标准库 |
| [`internal/config`](../../internal/config/README.md) | `~/.config/veri/` 配置加载、TOML 严格校验与三级凭据合并 | 全仓仅在此包导入 `BurntSushi/toml` |
| [`internal/render`](../../internal/render/README.md) | Markdown / JSON / JSONL / URL 列表格式化渲染与不可信内容包裹 | 不依赖界面本地化词表 |
| [`internal/core`](../../internal/core/README.md) | 核心领域实体、输出信封、正交接口、微单位成本、时间窗与标准错误码 | 仅依赖标准库、`spec` 与 `textnorm` |
| [`internal/spec`](../../internal/spec/README.md) | 应用身份标识、内置 Provider 注册表、扩展选项键与跨包默认边界常量 | 仅依赖 Go 标准库 |
| [`clikit`](../../clikit/README.md) | 命令行通用基础组件（构建信息、存储、加密凭据、脱敏、多语言、终端与技能安装） | 严禁引用 `veri/internal/**` |

## 目录结构

```text
veri/
├── main.go                    # 单二进制程序入口
├── clikit/                    # 命令行基础组件库
│   ├── buildinfo/             # 构建与版本元数据解析
│   ├── storage/               # 配置目录解析与原子文件写入
│   ├── redact/                # 凭据遮蔽与脱敏日志器
│   ├── credentials/           # 本地主密钥保护与加密凭据库
│   ├── i18n/                  # 三端语言探测与多语言词表引擎
│   ├── term/                  # 终端样式、无回显输入、对齐表格与内容包裹
│   └── skill/                 # 标准 SKILL.md 导出与离线安装器
├── internal/
│   ├── spec/                  # 应用静态规格、Provider 注册表、扩展选项与默认边界
│   ├── mdscan/                # Markdown 围栏状态机与 ATX 标题扫描叶子包
│   ├── textnorm/              # 文本清洗、宽容时间解析与网页正文三级质量闸门
│   ├── core/                  # 领域实体、输出信封、正交接口、成本、时间窗与标准错误码
│   ├── urlnorm/               # URL 规范化与追踪参数清洗
│   ├── keypool/               # 多密钥轮询与冷却池
│   ├── htmlmd/                # HTML 预处理归一化与紧凑 GFM Markdown 渲染
│   ├── httpx/                 # SSRF 防护、Fake-IP 兼容与安全 HTTP 客户端
│   ├── config/                # TOML 配置校验与凭据合并
│   ├── provider/              # 检索与正文提取源适配器
│   │   ├── deepseek/          # DeepSeek Search & Fetch 适配器
│   │   ├── keenable/          # Keenable Search & Fetch 适配器
│   │   ├── zhihu/             # 知乎开放平台全网与站内搜索适配器
│   │   ├── exa/               # Exa Search & Contents 适配器
│   │   ├── tinyfish/          # TinySearch & TinyFetch 适配器
│   │   ├── tencentwsa/        # 腾讯云联网搜索 SearchPro 适配器
│   │   ├── tavily/            # Tavily Search & Extract 适配器
│   │   ├── brave/             # Brave Web Search & LLM Context 适配器
│   │   ├── linkup/            # Linkup Search & Fetch 适配器
│   │   ├── parallel/          # Parallel REST v1 Search & Extract 适配器
│   │   ├── firecrawl/         # Firecrawl v2 Scrape & Search 适配器
│   │   ├── github/            # GitHub REST API 结构化读取适配器
│   │   ├── local/             # 本地纯 Go 网页提取与正文质量闸门
│   │   └── providertest/      # 离线契约断言与手动联调测试套件
│   ├── router/                # 检索回退、RRF 融合、质量升档与字符预算裁剪
│   ├── app/                   # 依赖装配、进程内缓存与健康诊断
│   ├── update/                # 版本检查、摘要校验与跨平台自更新
│   ├── render/                # CLI / MCP 输出格式化与内容隔离包裹
│   ├── cli/                   # 命令行入口、双语词表与架构守卫测试
│   └── mcpserver/             # stdio MCP 服务端与输入/输出 Schema
├── skills/
│   └── veri/                  # 内嵌与归档分发的 SKILL.md
├── assets/                    # README 终端演示动画
├── build/                     # 应用元信息配置与跨平台打包任务
│   ├── darwin/                # macOS 独立构建与打包任务
│   ├── linux/                 # Linux 独立构建与打包任务
│   └── windows/               # Windows 独立构建与打包任务
├── scripts/                   # 版本提取、发版准备、Release 说明与包清单脚本
├── .github/                   # PR 模板、Dependabot 与 CI / Release / Rolling 工作流
├── docs/guide/                # 稳定项目指南
├── CHANGELOG.md               # 版本更新日志
└── Taskfile.yml               # 根级开发与打包任务入口
```
