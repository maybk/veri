package brave

type contextWireReq struct {
	Q                           string   `json:"q"`
	Count                       int      `json:"count"`
	MaximumNumberOfURLs         int      `json:"maximum_number_of_urls"`
	MaximumNumberOfTokensPerURL int      `json:"maximum_number_of_tokens_per_url"`
	ContextThresholdMode        string   `json:"context_threshold_mode,omitempty"`
	SearchLang                  string   `json:"search_lang,omitempty"`
	Freshness                   string   `json:"freshness,omitempty"`
	EnableSourceMetadata        bool     `json:"enable_source_metadata"`
	Operators                   bool     `json:"operators"`
	OperatorsFallback           bool     `json:"operators_fallback"`
	Goggles                     []string `json:"goggles,omitempty"`
}

type contextWireResp struct {
	Grounding struct {
		Generic []contextGenericItem `json:"generic"`
	} `json:"grounding"`
	Sources map[string]contextSourceMeta `json:"sources"`
}

type contextGenericItem struct {
	URL      string   `json:"url"`
	Title    string   `json:"title"`
	Snippets []string `json:"snippets"`
}

type contextSourceMeta struct {
	Title       string   `json:"title"`
	SiteName    string   `json:"site_name"`
	Hostname    string   `json:"hostname"`
	Favicon     string   `json:"favicon"`
	Description string   `json:"description"`
	Age         []string `json:"age"`
}

type webSearchWireResp struct {
	Query struct {
		Altered string `json:"altered"`
	} `json:"query"`
	Web struct {
		Results []webSearchWireItem `json:"results"`
	} `json:"web"`
}

type webSearchWireItem struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Description   string   `json:"description"`
	ExtraSnippets []string `json:"extra_snippets"`
	PageAge       string   `json:"page_age"`
	Age           string   `json:"age"`
	Language      string   `json:"language"`
	Subtype       string   `json:"subtype"`
	Profile       *struct {
		Name string `json:"name"`
	} `json:"profile"`
	MetaURL *struct {
		Favicon string `json:"favicon"`
	} `json:"meta_url"`
}

type errorWireResp struct {
	Error struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"error"`
}
