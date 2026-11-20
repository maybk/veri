package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"veri/internal/textnorm"
)

// FreshnessUnit 表示相对时间窗的单位。
type FreshnessUnit string

const (
	// UnitDay 天（day / Nd / dN）。
	UnitDay FreshnessUnit = "d"
	// UnitWeek 周（week / Nw / wN）。
	UnitWeek FreshnessUnit = "w"
	// UnitMonth 月（month / Nm / mN）。
	UnitMonth FreshnessUnit = "m"
	// UnitYear 年（year / Ny / yN）。
	UnitYear FreshnessUnit = "y"
)

// Freshness 表示统一的时间窗过滤条件。
// 所有日历日期统一锚定在 UTC 零点，避免跨时区比较与夏令时偏移误差。
type Freshness struct {
	Raw     string        // 原始输入字符串
	Unit    FreshnessUnit // 相对窗口单位：d | w | m | y；绝对区间时为空
	N       int           // 相对窗口数值（1～365）
	After   time.Time     // 起始日期（含），UTC 零点；零值表示不限起点
	Before  time.Time     // 截止日期（含），UTC 零点；零值表示至今
	RefDate time.Time     // 解析时的日历基准日期（UTC 零点），供开区间格式化复用
}

// ReferenceTime 返回解析该时间窗时的基准日期；若未设置则回退到 fallback。
func (f *Freshness) ReferenceTime(fallback time.Time) time.Time {
	if f != nil && !f.RefDate.IsZero() {
		return f.RefDate
	}
	return fallback
}

// EndOfBeforeDay 返回截止日期当天的最后一纳秒（UTC 23:59:59.999999999）。
func (f *Freshness) EndOfBeforeDay() time.Time {
	if f == nil || f.Before.IsZero() {
		return time.Time{}
	}
	u := f.Before.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), time.UTC)
}

// IsRelative 判断是否为相对时间窗。
func (f *Freshness) IsRelative() bool {
	return f != nil && f.Unit != ""
}

// ApproximateDays 返回相对时间窗折算的大致天数（供分钟/天数类参数换算）。
func (f *Freshness) ApproximateDays() int {
	if f == nil || f.Unit == "" {
		return 0
	}
	switch f.Unit {
	case UnitDay:
		return f.N
	case UnitWeek:
		return f.N * 7
	case UnitMonth:
		return f.N * 30
	case UnitYear:
		return f.N * 365
	default:
		return f.N
	}
}

// ParseFreshness 解析 CLI / MCP 时间窗语法并校验合法性。
func ParseFreshness(raw string, now time.Time) (*Freshness, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	localToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	utcNow := now.UTC()
	utcToday := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day(), 0, 0, 0, 0, time.UTC)
	maxToday := localToday
	if utcToday.After(maxToday) {
		maxToday = utcToday
	}

	if strings.Contains(trimmed, "..") {
		return parseAbsoluteFreshness(trimmed, localToday, maxToday)
	}
	return parseRelativeFreshness(trimmed, localToday)
}

func parseRelativeFreshness(trimmed string, today time.Time) (*Freshness, error) {
	unit, n, err := parseRelativeSpec(trimmed)
	if err != nil {
		return nil, err
	}

	var after time.Time
	switch unit {
	case UnitDay:
		after = today.AddDate(0, 0, -n)
	case UnitWeek:
		after = today.AddDate(0, 0, -7*n)
	case UnitMonth:
		after = textnorm.SubtractMonthsClamped(today, n)
	case UnitYear:
		after = textnorm.SubtractMonthsClamped(today, n*12)
	}

	return &Freshness{
		Raw:     trimmed,
		Unit:    unit,
		N:       n,
		After:   after,
		RefDate: today,
	}, nil
}

