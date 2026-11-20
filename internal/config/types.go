package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Duration 包装 time.Duration，支持 TOML 字符串与 JSON 文本格式（如 "10s", "500ms"）。
type Duration struct {
	time.Duration
}

// UnmarshalText 实现 encoding.TextUnmarshaler。
func (d *Duration) UnmarshalText(text []byte) error {
	val, err := time.ParseDuration(strings.TrimSpace(string(text)))
	if err != nil || val < 0 {
		return fmt.Errorf("invalid duration %q", string(text))
	}
	d.Duration = val
	return nil
}

// MarshalText 实现 encoding.TextMarshaler。
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// ByteSize 表示字节数上限，支持 "5MB"、"512KB"、"1048576" 等可读格式。
type ByteSize int64

// ParseByteSize 解析可读字节大小字符串并防范 int64 乘法溢出。
func ParseByteSize(raw string) (ByteSize, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return 0, fmt.Errorf("empty byte size")
	}
	type unit struct {
		suffix string
		mult   int64
	}
	units := []unit{
		{"GIB", 1 << 30}, {"GB", 1 << 30},
		{"MIB", 1 << 20}, {"MB", 1 << 20},
		{"KIB", 1 << 10}, {"KB", 1 << 10},
		{"B", 1},
	}
	mult := int64(1)
	numPart := s
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			mult = u.mult
			numPart = strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			break
		}
	}
	val, err := strconv.ParseInt(numPart, 10, 64)
	if err != nil || val <= 0 || val > math.MaxInt64/mult {
		return 0, fmt.Errorf("invalid byte size %q", raw)
	}
	return ByteSize(val * mult), nil
}

// UnmarshalText 实现 encoding.TextUnmarshaler。
func (b *ByteSize) UnmarshalText(text []byte) error {
	val, err := ParseByteSize(string(text))
	if err != nil {
		return err
	}
	*b = val
	return nil
}

// MarshalText 实现 encoding.TextMarshaler。
func (b ByteSize) MarshalText() ([]byte, error) {
	v := int64(b)
	switch {
	case v > 0 && v%(1<<20) == 0:
		return []byte(fmt.Sprintf("%dMB", v/(1<<20))), nil
	case v > 0 && v%(1<<10) == 0:
		return []byte(fmt.Sprintf("%dKB", v/(1<<10))), nil
	default:
		return []byte(strconv.FormatInt(v, 10)), nil
	}
}
