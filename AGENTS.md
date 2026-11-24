# veri

`veri` 是面向 AI Agent 和开发者的纯 Go 单二进制多源联网检索与网页 Markdown 提取工具，跨平台支持 macOS、Windows 与 Linux，提供命令行界面（CLI）、MCP 服务端（`veri mcp`）与 Agent Skill（`veri skill`）。

## 架构与模块边界

依赖方向固定为：

- 命令行与 MCP 进程：`main.go -> internal/cli | internal/mcpserver -> internal/app -> internal/router -> internal/provider/* -> internal/httpx`；自更新链路：`internal/cli -> internal/update -> internal/httpx`
- 配置与渲染：`internal/config -> internal/core | internal/spec | clikit/*`；`internal/render -> internal/core | internal/spec | internal/textnorm | clikit/term`
- 独立基础底座：`clikit/**` **严禁**引用 `veri/internal/**`，可随时独立拆出为单独仓库；`build/` 与 `skills/` 不放置 `.go` 源码文件。

各模块职责：

- `clikit`：纯 Go 命令行基础组件，涵盖退出码与信号上下文、构建元信息解析、配置目录与原子写入、凭据加密存储、日志与文本脱敏、三端系统语言探测、终端交互及技能文件安装。
- `internal/spec`：全局静态规格、应用身份标识、内置 Provider 注册表、跨包扩展选项键与共享默认边界常量。
- `internal/mdscan`：零依赖标准库叶子包，提供 Markdown 围栏代码块状态机与 ATX 标题解析。
- `internal/textnorm`：文本归一化、隐形注入字符剥离、宽容日期时间解析与网页正文三级质量闸门。
- `internal/core`：检索与提取核心领域实体、统一输出信封、正交接口（`Searcher` / `Fetcher` / `Answerer`）、微单位成本、时间窗与标准错误码。
- `internal/urlnorm` 与 `internal/keypool`：URL 规范化去重键生成，以及同厂商多 Key 轮询、分级退避冷却与失效隔离。
- `internal/htmlmd`：纯 Go HTML DOM 预处理（代码块、数学公式、隐藏节点清洗）与紧凑 GFM Markdown 渲染。
- `internal/httpx`：安全 HTTP 客户端，负责 SSRF 拨号拦截、TUN Fake-IP 域名解析放行、逐跳重定向复验、解压体积限制、并发闸门与计费安全重试。
- `internal/config`：`~/.config/veri/` 配置加载、TOML 未知键校验，以及环境变量、加密凭据库与配置文件合并。
- `internal/provider/<name>`：单一检索或提取源适配器，只负责单次请求组装、响应映射与错误分类，互不导入。
- `internal/router`：链式自动回退、语种分链选路、并发 RRF 融合、本地提取质量闸门升档、过滤条件后置模拟与字符预算裁剪。
- `internal/app`：显式 Provider 注册装配、CLI 磁盘缓存、MCP 进程内内存缓存、并发相同请求合并与 `doctor` 健康诊断。
- `internal/update`：官方 GitHub Releases 版本检查、SHA-256 完整性校验、包管理器托管识别与跨平台原子自更新。
- `internal/render`：Markdown、JSON、JSONL、URL 列表格式化渲染与外部网页不可信内容标签包裹。
- `internal/cli` 与 `internal/mcpserver`：Cobra 命令行入口（含双语词表 `locales/`）与基于官方 Go SDK 的 stdio MCP 服务端。

## 常用命令

在项目根目录通过 `task` 统一编排执行：

```bash
task fmt                # 格式化全部 Go 源码
task lint               # 运行 go vet、golangci-lint 与 govulncheck
task test               # 运行全量单元测试与竞态检测
task check              # 运行完整质量门禁（格式、扫描、竞态测试与发布契约检查）
task build              # 编译当前宿主机架构二进制至 bin/veri
task run -- <args>      # 编译并运行当前宿主机架构 CLI 子命令
task package:all        # 交叉编译打包三端六架构归档与 SHA256SUMS.txt
```

## 修改与安全规则

1. 未经明确要求，不执行 `git add`、`git commit`、`git push`、切换分支或改写 Git 历史；保留现有改动，不覆盖无关代码。
2. 配置与加密凭据统一存放在 `~/.config/veri/`（`config.toml`、`secrets.json`、`secret.key`），环境变量最先优先生效；禁止在源码、样本、日志、错误或标准输出中写入真实密钥。
3. 所有外部请求经由 `internal/httpx`，禁用 `http.DefaultClient`；本地抓取默认拦截私网与回环地址，仅放行域名解析返回的 Fake-IP 网段。
4. 读超时或连接中断未收响应时严禁自动重放；仅对明确未扣费的 `408 / 429 / 500 / 502 / 503 / 504` 或切换池内其他 Key 最多重试 1 次。
5. 不硬编码上游刊例单价或配额；仅在上游返回实际计费字段或配置 `price_*` 时记录并展示 `Cost`。
6. 应用版本号仅在 `build/config.yml` 的 `info.version` 维护；跨包共享常量归口 `internal/spec/`，包私有常量置于本包 `defaults.go`，禁止跨包重复硬编码。
7. 三端（macOS / Windows / Linux）保持对等完整实现。
8. 终端文案统一维护在 `internal/cli/locales/{zh-CN,en-US}.json`（键与占位符对称）；机器协议与 `SKILL.md` 固定英文。
9. 全仓 `.json` 遵循标准 RFC 8259，禁止注释。
10. 仅对根目录 `*.go` 及 `clikit/`、`internal/` 下手写 `.go` 设行数上限：源码 `<= 500` 行，`*_test.go` `<= 800` 行（豁免 `// Code generated ... DO NOT EDIT.` 与 `testdata/`）。超限时同包按职责拆分，测试按场景拆分、大夹具入 `testdata/`；禁止拆包、加无意义 `interface`、删注释、压行或泛化/序号命名（如 `extra_test.go`、`*_split.go`、`*_2.go`）。
11. 修复缺陷先补复现测试，完成后通过 `task check`（局部快速回归用 `go test -race ./...`）。

## 文档维护

- `README.md` 与 `README.en.md` 只保留入口、安装、特性、快速开始、架构与文档链接；稳定指南统一放在 `docs/guide/`，排错与阶段性方案不入长期文档。
- 目录 `README.md` 遵循单层局部索引（仅列非测试直接文件与直接子目录，不跨层展开）；核心二级目录说明职责、结构与规范，专属验证命令按需补充。
- 修改配置、目录职责、安全边界、命令或构建产物时，同步更新 `docs/guide/*` 与对应目录 `README.md`。
- 用户可见的新增、变更或修复同步记入 `CHANGELOG.md` 顶部待发布段（`## [X.Y.Z] - 未发布`，只记相对上一正式版的净变化）；未明确要求发版时，不改正式版本、不打标签或发 Release。
