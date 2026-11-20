# 配置模板 (`internal/config/templates`)

存放由 `//go:embed` 内嵌并供 `veri config init` 生成的简体中文与英文注释 `config.toml` 模板。

## 目录结构

- `zh-CN.toml`：带简体中文注释的默认配置模板。
- `en-US.toml`：带英文注释的默认配置模板。

## 维护规范

- **键值严格对称**：`zh-CN.toml` 与 `en-US.toml` 的激活配置键集合及默认取值必须保持 100% 一致，且严格与 `Default()` 对齐。
- **零真实密钥**：模板仅演示 `env:VAR` 引用与配置结构，禁止包含任何真实凭据。
