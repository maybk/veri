package tencentwsa

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"veri/internal/core"
	"veri/internal/provider/providertest"
)

func TestTencentWSADoubleJSONDecodeAndBusinessError(t *testing.T) {
	okPayload := providertest.LoadFixture(t, "search_ok.json")
	emptyPayload := providertest.LoadFixture(t, "search_empty.json")
	errPayload := providertest.LoadFixture(t, "error.json")

	mode := "ok"
	var captured searchWireReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.UnmarshalRead(r.Body, &captured)
		switch mode {
		case "err":
			_, _ = w.Write(errPayload)
		case "empty":
			_, _ = w.Write(emptyPayload)
		default:
			_, _ = w.Write(okPayload)
		}
	}))
	defer srv.Close()

	p, enabled, err := New(Options{Keys: []string{"wsa-k"}, Edition: "flagship", BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	if err != nil || !enabled {
		t.Fatalf("New: enabled=%v err=%v", enabled, err)
	}

	out, err := p.Search(context.Background(), &core.SearchRequest{
		Query:  "货币政策",
		Count:  12,
		Detail: core.DetailSummary,
		Filters: core.Filters{
			ExcludeSites: []string{"a.com", "b.com"},
		},
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	providertest.AssertSearchOutcomeInvariants(t, ProviderName, out)
	if captured.Cnt != 20 || captured.Site != "exclude:a.com|b.com" {
		t.Fatalf("unexpected wire request: %+v", captured)
	}
	if len(out.Results) != 1 || *out.Results[0].Authority != 0.8 || out.Cost != nil {
		t.Fatalf("unexpected mapped result or default cost: %+v cost=%+v", out.Results, out.Cost)
	}

	priced, _, err := New(Options{Keys: []string{"wsa-k"}, Edition: "flagship", BaseURL: srv.URL, PriceSearch: "CNY:0.026"}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatalf("New priced: %v", err)
	}
	pOut, err := priced.Search(context.Background(), &core.SearchRequest{Query: "货币政策", Count: 5})
	if err != nil || pOut.Cost == nil || pOut.Cost.Micros != 26000 {
		t.Fatalf("unexpected configured flagship cost: %+v err=%v", pOut.Cost, err)
	}

	mode = "empty"
	eOut, err := p.Search(context.Background(), &core.SearchRequest{Query: "货币政策"})
	if err != nil || len(eOut.Results) != 0 {
		t.Fatalf("unexpected empty result: %+v err=%v", eOut, err)
	}

	mode = "err"
	_, err = p.Search(context.Background(), &core.SearchRequest{Query: "货币政策"})
	if core.AsError(err).Code != core.CodeNotEnabled {
		t.Fatalf("expected ResourceNotFound -> not_enabled, got %v", err)
	}
	if classifyWSACode("AuthFailure.SecretIdNotFound", "").Code != core.CodeAuthFailed ||
		classifyWSACode("LimitExceeded.DailyQuota", "").Code != core.CodeRateLimited ||
		classifyWSACode("ResourceUnavailable.LowBalance", "").Code != core.CodeInsufficientBalance ||
		classifyWSACode("InvalidParameterValue.Cnt", "").Code != core.CodeInvalidArgument {
		t.Fatal("classifyWSACode prefix matching failed")
	}
}
