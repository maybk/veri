package parallel

type searchWireReq struct {
	SearchQueries    []string                `json:"search_queries"`
	Objective        string                  `json:"objective,omitempty"`
	Mode             string                  `json:"mode,omitempty"`
	AdvancedSettings *advancedSearchSettings `json:"advanced_settings,omitempty"`
}

type advancedSearchSettings struct {
	MaxResults   int           `json:"max_results,omitempty"`
	SourcePolicy *sourcePolicy `json:"source_policy,omitempty"`
	Location     string        `json:"location,omitempty"`
}

type sourcePolicy struct {
	IncludeDomains []string `json:"include_domains,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
	AfterDate      string   `json:"after_date,omitempty"`
}

type searchWireResp struct {
	SearchID string           `json:"search_id"`
	Results  []searchWireItem `json:"results"`
	Warnings []wireWarning    `json:"warnings,omitempty"`
	Usage    []wireUsageItem  `json:"usage,omitempty"`
}

type searchWireItem struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	PublishDate string   `json:"publish_date"`
	Excerpts    []string `json:"excerpts"`
}

type extractWireReq struct {
	URLs             []string                 `json:"urls"`
	AdvancedSettings *advancedExtractSettings `json:"advanced_settings,omitempty"`
}

type advancedExtractSettings struct {
	FullContent bool         `json:"full_content"`
	FetchPolicy *fetchPolicy `json:"fetch_policy,omitempty"`
}

type fetchPolicy struct {
	MaxAgeSeconds        int  `json:"max_age_seconds,omitempty"`
	TimeoutSeconds       int  `json:"timeout_seconds,omitempty"`
	DisableCacheFallback bool `json:"disable_cache_fallback,omitempty"`
}

type extractWireResp struct {
	ExtractID string             `json:"extract_id"`
	Results   []extractWireItem  `json:"results"`
	Errors    []extractWireError `json:"errors"`
	Warnings  []wireWarning      `json:"warnings,omitempty"`
	Usage     []wireUsageItem    `json:"usage,omitempty"`
}

type extractWireItem struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	PublishDate string   `json:"publish_date"`
	Excerpts    []string `json:"excerpts"`
	FullContent string   `json:"full_content"`
}

type extractWireError struct {
	URL            string `json:"url"`
	ErrorType      string `json:"error_type"`
	HTTPStatusCode *int   `json:"http_status_code"`
	Content        string `json:"content"`
}

type wireWarning struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type wireUsageItem struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type errorWireResp struct {
	Type  string           `json:"type"`
	Error *wireErrorDetail `json:"error"`
}

type wireErrorDetail struct {
	RefID   string `json:"ref_id"`
	Message string `json:"message"`
}
