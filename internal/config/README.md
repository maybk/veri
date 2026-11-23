# 配置加载与凭据解析 (`internal/config`)

负责跨平台配置文件路径发现、TOML 严格解码、环境变量覆盖合并、密钥池拆分与配置模板生成。

## 目录结构

- `types.go`：时长（`Duration`）与防溢出可读字节单位解析器（`ByteSize`）。
- `config.go`：配置结构体定义、基于 `toml` 标签的凭据/权重访问与自动脱敏快照构造。
- `defaults.go`：内置默认配置树。
- `load.go`：三端配置路径解析、未知 TOML 键拦截、环境变量合并与文件权限审计。
- `keys.go`：配置键元数据索引、拼写建议、单键脱敏读取与自定义覆盖视图。
- `edit.go`：单键写入、移除清理、明文密钥拦截与落盘前预校验。
- `toml_edit.go`：保留注释与换行的 TOML 行级编辑与字面量格式化。
- `credentials.go`：环境变量、加密凭据库与配置文件的优先级合并及多密钥脱敏。
- `validate.go`：枚举、数值边界、代理协议、网段与自定义单价语法校验。
- `template.go`：内嵌双语配置模板加载与 `veri config init` 原子写入。
- `templates/`：简体中文与英文的 `config.toml` 全注释参考模板。

## 开发规范

- **严格解码**：解码 `config.toml` 时必须校验 `MetaData.Undecoded()`，拒绝静默忽略拼写错误或未知配置项。
- **依赖收敛**：全仓仅允许在本包导入 `github.com/BurntSushi/toml`。

## 验证

```bash
go test -race ./internal/config/...
```
