package htmlmd

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type tableRowData struct {
	cells    []string
	aligns   []string
	isHeader bool
}

func (r *renderer) renderTable(table *html.Node, depth int) {
	// 若表格实为单列传统网页布局容器或内嵌多行 <pre> 代码块，直接解包为普通块级流渲染
	if shouldUnwrapLayoutTable(table) {
		r.ensureBlankLine()
		r.renderUnwrappedTable(table, depth)
		r.ensureBlankLine()
		return
	}

	var rows []tableRowData
	collectTableRows(r, table, table, false, depth, &rows)
	if len(rows) == 0 {
		return
	}

	colCount := 0
	for _, row := range rows {
		if len(row.cells) > colCount {
			colCount = len(row.cells)
		}
	}
	if colCount == 0 {
		return
	}

	// 过滤全空白无效行
	filtered := make([]tableRowData, 0, len(rows))
	for _, row := range rows {
		nonEmpty := false
		for _, c := range row.cells {
			if strings.TrimSpace(c) != "" {
				nonEmpty = true
				break
			}
		}
		if nonEmpty {
			filtered = append(filtered, row)
		}
	}
	if len(filtered) == 0 {
		return
	}

	// 若单列且无显式 <th> 表头，降级为普通段落避免无意义的 "| --- |" 单列伪表格
	if colCount == 1 && !filtered[0].isHeader {
		r.ensureBlankLine()
		for _, row := range filtered {
			if row.cells[0] != "" {
				r.writeRaw(row.cells[0])
				r.ensureBlankLine()
			}
		}
		return
	}

	colAligns := resolveColumnAlignments(filtered, colCount)

	r.ensureBlankLine()
	if caption := findDirectTag(table, "caption"); caption != nil {
		if capText := strings.TrimSpace(r.renderSubtreeInline(caption, depth+1)); capText != "" {
			r.writeRaw("**" + capText + "**")
			r.ensureBlankLine()
		}
	}

	r.writeTableRow(filtered[0].cells, colCount)
	r.writeTableSeparator(colAligns, colCount)
	for _, row := range filtered[1:] {
		r.writeTableRow(row.cells, colCount)
	}
	r.ensureBlankLine()
}

func (r *renderer) writeTableRow(cells []string, colCount int) {
	r.ensureNewline()
	var line strings.Builder
	line.WriteByte('|')
	for i := range colCount {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		line.WriteByte(' ')
		line.WriteString(cell)
		line.WriteString(" |")
	}
	r.writeRaw(line.String())
	r.ensureNewline()
}

func (r *renderer) writeTableSeparator(aligns []string, colCount int) {
	r.ensureNewline()
	var line strings.Builder
	line.WriteByte('|')
	for i := range colCount {
		sep := "---"
		if i < len(aligns) {
			switch aligns[i] {
			case "left":
				sep = ":---"
			case "center":
				sep = ":---:"
			case "right":
				sep = "---:"
			}
		}
		line.WriteByte(' ')
		line.WriteString(sep)
		line.WriteString(" |")
	}
	r.writeRaw(line.String())
	r.ensureNewline()
}

func collectTableRows(r *renderer, rootTable, cur *html.Node, inThead bool, depth int, out *[]tableRowData) {
	for c := cur.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		t := tagName(c)
		if t == "table" && c != rootTable {
			continue
		}
		switch t {
		case "thead":
			collectTableRows(r, rootTable, c, true, depth+1, out)
		case "tbody", "tfoot":
			collectTableRows(r, rootTable, c, false, depth+1, out)
		case "tr":
			row := extractTableRow(r, c, inThead, depth+1)
			if len(row.cells) > 0 {
				*out = append(*out, row)
			}
		}
	}
}

func extractTableRow(r *renderer, tr *html.Node, inThead bool, depth int) tableRowData {
	row := tableRowData{isHeader: inThead}
	allTH := true
	hasCells := false

	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		t := tagName(c)
		if t != "th" && t != "td" {
			continue
		}
		hasCells = true
		if t != "th" {
			allTH = false
		}

		cellText := renderTableCell(r, c, depth+1)
		align := detectCellAlignment(c)
		span := parseColSpan(getAttr(c, "colspan"))

		row.cells = append(row.cells, cellText)
		row.aligns = append(row.aligns, align)
		for k := 1; k < span; k++ {
			row.cells = append(row.cells, "")
			row.aligns = append(row.aligns, align)
		}
	}
	if hasCells && allTH {
		row.isHeader = true
	}
	return row
}

func renderTableCell(r *renderer, cell *html.Node, depth int) string {
	sub := &renderer{
		baseURL:     r.baseURL,
		cleanURL:    r.cleanURL,
		inTableCell: true,
	}
	sub.renderChildren(cell, depth)
	raw := strings.ReplaceAll(sub.buf.String(), "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n", " ")
	raw = collapseInlineSpaces(raw)
	raw = escapeTablePipes(raw)
	return strings.TrimSpace(raw)
}

func escapeTablePipes(s string) string {
	if !strings.Contains(s, "|") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	inCode := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '`' {
			inCode = !inCode
			b.WriteByte(ch)
			continue
		}
		if ch == '|' && !inCode {
			if i == 0 || s[i-1] != '\\' {
				b.WriteByte('\\')
			}
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func parseColSpan(raw string) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v <= 1 {
		return 1
	}
	return min(v, 16)
}

func resolveColumnAlignments(rows []tableRowData, colCount int) []string {
	aligns := make([]string, colCount)
	for _, row := range rows {
		for i := range min(colCount, len(row.aligns)) {
			if aligns[i] == "" && row.aligns[i] != "" {
				aligns[i] = row.aligns[i]
			}
		}
	}
	return aligns
}

func shouldUnwrapLayoutTable(table *html.Node) bool {
	if strings.EqualFold(strings.TrimSpace(getAttr(table, "role")), "presentation") {
		return true
	}
	hasTH := false
	hasMultiLinePre := false
	stack := []*html.Node{table}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur != table && cur.Type == html.ElementNode && tagName(cur) == "table" {
			return true
		}
		if cur.Type == html.ElementNode {
			switch tagName(cur) {
			case "th", "thead":
				hasTH = true
			case "pre":
				if strings.Contains(strings.TrimSpace(collectPlainText(cur)), "\n") {
					hasMultiLinePre = true
				}
			}
		}
		for c := cur.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	return !hasTH && hasMultiLinePre
}

func (r *renderer) renderUnwrappedTable(table *html.Node, depth int) {
	for c := table.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch tagName(c) {
		case "thead", "tbody", "tfoot":
			r.renderUnwrappedTable(c, depth+1)
		case "tr":
			for cell := c.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.Type == html.ElementNode && (tagName(cell) == "td" || tagName(cell) == "th") {
					r.renderChildren(cell, depth+2)
					r.ensureBlankLine()
				}
			}
		}
	}
}

func findDirectTag(parent *html.Node, target string) *html.Node {
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && tagName(c) == target {
			return c
		}
	}
	return nil
}