func parseRelativeSpec(trimmed string) (FreshnessUnit, int, error) {
	lower := strings.ToLower(trimmed)
	switch lower {
	case "day":
		return UnitDay, 1, nil
	case "week":
		return UnitWeek, 1, nil
	case "month":
		return UnitMonth, 1, nil
	case "year":
		return UnitYear, 1, nil
	}

	const syntaxDetail = "expected day|week|month|year, <N>d|<N>w|<N>m|<N>y, or YYYY-MM-DD..YYYY-MM-DD"

	var unit FreshnessUnit
	var numPart string
	if last := FreshnessUnit(lower[len(lower)-1:]); isFreshnessUnit(last) && len(lower) > 1 && lower[0] >= '0' && lower[0] <= '9' {
		unit = last
		numPart = lower[:len(lower)-1]
	} else if first := FreshnessUnit(lower[:1]); isFreshnessUnit(first) {
		unit = first
		numPart = lower[1:]
	} else {
		return "", 0, invalidFreshnessErr(trimmed, syntaxDetail)
	}

	if numPart == "" {
		return unit, 1, nil
	}
	parsed, err := strconv.Atoi(numPart)
	if err != nil {
		return "", 0, invalidFreshnessErr(trimmed, syntaxDetail)
	}
	if parsed < 1 || parsed > 365 {
		return "", 0, invalidFreshnessErr(trimmed, "relative freshness count N must be an integer between 1 and 365")
	}
	return unit, parsed, nil
}

func isFreshnessUnit(u FreshnessUnit) bool {
	switch u {
	case UnitDay, UnitWeek, UnitMonth, UnitYear:
		return true
	default:
		return false
	}
}

func parseAbsoluteFreshness(trimmed string, refDate, maxAllowedToday time.Time) (*Freshness, error) {
	parts := strings.Split(trimmed, "..")
	if len(parts) != 2 || (parts[0] == "" && parts[1] == "") {
		return nil, invalidFreshnessErr(trimmed, "absolute range must be YYYY-MM-DD.., ..YYYY-MM-DD, or YYYY-MM-DD..YYYY-MM-DD")
	}

	var after, before time.Time
	var err error
	if parts[0] != "" {
		after, err = parseUTCDate(parts[0])
		if err != nil {
			return nil, invalidFreshnessErr(trimmed, "invalid start date, expected YYYY-MM-DD")
		}
		if after.Year() < 1970 {
			return nil, invalidFreshnessErr(trimmed, "start date must not be earlier than 1970-01-01")
		}
		if after.After(maxAllowedToday) {
			return nil, invalidFreshnessErr(trimmed, "start date must not be in the future")
		}
	}
	if parts[1] != "" {
		before, err = parseUTCDate(parts[1])
		if err != nil {
			return nil, invalidFreshnessErr(trimmed, "invalid end date, expected YYYY-MM-DD")
		}
		if before.Year() < 1970 {
			return nil, invalidFreshnessErr(trimmed, "end date must not be earlier than 1970-01-01")
		}
		if before.After(maxAllowedToday) {
			return nil, invalidFreshnessErr(trimmed, "end date must not be in the future")
		}
	}
	if !after.IsZero() && !before.IsZero() && after.After(before) {
		return nil, invalidFreshnessErr(trimmed, "start date must not be after end date")
	}

	return &Freshness{
		Raw:     trimmed,
		After:   after,
		Before:  before,
		RefDate: refDate,
	}, nil
}

func parseUTCDate(s string) (time.Time, error) {
	if len(s) != len("2006-01-02") {
		return time.Time{}, fmt.Errorf("invalid date length")
	}
	return time.ParseInLocation("2006-01-02", s, time.UTC)
}

func invalidFreshnessErr(raw, detail string) error {
	return NewError(CodeInvalidArgument, fmt.Sprintf("invalid freshness %q: %s", raw, detail)).
		WithHint(`Use day | week | month | year, 7d | 2w | 3m | 1y, or date ranges like 2026-08-01..2026-08-31.`).
		WithI18n("err.invalid_freshness", "hint.invalid_freshness").
		WithParam("field", "freshness").
		WithParam("value", raw)
}
