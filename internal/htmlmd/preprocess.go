package htmlmd

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Prepare 原地执行现代网页 DOM 归一化预处理：
//  1. 还原 KaTeX / MathJax / MathML 公式源码，消除无障碍层与视觉层重复乱码；
//  2. 移除隐藏节点、脚本样式、SVG 装饰图标及非正文交互控件；
//  3. 归一化现代语法高亮代码块与行号双列表格，剥离复制按钮/行号并保留代码语言与真实换行；
//  4. 固化表格单元格对齐属性，防止后续 Readability 清洗丢失对齐元数据。
func Prepare(root *html.Node) {
	if root == nil {
		return
	}
	resolveStreamingAndShadowDOM(root)

	visited := 0
	stack := []*html.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		visited++
		if visited > DefaultMaxNodes {
			return
		}
		if n.Parent == nil && n != root {
			continue
		}
		if n.Type == html.CommentNode {
			detachNode(n)
			continue
		}
		if n.Type == html.DocumentNode {
			for c := n.LastChild; c != nil; c = c.PrevSibling {
				stack = append(stack, c)
			}
			continue
		}
		if n.Type != html.ElementNode {
			continue
		}

		tag := tagName(n)

		// 1. 优先识别并提取数学公式（必须在 aria-hidden 移除前执行，避免误删 KaTeX MathML 注解）
		if latex, isBlock, ok := extractMathNode(n, tag); ok {
			wrapperTag := "span"
			mode := "inline"
			if isBlock {
				wrapperTag = "p"
				mode = "block"
			}
			replaceNode(n, newTextElement(wrapperTag, attrMath, mode, latex))
			continue
		}

		// 2. 移除明确的非正文标签与隐藏元素
		if shouldDropElement(n, tag) {
			detachNode(n)
			continue
		}

		// 3. 将 <picture> 内 <source srcset> 的高清图源回填至子 <img>，再清理 <source>
		if tag == "picture" {
			promotePictureSources(n)
		}

		// 4. 将真实网页中的 div/aside Alert 或 Admonition 容器原地归一化为 <blockquote data-htmlmd-alert="...">
		if tag == "div" || tag == "aside" || tag == "blockquote" {
			normalizeAlertContainer(n, tag)
		}

		// 5. 识别语法高亮器常用的“左行号 + 右代码”双列布局表格，提纯为标准 <pre>
		if tag == "table" {
			if codePre := extractCodeTable(n); codePre != nil {
				normalizePreElement(codePre)
				replaceNode(n, codePre)
				continue
			}
		}

		// 6. 归一化 <pre> 代码块（剥离按钮/行号，还原 .line 换行，提取语言至 data-htmlmd-lang，解包纯高亮单子元素外层 div）
		if tag == "pre" {
			normalizePreElement(n)
			unwrapSinglePreWrapper(n)
			continue
		}

		// 7. 将仅包裹单个标题的卡片链接 <a href><h2>T</h2></a> 翻转为 <h2><a href>T</a></h2> 以保留章节层级
		if tag == "a" {
			if heading := unwrapHeadingCardLink(n); heading != nil {
				stack = append(stack, heading)
				continue
			}
		}

		// 8. 提前固化任务列表复选框与表格列对齐至 data-* 属性（防止 Readability 剥离 <input> / align / style）
		if tag == "li" {
			preserveTaskListAttr(n)
		}
		if tag == "th" || tag == "td" {
			if align := detectCellAlignment(n); align != "" {
				setAttr(n, attrAlign, align)
			}
		}

		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
}

func shouldDropElement(n *html.Node, tag string) bool {
	switch tag {
	case "script":
		return !isJSONLDScript(n)
	case "noscript":
		return !isNoscriptWithImage(n)
	case "style", "template", "svg", "canvas", "iframe", "object", "embed", "dialog", "audio", "video", "source", "track", "map", "area":
		return true
	case "input":
		return !strings.EqualFold(strings.TrimSpace(getAttr(n, "type")), "checkbox")
	case "button", "select", "textarea":
		return true
	}

	if hasAttr(n, "hidden") && !strings.EqualFold(strings.TrimSpace(getAttr(n, "hidden")), "false") {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(getAttr(n, "aria-hidden")), "true") && !containsMathDescendant(n) {
		return true
	}
	if styleRaw := getAttr(n, "style"); styleRaw != "" && hasZeroVisibilityStyle(styleRaw) {
		return true
	}
	return false
}

func findFirstTag(root *html.Node, targetTag string) *html.Node {
	stack := []*html.Node{root}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.Type == html.ElementNode && tagName(cur) == targetTag {
			return cur
		}
		for c := cur.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	return nil
}

