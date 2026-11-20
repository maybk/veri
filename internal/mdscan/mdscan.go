package mdscan

import "strings"

// MinFenceLen 是 CommonMark 规定的围栏代码块最小定界符长度。
const MinFenceLen = 3

// MaxHeadingLevel 是标准 ATX 标题的最大层级（H1-H6）。
const MaxHeadingLevel = 6

// Fence 描述当前打开的围栏代码块定界信息。
type Fence struct {
	Marker byte   // '`' 或 '~'
	Len    int    // >= MinFenceLen
	Info   string // 开围栏信息串（已去除首尾空白）
}

// OpenLine 返回可用于跨页补齐的开围栏行文本（不含末尾换行符）。
func (f Fence) OpenLine() string {
	if f.Len < MinFenceLen || (f.Marker != '`' && f.Marker != '~') {
		return ""
	}
	base := strings.Repeat(string(f.Marker), f.Len)
	if f.Info == "" {
		return base
	}
	return base + f.Info
}

// CloseLine 返回与当前开围栏匹配的闭围栏行文本（不含末尾换行符）。
func (f Fence) CloseLine() string {
	if f.Len < MinFenceLen || (f.Marker != '`' && f.Marker != '~') {
		return ""
	}
	return strings.Repeat(string(f.Marker), f.Len)
}

// Closes 判断给定单行是否为当前围栏的合法闭合行（同定界符、长度 >= 开围栏、其后仅空白）。
func (f Fence) Closes(line string) bool {
	if f.Len < MinFenceLen || (f.Marker != '`' && f.Marker != '~') {
		return false
	}
	trimmed := trimLineHorizontal(line)
	if len(trimmed) < f.Len || trimmed[0] != f.Marker {
		return false
	}
	run := countLeadingByte(trimmed, f.Marker)
	if run < f.Len {
		return false
	}
	return strings.TrimSpace(trimmed[run:]) == ""
}

// ParseFenceOpen 判断单行是否为合法围栏开行：
// 至少连续 3 个 '`' 或 '~'；若为反引号围栏，信息串内不得包含反引号（避免误判行内代码）。
// 允许列表项缩进产生的任意前导空格/制表符。
func ParseFenceOpen(line string) (Fence, bool) {
	trimmed := trimLineHorizontal(line)
	if len(trimmed) < MinFenceLen {
		return Fence{}, false
	}
	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return Fence{}, false
	}
	run := countLeadingByte(trimmed, ch)
	if run < MinFenceLen {
		return Fence{}, false
	}
	rest := trimmed[run:]
	if ch == '`' && strings.ContainsRune(rest, '`') {
		return Fence{}, false
	}
	return Fence{
		Marker: ch,
		Len:    run,
		Info:   strings.TrimSpace(rest),
	}, true
}

// ParseATXHeading 解析单行 ATX 标题，按 CommonMark 规则剥离可选的尾随闭合 `#` 序列（保留如 "C#" 的合法末尾 `#`）。
// maxLevel <= 0 或 > 6 时默认按 6 级处理。
func ParseATXHeading(line string, maxLevel int) (int, string, bool) {
	if maxLevel <= 0 || maxLevel > MaxHeadingLevel {
		maxLevel = MaxHeadingLevel
	}
	trimmed := trimLineHorizontal(line)
	if len(trimmed) < 2 || trimmed[0] != '#' {
		return 0, "", false
	}
	lvl := countLeadingByte(trimmed, '#')
	if lvl < 1 || lvl > maxLevel || lvl >= len(trimmed) {
		return 0, "", false
	}
	if trimmed[lvl] != ' ' && trimmed[lvl] != '\t' {
		return 0, "", false
	}
	text := stripOptionalClosingHashes(strings.TrimSpace(trimmed[lvl+1:]))
	if text == "" {
		return 0, "", false
	}
	return lvl, text, true
}

// LineKind 表示一行文本在围栏代码块状态机中的分类。
type LineKind uint8

const (
	// KindText 表示普通非围栏代码块文本行。
	KindText LineKind = iota
	// KindFenceOpen 表示打开围栏代码块的起始行。
	KindFenceOpen
	// KindFenceBody 表示围栏代码块内部的内容行。
	KindFenceBody
	// KindFenceClose 表示关闭当前围栏代码块的结束行。
	KindFenceClose
)

// Tracker 逐行跟踪 Markdown 围栏代码块的开闭状态（零值即可直接使用）。
type Tracker struct {
	open    Fence
	inFence bool
}

// Next 消费一行文本（可带或不带末尾 \r\n），更新内部围栏状态并返回该行分类。
func (t *Tracker) Next(line string) LineKind {
	if t.inFence {
		if t.open.Closes(line) {
			t.inFence = false
			t.open = Fence{}
			return KindFenceClose
		}
		return KindFenceBody
	}
	if f, ok := ParseFenceOpen(line); ok {
		t.inFence = true
		t.open = f
		return KindFenceOpen
	}
	return KindText
}

// InFence 报告当前是否处于未闭合的围栏代码块内部。
func (t *Tracker) InFence() bool {
	return t.inFence
}

// Open 返回当前打开的围栏信息（未处于围栏内时返回 false）。
func (t *Tracker) Open() (Fence, bool) {
	return t.open, t.inFence
}

// Reset 重置状态机至初始非围栏状态。
func (t *Tracker) Reset() {
	t.inFence = false
	t.open = Fence{}
}

// FirstH1 返回 Markdown 中围栏代码块之外的第一个一级 ATX 标题文本，未找到返回空字符串。
func FirstH1(markdown string) string {
	var tr Tracker
	for line := range strings.SplitSeq(markdown, "\n") {
		if tr.Next(line) != KindText {
			continue
		}
		if lvl, text, ok := ParseATXHeading(line, 1); ok && lvl == 1 {
			return text
		}
	}
	return ""
}

func trimLineHorizontal(line string) string {
	s := strings.TrimRight(line, "\r\n")
	return strings.TrimLeft(s, " \t")
}

func countLeadingByte(s string, target byte) int {
	n := 0
	for n < len(s) && s[n] == target {
		n++
	}
	return n
}

func stripOptionalClosingHashes(s string) string {
	if s == "" || s[len(s)-1] != '#' {
		return s
	}
	end := len(s)
	for end > 0 && s[end-1] == '#' {
		end--
	}
	if end == 0 {
		return ""
	}
	if s[end-1] == ' ' || s[end-1] == '\t' {
		return strings.TrimRight(s[:end], " \t")
	}
	return s
}
