package term

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	xterm "golang.org/x/term"
)

const maxPipedSecretBytes = 64 << 10

// ErrInterrupted 表示用户在交互式终端主动取消了输入（如按下 Ctrl+C、Ctrl+D 或 Esc）。
var ErrInterrupted = errors.New("interrupted")

// ReadSecret 从交互式终端无回显读取敏感凭据，或在 forceStdin / 非交互管道模式下从 stdin 有界读取；
// 支持 Context 取消信号立即中断并恢复终端回显状态。
func ReadSecret(ctx context.Context, stdin io.Reader, stderr io.Writer, prompt string, forceStdin bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if stdin == nil {
		return "", errors.New("stdin is nil")
	}
	if forceStdin || !IsTerminal(stdin) {
		return readPipedSecret(ctx, stdin)
	}

	f, ok := stdin.(*os.File)
	if !ok {
		return "", errors.New("interactive secret input requires *os.File")
	}
	fd, valid := TerminalFD(f)
	if !valid {
		return "", errors.New("invalid terminal file descriptor")
	}

	oldState, err := xterm.MakeRaw(fd)
	if err != nil {
		return "", fmt.Errorf("failed to switch terminal to raw mode: %w", err)
	}
	if stderr != nil && prompt != "" {
		_, _ = fmt.Fprint(stderr, prompt)
	}

	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(func() {
			_ = xterm.Restore(fd, oldState)
			if stderr != nil && prompt != "" {
				_, _ = fmt.Fprintln(stderr)
			}
		})
	}
	defer restore()

	type secretResult struct {
		val string
		err error
	}
	ch := make(chan secretResult, 1)
	go func() {
		val, rErr := readRawSecretLine(f)
		ch <- secretResult{val: val, err: rErr}
	}()

	select {
	case <-ctx.Done():
		_ = f.SetReadDeadline(time.Now())
		restore()
		return "", ctx.Err()
	case res := <-ch:
		restore()
		if res.err != nil {
			if errors.Is(res.err, ErrInterrupted) {
				return "", ErrInterrupted
			}
			return "", fmt.Errorf("failed to read secret from terminal: %w", res.err)
		}
		return res.val, nil
	}
}

func readPipedSecret(ctx context.Context, stdin io.Reader) (string, error) {
	type pipedResult struct {
		raw []byte
		err error
	}
	ch := make(chan pipedResult, 1)
	go func() {
		raw, err := io.ReadAll(io.LimitReader(stdin, maxPipedSecretBytes))
		ch <- pipedResult{raw: raw, err: err}
	}()

	select {
	case <-ctx.Done():
		if f, ok := stdin.(*os.File); ok {
			_ = f.SetReadDeadline(time.Now())
		}
		return "", ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return "", fmt.Errorf("failed to read secret from stdin: %w", res.err)
		}
		return strings.TrimSpace(string(res.raw)), nil
	}
}

// readRawSecretLine 在原始模式下读取单行敏感输入，统一兼容三端 Ctrl+C / Ctrl+D / Esc 取消、退格删字符及粘贴控制序列过滤。
func readRawSecretLine(r io.Reader) (string, error) {
	var chunk [256]byte
	var out []byte

	for {
		n, err := r.Read(chunk[:])
		if n > 0 {
			data := chunk[:min(n, len(chunk))]
			for i := 0; i < len(data); i++ {
				b := data[i]
				switch b {
				case 0x03: // Ctrl+C (ETX)
					return "", ErrInterrupted
				case 0x04: // Ctrl+D (EOT)
					if len(out) == 0 {
						return "", ErrInterrupted
					}
					return strings.TrimSpace(string(out)), nil
				case 0x1a: // Ctrl+Z (SUB)
					if len(out) == 0 {
						return "", ErrInterrupted
					}
				case '\r', '\n':
					return strings.TrimSpace(string(out)), nil
				case 0x7f, '\b': // Backspace / DEL
					if len(out) > 0 {
						_, size := utf8.DecodeLastRune(out)
						if size <= 0 {
							size = 1
						}
						out = out[:len(out)-size]
					}
				case 0x15: // Ctrl+U 清空当前行
					out = out[:0]
				case 0x1b: // ESC 或终端控制序列（方向键、Bracketed Paste 等）
					rest := data[i+1:]
					if len(rest) > 0 && rest[0] == '[' {
						skip := 1
						for skip < len(rest) && rest[skip] >= 0x20 && rest[skip] <= 0x3f {
							skip++
						}
						if skip < len(rest) && rest[skip] >= 0x40 && rest[skip] <= 0x7e {
							skip++
						}
						i += skip
						continue
					}
					if len(rest) > 0 && rest[0] == 'O' {
						skip := 1
						if len(rest) > 1 && rest[1] >= 0x20 && rest[1] <= 0x7e {
							skip++
						}
						i += skip
						continue
					}
					if len(rest) == 0 && len(out) == 0 {
						return "", ErrInterrupted
					}
				default:
					if b < 0x20 {
						continue
					}
					if len(out) >= maxPipedSecretBytes {
						return "", errors.New("secret input exceeds maximum length")
					}
					out = append(out, b)
				}
			}
		}

		if n == 0 && err == nil {
			if len(out) == 0 {
				return "", ErrInterrupted
			}
			return strings.TrimSpace(string(out)), nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(out) == 0 {
					return "", ErrInterrupted
				}
				return strings.TrimSpace(string(out)), nil
			}
			return "", err
		}
	}
}

// Confirm 在交互式终端输出提示并等待用户输入 y / yes 确认；非交互终端返回错误以防止挂起，支持 Context 取消立即退出。
func Confirm(ctx context.Context, stdin io.Reader, stderr io.Writer, prompt string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !IsTerminal(stdin) {
		return false, errors.New("confirmation requires an interactive terminal")
	}
	if stderr != nil && prompt != "" {
		_, _ = fmt.Fprint(stderr, prompt)
	}

	type confirmResult struct {
		line string
		err  error
	}
	ch := make(chan confirmResult, 1)
	go func() {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		ch <- confirmResult{line: line, err: err}
	}()

	select {
	case <-ctx.Done():
		if f, ok := stdin.(*os.File); ok {
			_ = f.SetReadDeadline(time.Now())
		}
		if stderr != nil && prompt != "" {
			_, _ = fmt.Fprintln(stderr)
		}
		return false, ctx.Err()
	case res := <-ch:
		if strings.ContainsRune(res.line, '\x03') {
			if stderr != nil && prompt != "" {
				_, _ = fmt.Fprintln(stderr)
			}
			return false, ErrInterrupted
		}
		if errors.Is(res.err, io.EOF) && !strings.HasSuffix(res.line, "\n") && stderr != nil && prompt != "" {
			_, _ = fmt.Fprintln(stderr)
		}
		ans := strings.ToLower(strings.TrimSpace(res.line))
		return ans == "y" || ans == "yes", nil
	}
}
