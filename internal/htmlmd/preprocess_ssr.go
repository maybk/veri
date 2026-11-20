package htmlmd

import (
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// resolveStreamingAndShadowDOM 还原现代前端流式 SSR（如 Next.js/React $RC 隐藏槽位）与 Declarative Shadow DOM。
func resolveStreamingAndShadowDOM(root *html.Node) {
	if root == nil {
		return
	}
	var shadowTemplates []*html.Node
	placeholders := make(map[string]*html.Node)
	segments := make(map[string]*html.Node)

	visited := 0
	stack := []*html.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		visited++
		if visited > DefaultMaxNodes {
			break
		}
		if n.Type == html.ElementNode {
			tag := tagName(n)
			id := strings.TrimSpace(getAttr(n, "id"))
			if tag == "template" {
				mode := strings.ToLower(strings.TrimSpace(firstNonEmpty(getAttr(n, "shadowrootmode"), getAttr(n, "shadowroot"))))
				if mode == "open" || mode == "closed" {
					shadowTemplates = append(shadowTemplates, n)
				} else if key, ok := strings.CutPrefix(id, "B:"); ok && key != "" {
					placeholders[key] = n
				}
			} else if key, ok := strings.CutPrefix(id, "S:"); ok && key != "" {
				segments[key] = n
			}
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}

	// 将流式渲染的正文块 S:x 填充回对应的骨架占位符 B:x 位置
	for key, slot := range placeholders {
		seg, ok := segments[key]
		if !ok || slot.Parent == nil || isAncestorNode(seg, slot) {
			continue
		}
		moveChildrenBefore(seg, slot)
		detachNode(slot)
		detachNode(seg)
	}

	// 解包 Declarative Shadow DOM <template shadowrootmode="open|closed">
	for _, tpl := range shadowTemplates {
		if tpl.Parent == nil {
			continue
		}
		moveChildrenBefore(tpl, tpl)
		detachNode(tpl)
	}
}

func isAncestorNode(candidateAncestor, node *html.Node) bool {
	for cur := node; cur != nil; cur = cur.Parent {
		if cur == candidateAncestor {
			return true
		}
	}
	return false
}

func moveChildrenBefore(srcParent, anchor *html.Node) {
	if srcParent == nil || anchor == nil || anchor.Parent == nil {
		return
	}
	dstParent := anchor.Parent
	for srcParent.FirstChild != nil {
		child := srcParent.FirstChild
		srcParent.RemoveChild(child)
		dstParent.InsertBefore(child, anchor)
	}
}

func isJSONLDScript(n *html.Node) bool {
	rawType := strings.ToLower(strings.TrimSpace(getAttr(n, "type")))
	if rawType == "" {
		return false
	}
	media, _, _ := strings.Cut(rawType, ";")
	return strings.TrimSpace(media) == "application/ld+json"
}

func isNoscriptWithImage(n *html.Node) bool {
	raw := strings.ToLower(collectPlainText(n))
	return strings.Contains(raw, "<img")
}

// hasZeroVisibilityStyle 检测内联 CSS 中常见的隐藏元素与零可见度提示词注入手法。
func hasZeroVisibilityStyle(styleRaw string) bool {
	for decl := range strings.SplitSeq(styleRaw, ";") {
		prop, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		prop = strings.ToLower(strings.TrimSpace(prop))
		val = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(val), "!important")))
		valCompact := strings.ReplaceAll(val, " ", "")
		if valCompact == "" {
			continue
		}
		switch prop {
		case "display":
			if valCompact == "none" {
				return true
			}
		case "visibility":
			if valCompact == "hidden" || valCompact == "collapse" {
				return true
			}
		case "opacity":
			if isZeroCSSNumber(valCompact) {
				return true
			}
		case "font-size":
			if isZeroCSSLength(valCompact) {
				return true
			}
		case "color":
			if valCompact == "transparent" {
				return true
			}
		case "clip-path":
			if strings.Contains(valCompact, "inset(50%)") || strings.Contains(valCompact, "inset(100%)") || strings.HasPrefix(valCompact, "circle(0") {
				return true
			}
		}
	}
	return false
}

func isZeroCSSNumber(val string) bool {
	trimmed := strings.TrimSuffix(val, "%")
	f, err := strconv.ParseFloat(trimmed, 64)
	return err == nil && f == 0
}

func isZeroCSSLength(val string) bool {
	for _, unit := range []string{"px", "rem", "em", "pt", "pc", "vh", "vw", "vmin", "vmax", "ch", "ex", "cm", "mm", "in", "%"} {
		if before, ok := strings.CutSuffix(val, unit); ok {
			val = before
			break
		}
	}
	f, err := strconv.ParseFloat(val, 64)
	return err == nil && f == 0
}

// unwrapHeadingCardLink 将仅包裹单个 h1~h6 的 <a href="..."><h2>Title</h2></a> 翻转为 <h2><a href="...">Title</a></h2>，保留标题层级。
func unwrapHeadingCardLink(a *html.Node) *html.Node {
	if a == nil || a.Parent == nil || strings.TrimSpace(getAttr(a, "href")) == "" {
		return nil
	}
	var heading *html.Node
	for c := a.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		if c.Type == html.ElementNode && isHeadingTag(tagName(c)) && heading == nil {
			heading = c
			continue
		}
		return nil
	}
	if heading == nil {
		return nil
	}

	newLink := newElement("a")
	newLink.Attr = slices.Clone(a.Attr)
	for heading.FirstChild != nil {
		ch := heading.FirstChild
		heading.RemoveChild(ch)
		newLink.AppendChild(ch)
	}
	detachNode(heading)
	heading.AppendChild(newLink)
	replaceNode(a, heading)
	return heading
}

func isHeadingTag(tag string) bool {
	return len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6'
}
