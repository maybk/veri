package term

import (
	"io"
	"math"
	"os"
	"strings"

	xterm "golang.org/x/term"
)

// Palette 控制终端 ANSI 着色，非 TTY、显式禁用或设置了 NO_COLOR 时自动退化为纯文本。
type Palette struct {
	Enabled bool
}

// TerminalFD 安全返回 *os.File 对应的终端文件描述符。
func TerminalFD(f *os.File) (int, bool) {
	if f == nil {
		return 0, false
	}
	fd := f.Fd()
	if fd > uintptr(math.MaxInt) {
		return 0, false
	}
	return int(fd), true
}

// IsTerminal 判断任意输入或输出流（底层为 *os.File 时）是否连接到交互式终端。
func IsTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	if !ok {
		return false
	}
	fd, valid := TerminalFD(f)
	return valid && xterm.IsTerminal(fd)
}

// DetectPalette 根据目标 Writer 是否为终端、forceNoColor 参数与 NO_COLOR / TERM 环境变量决定是否启用着色。
func DetectPalette(w io.Writer, forceNoColor bool, getenv func(string) string) Palette {
	if getenv == nil {
		getenv = os.Getenv
	}
	if forceNoColor || getenv("NO_COLOR") != "" || strings.EqualFold(getenv("TERM"), "dumb") {
		return Palette{Enabled: false}
	}
	return Palette{Enabled: IsTerminal(w)}
}

// Bold 加粗文本。
func (p Palette) Bold(s string) string { return p.wrap("\x1b[1m", s) }

// Dim 弱化次要元数据文本。
func (p Palette) Dim(s string) string { return p.wrap("\x1b[2m", s) }

// Cyan 青色高亮链接或标识。
func (p Palette) Cyan(s string) string { return p.wrap("\x1b[36m", s) }

// Green 绿色标记正常或已启用状态。
func (p Palette) Green(s string) string { return p.wrap("\x1b[32m", s) }

// Yellow 黄色标记警告或降级状态。
func (p Palette) Yellow(s string) string { return p.wrap("\x1b[33m", s) }

// Red 红色标记错误或阻断状态。
func (p Palette) Red(s string) string { return p.wrap("\x1b[31m", s) }

func (p Palette) wrap(code, s string) string {
	if !p.Enabled || s == "" {
		return s
	}
	return code + s + "\x1b[0m"
}
