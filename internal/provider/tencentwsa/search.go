package tencentwsa

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

// Search 调用 POST /SearchPro，通过 InspectBody 在重试与密钥池层识别 HTTP 200 内嵌业务错误，并执行 Pages 二次 JSON 解析。
func (p *Provider) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, core.NewError(core.CodeInvalidArgument, "query must not be empty").WithProvider(ProviderName)
	}

	var localWarnings []string
	wireReq := searchWireReq{Query: query}

	indOpt := textnorm.FirstNonEmpty(req.Options[spec.OptWSAIndustry], req.Options[spec.OptWSAIndustryAlias])
	modeOpt := textnorm.FirstNonEmpty(req.Options[spec.OptWSAMode], req.Options[spec.OptWSAModeAlias])
	if p.edition != spec.WSAEditionLite {
		wireReq.Cnt = roundUpStep(req.Count)
		if indOpt != "" {
			wireReq.Industry = indOpt
		}
		if modeOpt != "" {
			modeVal, err := strconv.Atoi(modeOpt)
			if err != nil || modeVal < 0 || modeVal > 2 {
				return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid tencent_wsa.mode %q: must be 0, 1, or 2", modeOpt)).
					WithProvider(ProviderName)
			}
			wireReq.Mode = &modeVal
		}
	} else if indOpt != "" || modeOpt != "" {
		localWarnings = append(localWarnings, "tencent_wsa: industry and mode options are ignored in lite edition")
	}

	if len(req.Filters.Sites) > 0 {
		wireReq.Site = strings.TrimSpace(req.Filters.Sites[0])
	} else if len(req.Filters.ExcludeSites) > 0 {
		limit := min(len(req.Filters.ExcludeSites), maxExcludeSites)
		wireReq.Site = "exclude:" + strings.Join(req.Filters.ExcludeSites[:limit], "|")
	}

	if req.Filters.Freshness != nil {
		wireReq.Freshness = formatWSAFreshness(req.Filters.Freshness, req.Filters.Freshness.ReferenceTime(time.Now()))
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/SearchPro",
		Timeout:      p.timeout,
		AuthHeader:   spec.HeaderAuthorization,
		AuthPrefix:   spec.AuthBearerPrefix,
		Body:         wireReq,
		ClassifyErr:  classifyHTTPError,
		InspectBody:  inspectWSABody,
	})
	if err != nil {
		return nil, err
	}

	var wireResp searchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Tencent WSA JSON", err).WithProvider(ProviderName)
	}

	if wireResp.Response.Error != nil && strings.TrimSpace(wireResp.Response.Error.Code) != "" {
		return nil, classifyWSACode(wireResp.Response.Error.Code, wireResp.Response.Error.Message)
	}

	if msg := strings.TrimSpace(wireResp.Response.Msg); msg != "" {
		localWarnings = append(localWarnings, "tencent_wsa: "+msg)
	}

	shanghai := textnorm.ShanghaiLocation()
	mapped := make([]core.Result, 0, len(wireResp.Response.Pages))
	for i, rawPage := range wireResp.Response.Pages {
		item, decErr := decodePageElement(rawPage)
		if decErr != nil {
			localWarnings = append(localWarnings, fmt.Sprintf("tencent_wsa: skipped malformed Pages[%d]", i))
			continue
		}
		snippet := textnorm.TruncateRunes(textnorm.CleanInline(item.Passage), spec.DefaultSnippetMaxRunes)
		summary := item.Content
		if strings.TrimSpace(summary) == "" {
			summary = item.Passage
		}
		var rel, auth *float64
		if item.Score != nil {
			rel = textnorm.ClampUnitFloat(*item.Score)
		}
		if item.AuthorityLevel != nil {
			auth = textnorm.ClampUnitFloat(*item.AuthorityLevel / maxAuthorityLevel)
		}
		var extra map[string]any
		if req.IncludeExtra {
			extra = buildWSAExtra(item)
		}
		mapped = append(mapped, core.Result{
			Title:     item.Title,
			URL:       item.URL,
			Site:      item.Site,
			Icon:      strings.TrimSpace(item.Favicon),
			Published: textnorm.ParseFlexibleTime(item.Date, shanghai),
			Snippet:   snippet,
			Summary:   summary,
			Relevance: rel,
			Authority: auth,
			Extra:     extra,
		})
	}

	cleaned, sanWarnings := core.SanitizeResults(ProviderName, mapped, req.Detail, req.IncludeExtra, urlnorm.Key)
	if req.Count > 0 && len(cleaned) > req.Count {
		cleaned = cleaned[:req.Count]
	}

	reqID := textnorm.FirstNonEmpty(wireResp.Response.RequestID, resp.RequestID)

	return &core.SearchOutcome{
		Results:   cleaned,
		Cost:      core.CloneCost(p.searchCost),
		RequestID: reqID,
		Warnings:  append(localWarnings, sanWarnings...),
	}, nil
}

// decodePageElement 兼容腾讯云字符串序列化 JSON 与直接对象结构。
func decodePageElement(raw jsontext.Value) (*pageWireItem, error) {
	var item pageWireItem
	switch raw.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		return nil, fmt.Errorf("empty page item")
	case jsontext.KindString:
		var innerJSON string
		if err := json.Unmarshal(raw, &innerJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(innerJSON), &item); err != nil {
			return nil, err
		}
		return &item, nil
	default:
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		return &item, nil
	}
}

func roundUpStep(count int) int {
	if count <= wsaCountSteps[0] {
		return wsaCountSteps[0]
	}
	for _, step := range wsaCountSteps {
		if count <= step {
			return step
		}
	}
	return wsaCountSteps[len(wsaCountSteps)-1]
}

// formatWSAFreshness 将时间窗转换为腾讯云格式，并按北京时间当天裁剪上限防止报 InvalidParameter。
func formatWSAFreshness(f *core.Freshness, now time.Time) string {
	sh := textnorm.ShanghaiLocation()
	todayCN := now.In(sh)
	todayFloor := time.Date(todayCN.Year(), todayCN.Month(), todayCN.Day(), 0, 0, 0, 0, sh)
	minDate := time.Date(1970, 1, 1, 0, 0, 0, 0, sh)

	if f.IsRelative() {
		switch f.Unit {
		case core.UnitDay:
			if f.N <= 30 {
				return fmt.Sprintf("d%d", f.N)
			}
		case core.UnitWeek:
			days := f.N * 7
			if days <= 30 {
				return fmt.Sprintf("d%d", days)
			}
		case core.UnitMonth:
			if f.N <= 12 {
				return fmt.Sprintf("m%d", f.N)
			}
		case core.UnitYear:
			if f.N <= 5 {
				return fmt.Sprintf("y%d", f.N)
			}
		}
	}

	start := f.After
	if start.IsZero() {
		start = minDate
	} else {
		start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, sh)
	}
	if start.Before(minDate) {
		start = minDate
	}
	if start.After(todayFloor) {
		start = todayFloor
	}

	end := f.Before
	if end.IsZero() {
		end = todayFloor
	} else {
		end = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, sh)
	}
	if end.After(todayFloor) {
		end = todayFloor
	}
	if end.Before(start) {
		end = start
	}
	return start.Format("2006-01-02") + "," + end.Format("2006-01-02")
}

func buildWSAExtra(item *pageWireItem) map[string]any {
	extra := make(map[string]any)
	if item.Pics != nil {
		extra["pics"] = item.Pics
	}
	if item.Images != nil {
		extra["images"] = item.Images
	}
	if item.Deeplinks != nil {
		extra["deeplinks"] = item.Deeplinks
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}
