# 终端交互与内容包裹 (`clikit/term`)

封装终端 TTY 探测、ANSI 样式控制、无回显敏感输入读取与不可信文本标签包裹。

## 目录结构

- `doc.go`：包级说明与依赖边界。
- `palette.go`：`IsTerminal`、`Palette` 与 `DetectPalette` 着色控制。
- `prompt.go`：`ReadSecret` 敏感输入读取与 `Confirm` 交互确认（支持信号取消与终端回显恢复）。
- `table.go`：`DisplayWidth` 宽字符列宽计算与 `WriteTable` 对齐表格渲染。
- `untrusted.go`：`WrapUntrusted` / `WrapUntrustedRandom` 随机边界标识包裹与闭合标签清理。

## 开发规范

- **非交互安全**：非 TTY 输入场景下 `Confirm` 显式返回错误，禁止阻塞自动化流程；管道输入须限制读取上限。

## 验证

```bash
go test -race ./clikit/term/...
```
