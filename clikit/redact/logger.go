package redact

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// NewLogger 构造对日志消息、字符串属性与错误对象自动执行密钥及敏感 URL 脱敏的 *slog.Logger。
func NewLogger(w io.Writer, logLevel, logFormat string, secrets []string) *slog.Logger {
	level := slog.LevelWarn
	switch strings.ToLower(strings.TrimSpace(logLevel)) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "error":
		level = slog.LevelError
	}
	scrubber := NewScrubber(secrets)
	cleanStr := func(s string) string {
		return ScrubWith(s, scrubber)
	}

	opts := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			v := attr.Value.Resolve()
			switch v.Kind() {
			case slog.KindString:
				attr.Value = slog.StringValue(cleanStr(v.String()))
			case slog.KindAny:
				switch x := v.Any().(type) {
				case error:
					if x != nil {
						attr.Value = slog.StringValue(cleanStr(x.Error()))
					}
				case fmt.Stringer:
					if x != nil {
						attr.Value = slog.StringValue(cleanStr(x.String()))
					}
				}
			}
			return attr
		},
	}
	if strings.EqualFold(strings.TrimSpace(logFormat), "json") {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