func isLineNumberCell(cell *html.Node) bool {
	if hasClassSubstr(cell, "gutter") || hasClassSubstr(cell, "lineno") || hasClassSubstr(cell, "line-num") || hasClassSubstr(cell, "blob-num") {
		return true
	}
	text := strings.TrimSpace(collectPlainText(cell))
	if text == "" {
		return false
	}
	for _, r := range text {
		if !unicode.IsDigit(r) && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func detectCellAlignment(cell *html.Node) string {
	if a := normalizeAlignValue(getAttr(cell, attrAlign)); a != "" {
		return a
	}
	if a := normalizeAlignValue(getAttr(cell, "align")); a != "" {
		return a
	}
	style := strings.ToLower(getAttr(cell, "style"))
	if _, rest, ok := strings.Cut(style, "text-align"); ok {
		if _, val, hasColon := strings.Cut(rest, ":"); hasColon {
			val, _, _ = strings.Cut(val, ";")
			return normalizeAlignValue(val)
		}
	}
	return ""
}

func normalizeAlignValue(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "left", "start":
		return "left"
	case "center", "middle":
		return "center"
	case "right", "end":
		return "right"
	default:
		return ""
	}
}

func preserveTaskListAttr(li *html.Node) {
	for c := li.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		if isDirectCheckboxInput(c) {
			recordAndDetachCheckbox(li, c)
			return
		}
		if c.Type == html.ElementNode && tagName(c) == "p" {
			for pc := c.FirstChild; pc != nil; pc = pc.NextSibling {
				if pc.Type == html.TextNode && strings.TrimSpace(pc.Data) == "" {
					continue
				}
				if isDirectCheckboxInput(pc) {
					recordAndDetachCheckbox(li, pc)
				}
				return
			}
		}
		return
	}
}

func recordAndDetachCheckbox(li, checkbox *html.Node) {
	if hasAttr(checkbox, "checked") {
		setAttr(li, attrTask, "checked")
	} else {
		setAttr(li, attrTask, "unchecked")
	}
	detachNode(checkbox)
}

func promotePictureSources(pic *html.Node) {
	img := findFirstTag(pic, "img")
	var bestURL string
	for c := pic.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && tagName(c) == "source" {
			if bestURL == "" {
				rawSet := firstNonEmpty(getAttr(c, "srcset"), getAttr(c, "data-srcset"))
				bestURL = pickLargestSrcsetURL(rawSet)
			}
			detachNode(c)
		}
		c = next
	}
	if bestURL != "" {
		if img == nil {
			img = newElement("img")
			pic.AppendChild(img)
		}
		setAttr(img, "data-src", bestURL)
		if curSrc := strings.TrimSpace(getAttr(img, "src")); curSrc == "" || strings.HasPrefix(strings.ToLower(curSrc), "data:") {
			setAttr(img, "src", bestURL)
		}
	}
}

func normalizeAlertContainer(n *html.Node, tag string) {
	alertKind := classifyAlertContainer(n)
	if alertKind == "" {
		return
	}
	// 移除 GitHub / Docusaurus 渲染生成的重复视觉标题栏（如 <p class="markdown-alert-title">Warning</p>）
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && (hasClass(c, "markdown-alert-title") || hasClass(c, "admonition-title") || hasClass(c, "callout-title")) {
			detachNode(c)
		}
		c = next
	}
	if tag != "blockquote" {
		setElementTag(n, "blockquote")
	}
	setAttr(n, attrAlert, alertKind)
}

func classifyAlertContainer(n *html.Node) string {
	if v := strings.ToUpper(strings.TrimSpace(getAttr(n, attrAlert))); v != "" {
		return v
	}
	if callout := strings.ToLower(strings.TrimSpace(getAttr(n, "data-callout"))); callout != "" {
		if mapped := mapAlertKind(callout); mapped != "" {
			return mapped
		}
	}
	isAlertBox := hasClass(n, "markdown-alert") || hasClass(n, "admonition") || hasClass(n, "callout") || hasClass(n, "custom-block")
	for tok := range strings.FieldsSeq(strings.ToLower(getAttr(n, "class"))) {
		if kind, ok := strings.CutPrefix(tok, "markdown-alert-"); ok && kind != "title" {
			return mapAlertKind(kind)
		}
		if isAlertBox {
			if mapped := mapAlertKind(strings.TrimPrefix(tok, "callout-")); mapped != "" {
				return mapped
			}
		}
	}
	return ""
}

func mapAlertKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "note", "info", "abstract", "summary":
		return "NOTE"
	case "tip", "hint", "success":
		return "TIP"
	case "important":
		return "IMPORTANT"
	case "warning", "warn", "attention", "question":
		return "WARNING"
	case "caution", "danger", "error", "failure", "bug":
		return "CAUTION"
	default:
		return ""
	}
}
