package exa

// searchContentsSpec 严格保持在 Exa type="lite" 亦允许的白名单字段内（不附带 maxAgeHours / livecrawlTimeout / summary）。
type searchContentsSpec struct {
	Highlights any `json:"highlights,omitzero"`
	Text       any `json:"text,omitzero"`
}

type wireHighlightsSpec struct {
	Query string `json:"query,omitempty"`
}

type wireTextSpec struct {
	MaxCharacters int `json:"maxCharacters,omitzero"`
}

type searchWireReq struct {
	Query              string             `json:"query"`
	Type               string             `json:"type"`
	NumResults         int                `json:"numResults"`
	Category           string             `json:"category,omitempty"`
	IncludeDomains     []string           `json:"includeDomains,omitempty"`
	ExcludeDomains     []string           `json:"excludeDomains,omitempty"`
	StartPublishedDate string             `json:"startPublishedDate,omitempty"`
	EndPublishedDate   string             `json:"endPublishedDate,omitempty"`
	Contents           searchContentsSpec `json:"contents"`
}

type searchWireResp struct {
	RequestID   string           `json:"requestId"`
	Results     []searchWireItem `json:"results"`
	Notices     []wireWarning    `json:"notices"`
	Warnings    []wireWarning    `json:"warnings"`
	CostDollars *wireCostDollars `json:"costDollars"`
}

type searchWireItem struct {
	Title           string    `json:"title"`
	URL             string    `json:"url"`
	PublishedDate   string    `json:"publishedDate"`
	Author          string    `json:"author"`
	Favicon         string    `json:"favicon"`
	Image           string    `json:"image"`
	Highlights      []string  `json:"highlights"`
	HighlightScores []float64 `json:"highlightScores"`
	Text            string    `json:"text"`
}

type wireWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type wireCostDollars struct {
	Total float64 `json:"total"`
}

type contentsWireReq struct {
	URLs             []string `json:"urls"`
	Text             bool     `json:"text"`
	MaxAgeHours      *int     `json:"maxAgeHours,omitempty"`
	LivecrawlTimeout int      `json:"livecrawlTimeout,omitzero"`
}

type contentsWireResp struct {
	RequestID   string             `json:"requestId"`
	Results     []contentsWireItem `json:"results"`
	Statuses    []contentsStatus   `json:"statuses"`
	CostDollars *wireCostDollars   `json:"costDollars"`
}

type contentsWireItem struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	PublishedDate string `json:"publishedDate"`
	Author        string `json:"author"`
	Text          string `json:"text"`
}

type contentsStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Tag            string `json:"tag"`
		HTTPStatusCode int    `json:"httpStatusCode"`
	} `json:"error"`
}

type errorWireResp struct {
	RequestID string `json:"requestId"`
	Error     string `json:"error"`
	Tag       string `json:"tag"`
}
