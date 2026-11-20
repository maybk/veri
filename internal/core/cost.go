package core

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
)

// MicrosUnit 表示 1 货币单位对应的微单位数（1 元 / 1 美元 = 1_000_000 微单位）。
const MicrosUnit int64 = 1_000_000

// maxPriceAmount 限定单次配置价格上限，防止微单位转换与累加发生 int64 溢出。
const maxPriceAmount = 1_000_000.0

const (
	// CurrencyCNY 人民币币种代码。
	CurrencyCNY = "CNY"
	// CurrencyUSD 美元币种代码。
	CurrencyUSD = "USD"
)

// Cost 使用整数微单位记录单次或汇总调用成本，避免浮点累积误差。
type Cost struct {
	Currency  string `json:"currency" jsonschema:"ISO currency code (CNY or USD)"`
	Micros    int64  `json:"micros" jsonschema:"Cost in micro-units (1 unit = 1,000,000 micros)"`
	Estimated bool   `json:"estimated" jsonschema:"True when computed from local price_* config"`
}

// AmountToMicros 将十进制金额四舍五入转换为整数微单位，拒绝非正数与非有限浮点数，并对有限超大值做饱和保护。
func AmountToMicros(amount float64) int64 {
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return 0
	}
	if amount >= float64(math.MaxInt64)/float64(MicrosUnit) {
		return math.MaxInt64
	}
	return int64(math.Round(amount * float64(MicrosUnit)))
}

// ParsePriceSpec 解析形如 "CNY:0.005" 或 "USD:0.008" 的单价配置字符串。
func ParsePriceSpec(spec string) (Cost, error) {
	cur, rawAmt, ok := strings.Cut(strings.TrimSpace(spec), ":")
	if !ok {
		return Cost{}, fmt.Errorf("invalid price spec %q: expected CURRENCY:AMOUNT (e.g. CNY:0.005)", spec)
	}
	cur = strings.ToUpper(strings.TrimSpace(cur))
	if cur != CurrencyCNY && cur != CurrencyUSD {
		return Cost{}, fmt.Errorf("unsupported currency %q in price spec %q: must be CNY or USD", cur, spec)
	}
	amt, err := strconv.ParseFloat(strings.TrimSpace(rawAmt), 64)
	if err != nil || amt < 0 || amt > maxPriceAmount || math.IsNaN(amt) || math.IsInf(amt, 0) {
		return Cost{}, fmt.Errorf("invalid price amount in %q", spec)
	}
	return Cost{
		Currency:  cur,
		Micros:    AmountToMicros(amt),
		Estimated: true,
	}, nil
}

// ParseOptionalPriceSpec 解析可选的单价覆盖字符串；为空白时返回 (nil, nil)。
func ParseOptionalPriceSpec(spec string) (*Cost, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	c, err := ParsePriceSpec(spec)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// CloneCost 返回可选成本指针的独立拷贝；入参为 nil 时返回 nil。
func CloneCost(c *Cost) *Cost {
	if c == nil {
		return nil
	}
	return new(*c)
}

// Format 将单笔成本格式化为紧凑人类可读字符串（如 "¥0.005" 或 "$0.007"）。
func (c Cost) Format() string {
	symbol := c.Currency + " "
	switch c.Currency {
	case CurrencyCNY:
		symbol = "¥"
	case CurrencyUSD:
		symbol = "$"
	}
	if c.Micros == 0 {
		return symbol + "0"
	}
	sign := ""
	micros := c.Micros
	if micros < 0 {
		sign = "-"
		if micros == math.MinInt64 {
			micros = math.MaxInt64
		} else {
			micros = -micros
		}
	}
	val := float64(micros) / float64(MicrosUnit)
	formatted := strconv.FormatFloat(val, 'f', 6, 64)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	return sign + symbol + formatted
}

// FormatCosts 将多币种成本列表格式化为组合展示文本；无成本记录时返回空字符串。
func FormatCosts(costs []Cost) string {
	if len(costs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(costs))
	for _, c := range costs {
		parts = append(parts, c.Format())
	}
	return strings.Join(parts, " + ")
}

// SumCosts 按币种分别汇总成本（饱和加法防止溢出）；任一分项为估算值则该币种标记 Estimated=true。
func SumCosts(items []*Cost) []Cost {
	byCurrency := make(map[string]*Cost)
	for _, item := range items {
		if item == nil {
			continue
		}
		cur := strings.ToUpper(strings.TrimSpace(item.Currency))
		if cur == "" {
			continue
		}
		agg, exists := byCurrency[cur]
		if !exists {
			byCurrency[cur] = &Cost{
				Currency:  cur,
				Micros:    item.Micros,
				Estimated: item.Estimated,
			}
			continue
		}
		if item.Micros > 0 && agg.Micros > math.MaxInt64-item.Micros {
			agg.Micros = math.MaxInt64
		} else {
			agg.Micros += item.Micros
		}
		agg.Estimated = agg.Estimated || item.Estimated
	}
	if len(byCurrency) == 0 {
		return []Cost{}
	}
	keys := slices.Sorted(maps.Keys(byCurrency))
	out := make([]Cost, 0, len(keys))
	for _, k := range keys {
		out = append(out, *byCurrency[k])
	}
	return out
}
