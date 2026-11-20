package textnorm

import (
	"strconv"
	"strings"
	"time"
)

// fixedShanghai 使用固定 UTC+8 表示北京时间，不依赖宿主机 tzdata 文件，确保纯静态二进制跨平台稳定。
var fixedShanghai = time.FixedZone("Asia/Shanghai", 8*3600)

// ShanghaiLocation 返回中国标准时间时区（UTC+8）。
func ShanghaiLocation() *time.Location {
	return fixedShanghai
}

var (
	zonedTimeLayouts = []string{
		time.RFC3339Nano,
		time.RFC3339,
		time.RFC1123Z,
		time.RFC1123,
		"Mon, 2 Jan 2006 15:04:05 MST",
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"2006-01-02T15:04:05-0700",
	}
	unzonedTimeLayouts = []string{
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006/01/02 15:04:05",
		"2006/1/2 15:04:05",
		"2006-1-2 15:04:05",
		"2006-01-02 15:04",
		"2006/01/02 15:04",
		"2006-01-02",
		"2006-1-2",
		"2006/01/02",
		"2006/1/2",
		"January 2, 2006",
		"January 02, 2006",
		"Jan 2, 2006",
		"Jan 02, 2006",
		"2006年01月02日",
		"2006年1月2日",
	}
)

// SubtractMonthsClamped 按日历月份回退并将日期钳制在目标月最大天数内（如 2026-03-31 减 1 个月 = 2026-02-28）。
func SubtractMonthsClamped(base time.Time, months int) time.Time {
	firstOfBaseMonth := time.Date(base.Year(), base.Month(), 1, 0, 0, 0, 0, time.UTC)
	targetFirst := firstOfBaseMonth.AddDate(0, -months, 0)
	maxDay := time.Date(targetFirst.Year(), targetFirst.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	day := min(base.Day(), maxDay)
	return time.Date(targetFirst.Year(), targetFirst.Month(), day, 0, 0, 0, 0, time.UTC)
}

// ParseFlexibleTime 宽容解析各厂商返回的绝对或相对日期时间字符串，并统一转换为 UTC。
func ParseFlexibleTime(raw string, defaultLoc *time.Location) *time.Time {
	return ParseFlexibleTimeAt(raw, defaultLoc, time.Now())
}

// ParseFlexibleTimeAt 按给定参考时刻宽容解析绝对日期时间或英文相对时间（如 "2 days ago"、"1 month ago"、"107d ago"）。
func ParseFlexibleTimeAt(raw string, defaultLoc *time.Location, now time.Time) *time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	loc := defaultLoc
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range zonedTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil && !t.IsZero() && t.Year() >= 1970 {
			return new(t.UTC())
		}
	}
	for _, layout := range unzonedTimeLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil && !t.IsZero() && t.Year() >= 1970 {
			return new(t.UTC())
		}
	}
	if now.IsZero() {
		return nil
	}
	calRef := now
	if defaultLoc != nil {
		calRef = now.In(defaultLoc)
	}
	refDay := time.Date(calRef.Year(), calRef.Month(), calRef.Day(), 0, 0, 0, 0, time.UTC)
	return parseRelativeAge(s, now.UTC(), refDay)
}

func parseRelativeAge(raw string, nowUTC, refDay time.Time) *time.Time {
	lower := strings.ToLower(strings.Join(strings.Fields(raw), " "))
	lower = strings.Trim(lower, ".,;()[]")

	switch lower {
	case "just now", "now":
		return new(nowUTC)
	case "today":
		return new(refDay)
	case "yesterday":
		return new(refDay.AddDate(0, 0, -1))
	}

	rawBody, hasAgo := strings.CutSuffix(lower, "ago")
	if !hasAgo {
		return nil
	}
	body := strings.TrimSpace(rawBody)
	for _, prefix := range []string{"about ", "approx. ", "approx ", "approximately ", "over ", "almost "} {
		if after, ok := strings.CutPrefix(body, prefix); ok {
			body = strings.TrimSpace(after)
			break
		}
	}
	if body == "" {
		return nil
	}

	var n int
	var unit string
	fields := strings.Fields(body)
	switch len(fields) {
	case 2:
		if fields[0] == "a" || fields[0] == "an" {
			n = 1
		} else {
			v, err := strconv.Atoi(fields[0])
			if err != nil {
				return nil
			}
			n = v
		}
		unit = fields[1]
	case 1:
		tok := fields[0]
		split := 0
		for split < len(tok) && tok[split] >= '0' && tok[split] <= '9' {
			split++
		}
		if split == 0 || split == len(tok) {
			return nil
		}
		v, err := strconv.Atoi(tok[:split])
		if err != nil {
			return nil
		}
		n = v
		unit = tok[split:]
	default:
		return nil
	}

	if n < 0 || n > 10000 {
		return nil
	}

	var t time.Time
	switch unit {
	case "s", "sec", "secs", "second", "seconds":
		t = nowUTC.Add(-time.Duration(n) * time.Second)
	case "m", "min", "mins", "minute", "minutes":
		t = nowUTC.Add(-time.Duration(n) * time.Minute)
	case "h", "hr", "hrs", "hour", "hours":
		t = nowUTC.Add(-time.Duration(n) * time.Hour)
	case "d", "day", "days":
		t = refDay.AddDate(0, 0, -n)
	case "w", "wk", "wks", "week", "weeks":
		t = refDay.AddDate(0, 0, -7*n)
	case "mo", "mos", "month", "months":
		t = SubtractMonthsClamped(refDay, n)
	case "y", "yr", "yrs", "year", "years":
		t = SubtractMonthsClamped(refDay, n*12)
	default:
		return nil
	}
	if t.Year() < 1970 {
		return nil
	}
	return &t
}
