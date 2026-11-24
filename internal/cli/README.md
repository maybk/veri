# 命令行界面 (`internal/cli`)

基于 Cobra 构建命令行入口，负责参数解析、子命令分组分发、双语界面渲染与架构合规门禁校验。

## 目录结构

- `doc.go`：包级说明与依赖边界。
- `locales/`：内嵌的简体中文与英文终端界面词表目录。
- `locales.go`：双语词表加载与结构化错误本地化格式化。
- `help.go`：双语 Help/Usage 渲染器、首屏渐进式披露、子命令排序与参数占位符及默认值注解。
- `args.go`：本地化位置参数校验器（`requireNoArgs` / `requireExactArgs` / `requireMinArgs`）、分组子命令拦截与拼写纠错。
- `root.go`：根命令装配、裸查询防误触守卫、全局标志绑定与进程退出码映射。
- `search.go`：`veri search` 子命令、过滤标志、本地缓存绕过（`--no-cache`）与结果输出。
- `fetch.go`：`veri fetch` 子命令（支持单页、多 URL 并发与 `stdin` 批量输入）、章节/关键词定位、缓存命中提示与分段偏移提示。
- `admin.go`：`veri providers` 与 `veri doctor` 子命令。
- `config_cmd.go`：`veri config` 配置项读写、查看、模板生成与补全子命令。
- `cache_cmd.go`：`veri cache info / clear` 统一缓存策略查看与本地磁盘缓存清理子命令。
- `update.go`：`veri update` 检查更新与跨平台安全自更新子命令。
- `keys.go`：`veri key` 本地加密密钥设置（含追加）、查看与移除子命令。
- `key_import.go`：`veri key import` 批量加密导入与配置明文迁移子命令。
- `integrations.go`：`veri mcp`（含只读 `veri mcp config`）、`veri skill` 与 `veri version` 子命令。
- `testdata/`：命令行端到端测试使用的标准输出黄金比对样本目录。

## 开发规范

- **薄适配层**：仅做参数校验、调用应用层服务与格式化输出，不承载路由策略或网络请求逻辑。
- **架构门禁维护**：新增模块、新增第三方依赖或调整目录边界时，同步更新架构守卫测试。

## 验证

```bash
go test -race ./internal/cli/...
```
