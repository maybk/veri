package tavily

type searchWireReq struct {
	Query                string   `json:"query"`
	SearchDepth          string   `json:"search_depth"`
	MaxResults           int      `json:"max_results"`
	Topic                string   `json:"topic,omitempty"`
	TimeRange            string   `json:"time_range,omitempty"`
	StartDate            string   `json:"start_date,omitempty"`
	EndDate              string   `json:"end_date,omitempty"`
	IncludeDomains       []string `json:"include_domains,omitempty"`
	ExcludeDomains       []string `json:"exclude_domains,omitempty"`
	Language             string   `json:"language,omitempty"`
	FilterByLanguage     bool     `json:"filter_by_language,omitzero"`
	IncludeAnswer        bool     `json:"include_answer"`
	IncludePublishedDate bool     `json:"include_published_date"`
	IncludeFavicon       bool     `json:"include_favicon"`
	IncludeUsage         bool     `json:"include_usage"`
	IncludeRawContent    any      `json:"include_raw_content,omitzero"`
}

type wireUsage struct {
	Credits *float64 `json:"credits"`
}

type searchWireResp struct {
	RequestID string           `json:"request_id"`
	Results   []searchWireItem `json:"results"`
	Usage     *wireUsage       `json:"usage"`
}

type searchWireItem struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Content       string   `json:"content"`
	RawContent    string   `json:"raw_content"`
	Score         *float64 `json:"score"`
	PublishedDate string   `json:"published_date"`
	Favicon       string   `json:"favicon"`
}

type extractWireReq struct {
	URLs                 []string `json:"urls"`
	ExtractDepth         string   `json:"extract_depth"`
	Format               string   `json:"format"`
	IncludeFavicon       bool     `json:"include_favicon"`
	IncludePublishedDate bool     `json:"include_published_date"`
	IncludeUsage         bool     `json:"include_usage"`
	Timeout              float64  `json:"timeout,omitzero"`
}

type extractWireResp struct {
	RequestID     string              `json:"request_id"`
	Results       []extractWireItem   `json:"results"`
	FailedResults []extractFailedItem `json:"failed_results"`
	Usage         *wireUsage          `json:"usage"`
}

type extractWireItem struct {
	URL           string `json:"url"`
	Title         string `json:"title"`
	RawContent    string `json:"raw_content"`
	PublishedDate string `json:"published_date"`
	Favicon       string `json:"favicon"`
}

type extractFailedItem struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

type errorWireResp struct {
	Detail any `json:"detail"`
}
