# 开发与构建

## 环境要求

| 组件 | 要求 |
| --- | --- |
| Go | `>= 1.27.4`，全程启用 `CGO_ENABLED=0` |
| Task（可选） | `go-task` v3，用于执行 `Taskfile.yml` 开发、检查与跨平台打包任务 |
| 静态与漏洞扫描（可选） | `golangci-lint` v2、`govulncheck`（未预装时 `task lint` / `task check` 自动通过 `go run` 按需执行） |
| Node.js（可选） | 仅在执行 `task dev` 启动 `@modelcontextprotocol/inspector` 调试面板时使用 |
| 操作系统 | macOS、Windows `10/11` 或 Linux |

## 本地开发

通过 `task` 编译并运行当前宿主机架构的 CLI 或启动 MCP 交互调试面板：

```bash
task build                      # 编译当前宿主机架构二进制至 bin/veri
task run -- search "golang"     # 编译并运行指定子命令
task dev                        # 编译并拉起 MCP Inspector 交互调试面板
```

`task run` 与 `task dev` 始终锁定当前宿主机系统与架构输出至 `bin/veri`（Windows 为 `bin/veri.exe`），不受外部 `GOOS` / `GOARCH` 环境变量影响。

未安装 `task` 时，也可直接使用 `go build` 编译当前宿主机二进制：

```bash
# macOS / Linux
CGO_ENABLED=0 go build -trimpath -o bin/veri .

# Windows (PowerShell)
$env:CGO_ENABLED="0"; go build -trimpath -o bin/veri.exe .
```

## 格式、分析与测试

日常格式化、静态分析、漏洞扫描与全量门禁统一通过 `task` 执行：

```bash
task fmt                        # 格式化全部 Go 源码
task lint                       # 运行 go vet、golangci-lint 与 govulncheck
task test                       # 运行全量单元测试与竞态检测
task test:cover                 # 运行测试并输出各包覆盖率报告
task check                      # 运行完整质量门禁（格式、扫描、竞态测试、发布契约测试与空白检查）
```

按需执行真实 API 联调测试（读取本机环境变量中的密钥，不进入常规单元测试与 CI）：

```bash
task test:live                  # 执行已配置密钥的 Provider 联调测试
task test:live RUN=Exa          # 按名称过滤执行指定联调测试
```

## 构建与跨平台打包

构建当前宿主机架构的静态二进制或发布归档：

```bash
task build
task package
```

一键交叉编译并打包三端六架构全部归档与 `SHA256SUMS.txt` 至 `dist/`：

```bash
task package:all
```

### 按架构独立构建与打包（macOS / Windows / Linux）

`veri` 全程启用 `CGO_ENABLED=0`，可在任意宿主机系统上直接交叉编译与打包各目标平台架构产物（独立二进制输出至 `bin/veri-<os>-<arch>`，发布归档输出至 `dist/`）：

- **macOS（支持 `arm64` 与 `amd64` 独立静态二进制及 `.tar.gz` 归档）**：
  ```bash
  task darwin:build:arm64
  task darwin:build:amd64
  task darwin:package:arm64
  task darwin:package:amd64
  ```

- **Windows（支持 `amd64` 与 `arm64` 独立可执行文件及 `.zip` 归档）**：
  ```bash
  task windows:build:amd64
  task windows:build:arm64
  task windows:package:amd64
  task windows:package:arm64
  ```

- **Linux（支持 `amd64` 与 `arm64` 独立静态二进制及 `.tar.gz` 归档）**：
  ```bash
  task linux:build:amd64
  task linux:build:arm64
  task linux:package:amd64
  task linux:package:arm64
  ```

## 代码规范

1. **单一版本源与分层静态规格**：应用版本号维护在 `build/config.yml` 的 `info.version` 中；跨包共享的应用标识、Provider 注册表、扩展选项键与默认预算按职责维护在 `internal/spec/{app,registry,options,defaults}.go`，模块私有调优常量下沉至各包 `defaults.go`。
2. **模块分层与边界隔离**：严格遵守 `AGENTS.md` 定义的导入方向；禁用包级 `init()` 隐式注册，所有适配器在 `internal/app/registry.go` 显式构造；单个 Go 源码文件（`.go`）不超过 500 行。
3. **测试与平台文件命名**：测试文件按职责命名（如 `gate_test.go`、`goggles_test.go`、`cooldown_test.go`），不使用 `extra_test.go`；平台差异实现采用 `<role>_darwin.go`、`<role>_linux.go`、`<role>_windows.go` 与 `<role>_other.go` 命名。
4. **新增 Provider 规范**：在 `internal/provider/<name>/` 下按统一结构提供 `doc.go`、`defaults.go`、`provider.go`、`wire.go`、`errors.go`、`search.go` / `fetch.go`、`provider_test.go`、`README.md`，在 `testdata/` 下提供至少 3 份合法 RFC 8259 JSON 脱敏样本及 `testdata/README.md`，并在 `internal/spec/registry.go`、`internal/config` 与 `internal/app/registry.go` 完成注册。
5. **双轨语言契约**：人类终端文案与命令用法示例统一维护在 `internal/cli/locales/{zh-CN,en-US}.json` 并保持键与占位符完全对称；MCP 工具描述、`--json` 字段与 `skills/veri/SKILL.md` 固定使用英文。

## 发布

本地归档打包、Rolling 预发布与版本标签自动发布流程见[发布指南](./release.md)。

## 交付检查

1. **全量质量门禁**：通过 `task check`（涵盖格式检查、`golangci-lint`、`govulncheck`、竞态单测、架构与目录守卫、全部 CI/发布契约测试及 `git diff --check`），确认无临时文件或敏感信息残留。
2. **多端一致性**：涉及 CLI 标志、MCP 参数或技能说明变更时，同步对齐 CLI、MCP 与 `skills/veri/SKILL.md`。
3. **文档与更新日志**：涉及配置、目录、命令或用户可见变化时，同步更新 `README.md`、`README.en.md`、`docs/guide/*`、对应目录 `README.md` 或 `CHANGELOG.md` 顶部的 `未发布` 段落。
