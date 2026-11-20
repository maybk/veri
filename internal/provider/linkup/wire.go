package linkup

type searchWireReq struct {
	Q              string   `json:"q"`
	Depth          string   `json:"depth"`
	OutputType     string   `json:"outputType"`
	MaxResults     int      `json:"maxResults,omitempty"`
	IncludeDomains []string `json:"includeDomains,omitempty"`
	ExcludeDomains []string `json:"excludeDomains,omitempty"`
	FromDate       string   `json:"fromDate,omitempty"`
	ToDate         string   `json:"toDate,omitempty"`
	IncludeImages  bool     `json:"includeImages"`
}

type searchWireResp struct {
	Results []searchWireItem `json:"results"`
}

type searchWireItem struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Content     string `json:"content"`
	Favicon     string `json:"favicon,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
}

type fetchWireReq struct {
	URL               string `json:"url"`
	Mode              string `json:"mode,omitempty"`
	RenderJS          bool   `json:"renderJs"`
	IncludeRawContent bool   `json:"includeRawContent"`
	ExtractImages     bool   `json:"extractImages"`
}

type fetchWireResp struct {
	Markdown string `json:"markdown"`
}

type errorWireResp struct {
	StatusCode int             `json:"statusCode,omitempty"`
	Error      *errorWireInner `json:"error,omitempty"`
}

type errorWireInner struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
