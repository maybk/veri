package deepseek

type searchWireReq struct {
	Query       string `json:"query"`
	Count       int    `json:"count,omitzero"`
	OriQuestion string `json:"ori_question,omitempty"`
	NeedContent bool   `json:"need_content,omitzero"`
}

type searchWireResp struct {
	Results []searchWireItem `json:"results"`
}

type searchWireItem struct {
	RerankScore *float64 `json:"rerank_score"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Snippet     string   `json:"snippet"`
	Summary     string   `json:"summary"`
	Content     string   `json:"content"`
	SiteName    *string  `json:"site_name"`
	SiteIcon    *string  `json:"site_icon"`
	Date        *string  `json:"date"`
	Authority   *int     `json:"authority"`
}

type fetchWireReq struct {
	URL string `json:"url"`
}

type fetchWireResp struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type errorWireResp struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}
