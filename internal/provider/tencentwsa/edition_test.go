package tencentwsa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/provider/providertest"
)

func TestTencentWSAEditionsFreshnessAndErrorCodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Response":{"Pages":[{"title":"DirectObj","url":"https://qq.com/1","passage":"p","pics":["u1"]}],"RequestId":"r1"}}`))
	}))
	defer srv.Close()

	// lite 版与 standard 版能力与参数校验
	pLite, ok, err := New(Options{Keys: []string{"k"}, Edition: "lite", BaseURL: srv.URL, PriceSearch: "CNY:0.014"}, providertest.NewTestAPIClient(t))
	if err != nil || !ok || pLite.Edition() != "lite" || pLite.Capabilities().MaxCount != 10 {
		t.Fatalf("unexpected lite provider: ok=%v err=%v", ok, err)
	}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	relW, _ := core.ParseFreshness("w2", now)
	out, err := pLite.Search(context.Background(), &core.SearchRequest{
		Query:        "腾讯",
		IncludeExtra: true,
		Options:      map[string]string{"tencent_wsa.industry": "news"},
		Filters:      core.Filters{Sites: []string{"qq.com"}, Freshness: relW},
	})
	if err != nil || len(out.Results) != 1 || out.Results[0].Extra["pics"] == nil {
		t.Fatalf("unexpected lite search: %+v err=%v", out, err)
	}

	pStd, _, _ := New(Options{Keys: []string{"k"}, Edition: "standard", BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	absF, _ := core.ParseFreshness("2026-01-01..2026-10-01", now)
	if _, err := pStd.Search(context.Background(), &core.SearchRequest{
		Query:   "金融",
		Count:   50,
		Options: map[string]string{"tencent_wsa.industry": "finance", "tencent_wsa.mode": "1"},
		Filters: core.Filters{Freshness: absF},
	}); err != nil {
		t.Fatalf("standard search failed: %v", err)
	}
	if _, err := pStd.Search(context.Background(), &core.SearchRequest{
		Query:   "金融",
		Options: map[string]string{"tencent_wsa.mode": "99"},
	}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatal("expected invalid mode to fail")
	}

	// 全业务错误码映射覆盖
	for code, want := range map[string]core.Code{
		"InvalidParameter":      core.CodeInvalidArgument,
		"UnauthorizedOperation": core.CodeAuthFailed,
		"ResourceNotFound":      core.CodeNotEnabled,
		"ResourceUnavailable":   core.CodeInsufficientBalance,
		"RequestLimitExceeded":  core.CodeRateLimited,
		"InternalError":         core.CodeUpstreamUnavailable,
		"UnknownCode":           core.CodeUpstreamBadResponse,
	} {
		if got := classifyWSACode(code, "").Code; got != want {
			t.Fatalf("WSA code %s -> %s, want %s", code, got, want)
		}
	}

	// 超长相对窗口自动转绝对日期区间覆盖
	longD := &core.Freshness{Unit: core.UnitDay, N: 90, After: now.AddDate(0, 0, -90)}
	if got := formatWSAFreshness(longD, now); len(got) != len("2006-01-02,2006-01-02") {
		t.Fatalf("expected d90 to fallback to absolute range, got %q", got)
	}
}
