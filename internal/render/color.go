package render

import (
	"io"
	"os"

	"veri/clikit/term"
)

// Palette 复用 clikit/term.Palette，控制终端 ANSI 着色。
type Palette = term.Palette

// IsTerminal 判断输入或输出流是否连接到交互式终端。
func IsTerminal(stream any) bool {
	return term.IsTerminal(stream)
}

// DetectPalette 根据目标 Writer 是否为终端、--no-color 参数与操作系统环境变量决定是否启用着色。
func DetectPalette(w io.Writer, forceNoColor bool) Palette {
	return DetectPaletteWithEnv(w, forceNoColor, os.Getenv)
}

// DetectPaletteWithEnv 使用注入的环境变量读取器判定终端 ANSI 着色策略。
func DetectPaletteWithEnv(w io.Writer, forceNoColor bool, getenv func(string) string) Palette {
	return term.DetectPalette(w, forceNoColor, getenv)
}
