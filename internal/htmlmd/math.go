package htmlmd

import (
	"encoding/json/v2"
	"strings"

	"golang.org/x/net/html"
)

const maxMathMLDepth = 32

type mediaWikiDataMW struct {
	Name string `json:"name"`
	Body struct {
		ExtSrc string `json:"extsrc"`
	} `json:"body"`
}

func containsMathDescendant(root *html.Node) bool {
	stack := []*html.Node{root}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.Type == html.ElementNode {
			t := tagName(cur)
			if t == "math" || t == "mjx-container" ||
				hasClass(cur, "katex") || hasClass(cur, "katex-display") ||
				hasClass(cur, "mwe-math-element") ||
				hasAttr(cur, "data-latex") || hasAttr(cur, "data-tex") {
				return true
			}
		}
		for c := cur.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	return false
}

// extractMathNode 识别并还原网页中的数学公式为 LaTeX 源码，按优先级尝试：
// 1. <script type="math/tex">；2. KaTeX/MathML <annotation>；3. alttext / data-latex / data-tex；
// 4. MediaWiki data-mw JSON 的 body.extsrc；5. 纯 MathML 结构线性化兜底。
func extractMathNode(n *html.Node, tag string) (latex string, isBlock bool, ok bool) {
	if tag == "script" {
		sType := strings.ToLower(strings.ReplaceAll(getAttr(n, "type"), " ", ""))
		if strings.HasPrefix(sType, "math/tex") {
			raw := strings.TrimSpace(collectPlainText(n))
			if raw == "" {
				return "", false, false
			}
			return raw, strings.Contains(sType, "mode=display"), true
		}
		return "", false, false
	}

	isMathContainer := tag == "math" ||
		tag == "mjx-container" ||
		hasClass(n, "katex-display") ||
		hasClass(n, "katex") ||
		hasClass(n, "math-display") ||
		hasClass(n, "math-inline") ||
		hasClass(n, "MathJax_Display") ||
		hasClass(n, "mwe-math-element") ||
		hasAttr(n, "data-latex") ||
		hasAttr(n, "data-tex")
	if !isMathContainer {
		return "", false, false
	}

	isBlock = isBlockMathNode(n)

	// 1. 直接属性（data-latex / data-tex / alttext）
	if v := firstMathAttr(n); v != "" {
		return v, isBlock, true
	}

	// 2. MediaWiki data-mw 属性（Parsoid 读视图将原始 TeX 存于 body.extsrc）
	if v := extractMediaWikiMathTeX(n); v != "" {
		return v, isBlock, true
	}

	// 3. 子树遍历：优先 <annotation encoding="*tex*">，其次子 <math> 的 alttext / data-mw
	var mathNode *html.Node
	if tag == "math" {
		mathNode = n
	}
	stack := []*html.Node{n}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.Type == html.ElementNode {
			ct := tagName(cur)
			if ct == "annotation" {
				enc := strings.ToLower(strings.TrimSpace(getAttr(cur, "encoding")))
				if strings.Contains(enc, "tex") || enc == "" {
					if tex := strings.TrimSpace(collectPlainText(cur)); tex != "" {
						return tex, isBlock, true
					}
				}
			}
			if ct == "math" {
				if mathNode == nil {
					mathNode = cur
				}
				if !isBlock && isBlockMathNode(cur) {
					isBlock = true
				}
				if alt := firstMathAttr(cur); alt != "" {
					return alt, isBlock, true
				}
				if mw := extractMediaWikiMathTeX(cur); mw != "" {
					return mw, isBlock, true
				}
			}
		}
		for c := cur.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}

	// 4. 兜底：将纯 MathML 结构线性化为 LaTeX，保留分式、上下标与根式语义
	if mathNode != nil {
		if tex := strings.TrimSpace(linearizeMathML(mathNode, 0)); tex != "" {
			return tex, isBlock, true
		}
	}
	return "", false, false
}

func isBlockMathNode(n *html.Node) bool {
	return hasClass(n, "katex-display") ||
		hasClass(n, "math-display") ||
		hasClass(n, "MathJax_Display") ||
		hasClass(n, "mwe-math-element-block") ||
		strings.EqualFold(getAttr(n, "display"), "block") ||
		strings.EqualFold(getAttr(n, "display"), "true") ||
		strings.EqualFold(getAttr(n, "mode"), "display")
}

func firstMathAttr(n *html.Node) string {
	for _, attr := range []string{"data-latex", "data-tex", "alttext"} {
		if v := strings.TrimSpace(getAttr(n, attr)); v != "" {
			return v
		}
	}
	return ""
}

func extractMediaWikiMathTeX(n *html.Node) string {
	raw := strings.TrimSpace(getAttr(n, "data-mw"))
	if raw == "" || !strings.Contains(raw, "extsrc") {
		return ""
	}
	var mw mediaWikiDataMW
	if err := json.Unmarshal([]byte(raw), &mw); err != nil {
		return ""
	}
	if mw.Name != "" && !strings.EqualFold(mw.Name, "math") {
		return ""
	}
	return strings.TrimSpace(mw.Body.ExtSrc)
}

