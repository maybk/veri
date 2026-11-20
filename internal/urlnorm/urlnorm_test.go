package urlnorm

import "testing"

func TestKey(t *testing.T) {
	a := Key("HTTP://WWW.Example.com:80/docs/guide/?utm_source=twitter&b=2&a=1&spm=123#section-2")
	b := Key("https://example.com:443/docs/guide?a=1&b=2")
	if a != b {
		t.Fatalf("expected identical normalized keys, got %q vs %q", a, b)
	}
	if a != "https://example.com/docs/guide?a=1&b=2" {
		t.Fatalf("unexpected key %q", a)
	}

	// HTTP on port 443 is non-standard and must preserve explicit port.
	nonStd := Key("http://example.com:443/a")
	if nonStd != "https://example.com:443/a" {
		t.Fatalf("expected non-standard http:443 port preserved, got %q", nonStd)
	}

	rootA := Key("http://example.com")
	rootB := Key("https://www.example.com/")
	if rootA != rootB || rootA != "https://example.com/" {
		t.Fatalf("unexpected root keys: %q vs %q", rootA, rootB)
	}

	// Semantic ref/from query params must NOT be stripped so multi-version docs do not collide.
	refV1 := Key("https://docs.example.com/api?ref=v1&msclkid=ads1")
	refV2 := Key("https://docs.example.com/api?ref=v2&gbraid=ads2")
	if refV1 == refV2 || refV1 != "https://docs.example.com/api?ref=v1" {
		t.Fatalf("expected ref version preserved and ad params stripped, got %q vs %q", refV1, refV2)
	}

	// Malformed percent-encoding or semicolon query must preserve RawQuery instead of silently dropping params.
	semiA := Key("https://legacy.example.com/doc?a=1;b=2")
	semiB := Key("https://legacy.example.com/doc?a=1;b=3")
	if semiA == semiB {
		t.Fatalf("semicolon-separated queries must not collide: %q vs %q", semiA, semiB)
	}

	// StripTrackingParams preserves original host/trailing-slash/fragment while stripping tracking params.
	stripped := StripTrackingParams("http://www.example.com/post/?id=42&utm_medium=rss&fbclid=xyz#top")
	if stripped != "http://www.example.com/post/?id=42#top" {
		t.Fatalf("unexpected StripTrackingParams: %q", stripped)
	}
	untouched := "https://www.example.com/post/?b=2&a=1#sec"
	if got := StripTrackingParams(untouched); got != untouched {
		t.Fatalf("expected URL without tracking params untouched, got %q", got)
	}
}

func TestIsSafeHTTPURLAndDomainMatch(t *testing.T) {
	if _, ok := IsSafeHTTPURL("https://user:pass@example.com/a"); ok {
		t.Fatal("expected URL with userinfo to be rejected")
	}
	if _, ok := IsSafeHTTPURL("javascript:alert(1)"); ok {
		t.Fatal("expected javascript scheme to be rejected")
	}
	if _, ok := IsSafeHTTPURL("https://docs.python.org/3/library/asyncio.html"); !ok {
		t.Fatal("expected valid https URL")
	}

	if !MatchesAnyDomain("https://docs.python.org/3/library/io.html", []string{"python.org"}) {
		t.Fatal("expected subdomain to match parent domain")
	}
	if MatchesAnyDomain("https://notpython.org/a", []string{"python.org"}) {
		t.Fatal("suffix collision must not match")
	}
	if !MatchesAnyDomain("https://example.com/Docs/API", []string{"https://example.com:443/docs"}) {
		t.Fatal("expected case-insensitive path prefix and stripped scheme/port pattern to match")
	}
	if MatchesAnyDomain("https://example.com/blog/post", []string{"example.com/docs"}) {
		t.Fatal("expected non-matching path prefix to fail")
	}

	cleaned := CleanDomainFilters([]string{" https://*.GitHub.com/golang/go/ ", "github.com/golang/go"})
	if len(cleaned) != 1 || cleaned[0] != "github.com/golang/go" {
		t.Fatalf("unexpected CleanDomainFilters: %v", cleaned)
	}
}
