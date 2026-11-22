# 文本归一化与质量评估 (`internal/textnorm`)

提供纯 Go 零第三方依赖的文本归一化、隐形注入字符剥离、宽容日期时间解析、正文质量闸门、标题清洗与内容指纹。

## 目录结构

- `doc.go`：包职责说明与依赖边界。
- `text.go`：HTML 实体解码、不可见控制符与变体选择符剥离、单行空白折叠及 Unicode 码点安全截断。
- `time.go`：多格式绝对时间戳与英文相对时间（如 `2 days ago`、`1 month ago`）宽容解析及固定东八区时区。
- `quality.go`：反爬挑战盾、软 404、正文长度与链接密度三级网页正文质量检测。
- `title.go`：Frontmatter 头部剥离与脏标题还原、标题卫生门禁、重复拼接检测与站点后缀归一化。
- `fingerprint.go`：中英混合 4-token Shingle 内容指纹与跨链接同稿去重判定。

## 开发规范

- **零第三方依赖**：仅允许导入 Go 标准库与叶子包 `internal/mdscan`，严禁引入任何第三方库或上层业务包。

## 验证

```bash
go test -race ./internal/textnorm/...
```
