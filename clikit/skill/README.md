# 技能文件安装器 (`clikit/skill`)

从 `fs.FS` 读取内嵌 `SKILL.md`，并跨平台（macOS / Windows / Linux）原子安装至标准 `.agents/skills` 目录或自定义目录。

## 目录结构

- `doc.go`：包级说明与依赖边界。
- `skill.go`：`InstallOptions`、`ErrAlreadyExists`、`ErrInvalidPath`、`Read`、`ResolveInstallPath` 与 `Install` 实现。

## 开发规范

- **参数化路径**：技能名称与资产路径均由调用方传入，不硬编码特定产品名或各家 Agent 私有目录。
- **覆盖保护**：目标文件已存在且未指定 `Force: true` 时拒绝覆盖，并返回目标路径与 `ErrAlreadyExists` 哨兵错误。

## 验证

```bash
go test -race ./clikit/skill/...
```
