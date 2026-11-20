# HTML 预处理与 Markdown 渲染 (`internal/htmlmd`)

基于纯 Go 与 `golang.org/x/net/html` 实现无状态网页 DOM 预处理与紧凑 GFM Markdown 转换。

## 目录结构

- `doc.go`：包职责与边界说明。
- `dom.go`：显式栈 DOM 遍历原语、有界子树深拷贝、语义正文容器定位与安全 URL 解析。
- `preprocess.go`：现代网页原地预处理入口（隐藏/噪音节点剥离、JSON-LD/`<noscript>` 懒加载图保护、表格对齐元数据保护）。
- `math.go`：KaTeX / MathJax / MediaWiki `data-mw` 与纯 MathML 结构线性化的数学公式还原。
- `preprocess_ssr.go`：流式 SSR（Next.js `$RC`）、Declarative Shadow DOM 解包、零可见度隐藏 CSS 注入过滤与卡片标题链接翻转。
- `preprocess_code.go`：现代语法高亮代码块与行号双列表格归一化（复制按钮/行号剥离、多节点行容器换行还原、代码语言提取）。
- `convert.go`：`ConvertString`、`ConvertBytes`、`RenderNode` 入口与有界深度渲染状态机。
- `block.go`：块级结构渲染（ATX 标题、段落、嵌套引用块与 GitHub Alert、有序/无序/任务列表、动态反引号围栏代码块、定义列表与折叠块）。
- `table.go`：面向 LLM Token 效率的紧凑 GFM 表格渲染（含 `colspan` 展开、列对齐、单元格管道符转义及单列/代码块布局伪表格自动解包）。
- `inline.go`：行内元素与上下文感知最小转义（CJK 标点友好的强调边界保护、动态反引号代码片段、相对链接解析、`srcset`/懒加载图片提取与行内公式）。

## 开发规范

- **零外部转换器与零业务耦合**：仅允许导入 Go 标准库、`golang.org/x/net/html` 与 `internal/mdscan`，禁止导入 `internal/core`、`internal/httpx` 或任何网络/配置包。
- **有界栈安全**：DOM 遍历与克隆使用显式栈或受 `maxRenderDepth` 深度上限保护，防止恶意超深嵌套网页触发栈溢出。

## 验证

```bash
go test -race ./internal/htmlmd/...
```
