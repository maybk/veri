package brave

import (
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/textnorm"
)

// normalizeBraveLang 规整语言标签为 Brave 支持的枚举（如 zh-hans / zh-hant / en-gb / pt-br 或基础码），防止触发 HTTP 422。
func normalizeBraveLang(raw string) string {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	if idx := strings.IndexAny(cleaned, ".@;"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	cleaned = strings.ReplaceAll(cleaned, "_", "-")
	if cleaned == "" {
		return ""
	}
	if cleaned == "zh" || strings.HasPrefix(cleaned, "zh-") {
		for _, sub := range strings.Split(cleaned, "-")[1:] {
			if sub == "hant" || sub == "tw" || sub == "hk" || sub == "mo" {
				return "zh-hant"
			}
		}
		return "zh-hans"
	}
	switch cleaned {
	case "en-gb", "pt-br", "pt-pt":
		return cleaned
	default:
		base, _, _ := strings.Cut(cleaned, "-")
		return base
	}
}

func exceedsBraveQueryLimits(q string) bool {
	if textnorm.RuneCount(q) > maxQueryRunes {
		return true
	}
	return len(strings.Fields(q)) > maxQueryWords
}

func buildBraveQueryOrGoggles(baseQuery string, sites, excludeSites []string) (string, []string) {
	var cleanSites, cleanExcludes []string
	for _, s := range sites {
		if st := strings.TrimSpace(s); st != "" {
			cleanSites = append(cleanSites, st)
		}
	}
	for _, ex := range excludeSites {
		if st := strings.TrimSpace(ex); st != "" {
			cleanExcludes = append(cleanExcludes, st)
		}
	}
	if len(cleanSites) == 0 && len(cleanExcludes) == 0 {
		return baseQuery, nil
	}

	parts := []string{baseQuery}
	if len(cleanSites) == 1 {
		parts = append(parts, "site:"+cleanSites[0])
	} else if len(cleanSites) > 1 {
		clauses := make([]string, 0, len(cleanSites))
		for _, s := range cleanSites {
			clauses = append(clauses, "site:"+s)
		}
		parts = append(parts, "("+strings.Join(clauses, " OR ")+")")
	}
	for _, ex := range cleanExcludes {
		parts = append(parts, "-site:"+ex)
	}

	composed := strings.Join(parts, " ")
	if !exceedsBraveQueryLimits(composed) {
		return composed, nil
	}

	var goggles []string
	if len(cleanSites) > 0 {
		lines := make([]string, 0, len(cleanSites)+1)
		lines = append(lines, "$discard")
		for _, s := range cleanSites {
			lines = append(lines, "$site="+s)
		}
		goggles = append(goggles, strings.Join(lines, "\n"))
	}
	if len(cleanExcludes) > 0 {
		lines := make([]string, 0, len(cleanExcludes))
		for _, ex := range cleanExcludes {
			lines = append(lines, "$discard,site="+ex)
		}
		goggles = append(goggles, strings.Join(lines, "\n"))
	}
	return baseQuery, goggles
}

func formatBraveFreshness(f *core.Freshness, now time.Time) string {
	if f == nil {
		return ""
	}
	if f.IsRelative() && f.N == 1 {
		switch f.Unit {
		case core.UnitDay:
			return "pd"
		case core.UnitWeek:
			return "pw"
		case core.UnitMonth:
			return "pm"
		case core.UnitYear:
			return "py"
		}
	}
	start := f.After
	if start.IsZero() {
		start = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	end := f.Before
	if end.IsZero() {
		end = now.UTC()
	}
	return start.Format("2006-01-02") + "to" + end.Format("2006-01-02")
}
