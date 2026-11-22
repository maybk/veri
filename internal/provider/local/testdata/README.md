# 本地提取器测试样本 (`internal/provider/local/testdata`)

存放本地 HTML 正文提取与正文质量闸门测试使用的静态网页样本。

## 目录结构

- `article_ok.html`：标准静态技术文章页面样本（满足正文长度与段落密度要求，预期通过质量闸门）。
- `short_stub.html`：正文过短的残缺页面样本（预期触发 `too_short` 软降级标记）。
- `spa_shell.html`：典型前端单页应用（SPA）空壳与启用 JavaScript 提示样本（预期触发质量闸门升档）。

## 维护规范

- **标准 HTML 结构**：样本保持以 `<!doctype html>` 开头的纯净 HTML 文档，不在文档头部拼接非 HTML 注释行。
- **自包含离线样本**：样本内不引用需要联网加载的真实外部脚本或隐私链接。
