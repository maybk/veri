# 开发与构建

## 环境要求

| 组件 | 要求 |
| --- | --- |
| Go | `>= 1.27.4`，全程启用 `CGO_ENABLED=0` |
| Task（可选） | `go-task` v3，用于执行 `Taskfile.yml` 开发、检查与跨平台打包任务 |
| 静态与漏洞扫描（可选） | `golangci-lint` v2、`govulncheck`（未安装或版本不匹配时，`task lint` / `task check` 自动通过 `go run` 执行与 CI 一致的版本） |
| Node.js（可选） | 仅在执行 `task dev` 启动 `@modelcontextprotocol/inspector` 调试面板时使用 |
| 操作系统 | macOS、Windows `10/11` 或 Linux |

## 本地开发

通过 `task` 运行本地 CLI 或拉起 MCP 调试面板：

```bash
task build                      # 编译当前宿主机架构二进制至 bin/veri
task run -- search "golang"     # 编译并运行指定子命令
task dev                        # 编译并拉起 MCP Inspector 调试面板
```

`task run` 与 `task dev` 固定按当前宿主机架构输出至 `bin/veri`（Windows 为 `bin/veri.exe`），不受外部 `GOOS` / `GOARCH` 影响。未安装 `task` 时可直接编译：

```bash
# macOS / Linux
CGO_ENABLED=0 go build -trimpath -o bin/veri .

# Windows (PowerShell)
$env:CGO_ENABLED="0"; go build -trimpath -o bin/veri.exe .
```

## 格式、分析与测试

日常检查统一通过 `task` 执行：

```bash
task fmt                        # 格式化全部 Go 源码
task lint                       # 运行 go vet、golangci-lint 与 govulncheck
task test                       # 运行全量单元测试（Unix 与 Windows amd64+gcc 自动启用 -race）
task test:cover                 # 运行全量单元测试并输出各包覆盖率（-race 规则同上）
task check                      # 运行完整质量门禁（格式、扫描、竞态单测、契约测试与空白检查）
```

按需运行真实 API 联调测试（读取本机环境变量密钥，不进入常规单测与 CI）：

```bash
task test:live                  # 运行已配置密钥的 Provider 联调测试
task test:live RUN=Exa          # 按名称过滤指定联调测试
```

## 构建与跨平台打包

构建当前宿主机架构二进制或归档：

```bash
task build
task package
```

交叉编译并打包三端六架构归档与 `SHA256SUMS.txt` 至 `dist/`：

```bash
task package:all
```

### 按架构独立构建与打包（macOS / Windows / Linux）

全程启用 `CGO_ENABLED=0`，可在任意宿主机交叉输出单架构二进制（`bin/veri-<os>-<arch>`）与归档（`dist/`）：

- **macOS（`arm64` / `amd64`，`.tar.gz`）**：
  ```bash
  task darwin:build:arm64
  task darwin:build:amd64
  task darwin:package:arm64
  task darwin:package:amd64
  ```

- **Windows（`amd64` / `arm64`，`.zip`）**：
  ```bash
  task windows:build:amd64
  task windows:build:arm64
  task windows:package:amd64
  task windows:package:arm64
  ```

- **Linux（`amd64` / `arm64`，`.tar.gz`）**：
  ```bash
  task linux:build:amd64
  task linux:build:arm64
  task linux:package:amd64
  task linux:package:arm64
  ```

## 代码规范

1. **版本与常量真源**：版本号维护在 `build/config.yml` 的 `info.version`；跨包共享规格归口 `internal/spec/{app,registry,options,defaults}.go`，包私有常量置于本包 `defaults.go`。
2. **分层与文件预算**：遵守 `AGENTS.md` 导入方向，禁用 `init()` 隐式注册，适配器在 `internal/app/registry.go` 显式装配；根目录 `*.go` 及 `clikit/`、`internal/` 下手写 Go 源码 `<= 500` 行、`*_test.go` `<= 800` 行（豁免 `// Code generated ... DO NOT EDIT.` 与 `testdata/`），超限时同包按职责拆分，禁止拆包、加无意义 `interface`、删注释或压行。
3. **测试与平台命名**：测试按领域场景命名（如 `fusion_test.go`、`cache_test.go`、`ssrf_test.go`、`gate_test.go`），大夹具入 `testdata/`，禁止 `extra`、`misc`、`*_split`、`*_partN`、`*_N` 等无语义或序号拆分命名（含平台后缀）；平台实现采用 `<role>_{darwin,linux,windows,other}.go`。
4. **新增 Provider**：在 `internal/provider/<name>/` 提供 `doc.go`、`defaults.go`、`provider.go`、`wire.go`、`errors.go`、`search.go` / `fetch.go`、`provider_test.go`、`README.md`，并在 `testdata/` 附至少 3 份合法 RFC 8259 JSON 脱敏样本与 `testdata/README.md`，最后在 `internal/spec/registry.go`、`internal/config` 与 `internal/app/registry.go` 注册。
5. **双轨语言契约**：终端文案维护在 `internal/cli/locales/{zh-CN,en-US}.json`（键与占位符严格对称）；MCP 描述、`--json` 字段与 `skills/veri/SKILL.md` 固定英文。

## 发布

本地打包、Rolling 预发布与标签自动发布流程见[发布指南](./release.md)。

## 交付检查

1. **质量门禁**：通过 `task check`（格式、`golangci-lint`、`govulncheck`、竞态单测、架构/文件预算/目录守卫、CI 与发布契约测试及 `git diff --check`），确认无临时文件或敏感信息残留。
2. **多端一致**：CLI 标志、MCP 参数或技能说明变更时，同步对齐 CLI、MCP 与 `skills/veri/SKILL.md`。
3. **文档与日志**：配置、目录、命令或用户可见变化时，同步更新 `README.md`、`README.en.md`、`docs/`、目录 `README.md` 或 `CHANGELOG.md` 待发布段。