func linearizeMathML(n *html.Node, depth int) string {
	if n == nil || depth > maxMathMLDepth {
		return ""
	}
	if n.Type == html.TextNode {
		return collapseInlineSpaces(n.Data)
	}
	if n.Type != html.ElementNode {
		return ""
	}

	tag := tagName(n)
	switch tag {
	case "annotation", "annotation-xml", "mphantom", "merror":
		return ""
	case "mi", "mn", "mo", "mtext", "ms":
		return strings.TrimSpace(collapseInlineSpaces(collectPlainText(n)))
	case "mspace":
		return " "
	case "mfrac":
		kids := mathMLChildren(n)
		if len(kids) >= 2 {
			num := strings.TrimSpace(linearizeMathML(kids[0], depth+1))
			den := strings.TrimSpace(linearizeMathML(kids[1], depth+1))
			if num != "" && den != "" {
				return `\frac{` + num + `}{` + den + `}`
			}
		}
	case "msup":
		kids := mathMLChildren(n)
		if len(kids) >= 2 {
			base := strings.TrimSpace(linearizeMathML(kids[0], depth+1))
			sup := strings.TrimSpace(linearizeMathML(kids[1], depth+1))
			return base + "^{" + sup + "}"
		}
	case "msub":
		kids := mathMLChildren(n)
		if len(kids) >= 2 {
			base := strings.TrimSpace(linearizeMathML(kids[0], depth+1))
			sub := strings.TrimSpace(linearizeMathML(kids[1], depth+1))
			return base + "_{" + sub + "}"
		}
	case "msubsup", "munderover":
		kids := mathMLChildren(n)
		if len(kids) >= 3 {
			base := strings.TrimSpace(linearizeMathML(kids[0], depth+1))
			sub := strings.TrimSpace(linearizeMathML(kids[1], depth+1))
			sup := strings.TrimSpace(linearizeMathML(kids[2], depth+1))
			return base + "_{" + sub + "}^{" + sup + "}"
		}
	case "munder":
		kids := mathMLChildren(n)
		if len(kids) >= 2 {
			base := strings.TrimSpace(linearizeMathML(kids[0], depth+1))
			sub := strings.TrimSpace(linearizeMathML(kids[1], depth+1))
			return base + "_{" + sub + "}"
		}
	case "mover":
		kids := mathMLChildren(n)
		if len(kids) >= 2 {
			base := strings.TrimSpace(linearizeMathML(kids[0], depth+1))
			over := strings.TrimSpace(linearizeMathML(kids[1], depth+1))
			return base + "^{" + over + "}"
		}
	case "msqrt":
		inner := strings.TrimSpace(linearizeMathMLChildren(n, depth+1))
		if inner != "" {
			return `\sqrt{` + inner + `}`
		}
	case "mroot":
		kids := mathMLChildren(n)
		if len(kids) >= 2 {
			base := strings.TrimSpace(linearizeMathML(kids[0], depth+1))
			idx := strings.TrimSpace(linearizeMathML(kids[1], depth+1))
			if idx != "" {
				return `\sqrt[` + idx + `]{` + base + `}`
			}
			return `\sqrt{` + base + `}`
		}
	case "mfenced":
		open := getAttr(n, "open")
		if open == "" {
			open = "("
		}
		closeCh := getAttr(n, "close")
		if closeCh == "" {
			closeCh = ")"
		}
		kids := mathMLChildren(n)
		parts := make([]string, 0, len(kids))
		for _, k := range kids {
			if p := strings.TrimSpace(linearizeMathML(k, depth+1)); p != "" {
				parts = append(parts, p)
			}
		}
		return open + strings.Join(parts, ", ") + closeCh
	}

	return linearizeMathMLChildren(n, depth+1)
}

func mathMLChildren(n *html.Node) []*html.Node {
	var kids []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			t := tagName(c)
			if t == "annotation" || t == "annotation-xml" {
				continue
			}
			kids = append(kids, c)
		}
	}
	return kids
}

func linearizeMathMLChildren(n *html.Node, depth int) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		part := linearizeMathML(c, depth)
		if part == "" {
			continue
		}
		if b.Len() > 0 && needsMathSpace(b.String(), part) {
			b.WriteByte(' ')
		}
		b.WriteString(part)
	}
	return b.String()
}

func needsMathSpace(prev, next string) bool {
	if prev == "" || next == "" {
		return false
	}
	last := prev[len(prev)-1]
	first := next[0]
	if last == ' ' || first == ' ' || last == '(' || last == '{' || last == '[' || first == ')' || first == '}' || first == ']' || first == ',' || first == '^' || first == '_' {
		return false
	}
	return (last == '=' || last == '+' || last == '-' || first == '=' || first == '+' || first == '-')
}
