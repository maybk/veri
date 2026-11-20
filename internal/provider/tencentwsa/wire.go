package tencentwsa

import "encoding/json/jsontext"

type searchWireReq struct {
	Query     string `json:"Query"`
	Mode      *int   `json:"Mode,omitempty"`
	Site      string `json:"Site,omitempty"`
	Cnt       int    `json:"Cnt,omitzero"`
	Industry  string `json:"Industry,omitempty"`
	Freshness string `json:"Freshness,omitempty"`
}

type searchWireResp struct {
	Response struct {
		Pages     []jsontext.Value `json:"Pages"`
		Query     string           `json:"Query"`
		Version   string           `json:"Version"`
		Msg       string           `json:"Msg"`
		RequestID string           `json:"RequestId"`
		Error     *wsaWireError    `json:"Error"`
	} `json:"Response"`
}

type wsaWireError struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

type pageWireItem struct {
	Title          string   `json:"title"`
	Date           string   `json:"date"`
	URL            string   `json:"url"`
	Passage        string   `json:"passage"`
	Content        string   `json:"content"`
	Site           string   `json:"site"`
	Favicon        string   `json:"favicon"`
	Score          *float64 `json:"score"`
	AuthorityLevel *float64 `json:"authority_level"`
	Pics           any      `json:"pics"`
	Images         any      `json:"images"`
	Deeplinks      any      `json:"deeplinks"`
}
