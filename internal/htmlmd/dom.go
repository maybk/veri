package htmlmd

import (
	"net/url"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	attrLang  = "data-htmlmd-lang"
	attrMath  = "data-htmlmd-math"
	attrAlign = "data-htmlmd-align"
	attrTask  = "data-htmlmd-task"
	attrAlert = "data-htmlmd-alert"

	maxDataURILen = 256
)

func tagName(n *html.Node) string {
	if n == nil || n.Type != html.ElementNode {
		return ""
	}
	return strings.ToLower(n.Data)
}

func getAttr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	if n == nil {
		return false
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return true
		}
	}
	return false
}

func setAttr(n *html.Node, key, val string) {
	if n == nil {
		return
	}
	for i := range n.Attr {
		if strings.EqualFold(n.Attr[i].Key, key) {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func hasClass(n *html.Node, target string) bool {
	cls := getAttr(n, "class")
	if cls == "" {
		return false
	}
	for token := range strings.FieldsSeq(cls) {
		if strings.EqualFold(token, target) {
			return true
		}
	}
	return false
}

func hasClassSubstr(n *html.Node, sub string) bool {
	cls := strings.ToLower(getAttr(n, "class"))
	return cls != "" && strings.Contains(cls, sub)
}

func detachNode(n *html.Node) {
	if n == nil || n.Parent == nil {
		return
	}
	n.Parent.RemoveChild(n)
}

func replaceNode(oldNode, newNode *html.Node) {
	if oldNode == nil || oldNode.Parent == nil || newNode == nil {
		return
	}
	parent := oldNode.Parent
	parent.InsertBefore(newNode, oldNode)
	parent.RemoveChild(oldNode)
}

func newElement(tag string) *html.Node {
	lower := strings.ToLower(tag)
	return &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Lookup([]byte(lower)),
		Data:     lower,
	}
}

func setElementTag(n *html.Node, tag string) {
	if n == nil || n.Type != html.ElementNode {
		return
	}
	lower := strings.ToLower(tag)
	n.Data = lower
	n.DataAtom = atom.Lookup([]byte(lower))
}

func newTextElement(tag, attrKey, attrVal, text string) *html.Node {
	el := newElement(tag)
	if attrKey != "" {
		el.Attr = []html.Attribute{{Key: attrKey, Val: attrVal}}
	}
	if text != "" {
		el.AppendChild(&html.Node{
			Type: html.TextNode,
			Data: text,
		})
	}
	return el
}

// collectPlainText 使用显式栈按文档前序提取子树全部文本内容。
func collectPlainText(root *html.Node) string {
	if root == nil {
		return ""
	}
	var b strings.Builder
	stack := []*html.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	return b.String()
}

// CloneSubtree 深拷贝给定 DOM 子树（受 maxNodes 节点上限保护，超出时返回 nil）。
func CloneSubtree(root *html.Node, maxNodes int) *html.Node {
	if root == nil {
		return nil
	}
	if maxNodes <= 0 {
		maxNodes = DefaultMaxNodes
	}
	type frame struct {
		src *html.Node
		dst *html.Node
	}
	cloneSingle := func(n *html.Node) *html.Node {
		cp := &html.Node{
			Type:      n.Type,
			DataAtom:  n.DataAtom,
			Data:      n.Data,
			Namespace: n.Namespace,
		}
		if len(n.Attr) > 0 {
			cp.Attr = slices.Clone(n.Attr)
		}
		return cp
	}

	rootCopy := cloneSingle(root)
	count := 1
	stack := []frame{{src: root, dst: rootCopy}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for c := cur.src.FirstChild; c != nil; c = c.NextSibling {
			count++
			if count > maxNodes {
				return nil
			}
			childCopy := cloneSingle(c)
			cur.dst.AppendChild(childCopy)
			if c.FirstChild != nil {
				stack = append(stack, frame{src: c, dst: childCopy})
			}
		}
	}
	return rootCopy
}

// FindMainContentCandidate 显式栈检索现代文档站语义正文容器（article -> main -> [role=main] -> body）。
func FindMainContentCandidate(doc *html.Node) *html.Node {
	if doc == nil {
		return nil
	}
	var firstMain, firstRoleMain, firstBody *html.Node
	stack := []*html.Node{doc}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Type == html.ElementNode {
			switch tagName(n) {
			case "article":
				return n
			case "main":
				if firstMain == nil {
					firstMain = n
				}
			case "body":
				if firstBody == nil {
					firstBody = n
				}
			}
			if firstRoleMain == nil && strings.EqualFold(strings.TrimSpace(getAttr(n, "role")), "main") {
				firstRoleMain = n
			}
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	if firstMain != nil {
		return firstMain
	}
	if firstRoleMain != nil {
		return firstRoleMain
	}
	return firstBody
}

func resolveSafeURL(raw string, base *url.URL, isImage bool) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	for _, bad := range []string{"javascript:", "vbscript:", "file:"} {
		if strings.HasPrefix(lower, bad) {
			return ""
		}
	}
	if strings.HasPrefix(lower, "data:") {
		if !isImage || len(trimmed) > maxDataURILen {
			return ""
		}
		return trimmed
	}
	if strings.HasPrefix(trimmed, "#") {
		return trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	if base != nil {
		resolved := base.ResolveReference(parsed)
		scheme := strings.ToLower(resolved.Scheme)
		if scheme != "http" && scheme != "https" && scheme != "mailto" {
			return ""
		}
		return resolved.String()
	}
	if parsed.Scheme != "" {
		scheme := strings.ToLower(parsed.Scheme)
		if scheme != "http" && scheme != "https" && scheme != "mailto" {
			return ""
		}
	}
	return parsed.String()
}
