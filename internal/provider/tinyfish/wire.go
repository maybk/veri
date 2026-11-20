package tinyfish

type searchWireResp struct {
	Results []searchWireItem `json:"results"`
}

type searchWireItem struct {
	Position     int      `json:"position"`
	Title        string   `json:"title"`
	URL          string   `json:"url"`
	Snippet      string   `json:"snippet"`
	SiteName     string   `json:"site_name"`
	Date         *string  `json:"date"`
	Publisher    *string  `json:"publisher"`
	Authors      []string `json:"authors"`
	Venue        *string  `json:"venue"`
	Year         *int     `json:"year"`
	CitedByCount *int     `json:"cited_by_count"`
	PDFURL       *string  `json:"pdf_url"`
}

type fetchWireReq struct {
	URLs            []string `json:"urls"`
	Format          string   `json:"format"`
	TTL             *int     `json:"ttl,omitempty"`
	PerURLTimeoutMS int      `json:"per_url_timeout_ms,omitzero"`
}

type fetchWireResp struct {
	Results []fetchWireItem  `json:"results"`
	Errors  []fetchWireError `json:"errors"`
}

type fetchWireItem struct {
	URL           string  `json:"url"`
	FinalURL      string  `json:"final_url"`
	Title         *string `json:"title"`
	PublishedDate *string `json:"published_date"`
	Text          string  `json:"text"`
}

type fetchWireError struct {
	URL    string `json:"url"`
	Error  string `json:"error"`
	Status *int   `json:"status"`
}

type apiErrorDetails struct {
	Reason string `json:"reason"`
}

type apiErrorWireResp struct {
	Error struct {
		Code    string           `json:"code"`
		Message string           `json:"message"`
		Details *apiErrorDetails `json:"details,omitempty"`
	} `json:"error"`
}
