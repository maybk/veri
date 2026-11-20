# `internal/mdscan`

零依赖、低分配的 Markdown 行级结构扫描叶子包，提供全仓共用的围栏代码块状态机与 ATX 标题解析。

## 目录结构

- `doc.go`：包职责说明。
- `mdscan.go`：围栏定界符解析（`ParseFenceOpen` / `Fence.Closes`）、ATX 标题解析（`ParseATXHeading` / `FirstH1`）与逐行状态跟踪器（`Tracker`）。

## 开发规范

- **纯标准库叶子包**：严禁导入任何 `veri/**` 或第三方依赖，保持零堆分配热路径。
- **CommonMark 围栏规则**：开围栏要求连续至少 3 个 `` ` `` 或 `~`，反引号围栏信息串禁止包含 `` ` ``；闭围栏要求同字符、长度不小于开围栏且行尾仅含空白；允许列表缩进产生的前导空白。

## 验证

```bash
go test -race -v ./internal/mdscan
```
