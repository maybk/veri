package router

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
	"veri/internal/urlnorm"
)

func TestFusionContentFingerprintDedupAndAntiDoubleCounting(t *testing.T) {
	t1 := time.Date(2026, 11, 19, 16, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 20, 5, 40, 0, 0, time.UTC)
	auth75, auth80 := 0.75, 0.80

	phoenixCopy1 := `# Anthropic七位创始人IPO套现近20亿美元
2026-11-20 13:36:24 来源：凤凰网
凤凰网科技讯 11月20日，据TechCrunch报道，Anthropic（ANT）七位联合创始人在公司首次公开募股中合计出售约2364.86万股股票。按照每股83美元的IPO发行价计算，相关股份价值约19.63亿美元，平均每人套现约2.8亿美元。美国证券交易委员会披露的最终招股书显示，七位创始人此次出售的股份数量相同，均为337.84万股，对应售股金额约2.804亿美元。`

	phoenixCopy2 := `# Anthropic七位创始人IPO套现近20亿美元
凤凰网科技 8小时前 35评论
11月20日，据TechCrunch报道，Anthropic七位联合创始人在公司首次公开募股中合计出售约2364.86万股股票。按照每股83美元的IPO发行价计算，相关股份价值约19.63亿美元，平均每人套现约2.8亿美元。美国证券交易委员会披露的最终招股书显示，七位创始人此次出售的股份数量相同，均为337.84万股，对应售股金额约2.804亿美元。`

	phoenixCopy3 := `问AI · 创始人小幅套现为何仍掌控公司过半投票权? 凤凰网科技讯 11月20日,据TechCrunch报道,Anthropic七位联合创始人在公司首次公开募股中合计出售约2364.86万股股票。按照每股83美元的IPO发行价计算,相关股份价值约19.63亿美元,平均每人套现约2.8亿美元。美国证券交易委员会披露的最终招股书显示,七位创始人此次出售的股份数量相同,均为337.84万股,对应售股金额约2.804亿美元。`

	sinaIndependent := `Anthropic于美东时间周四登陆美股，本次IPO合计募资813亿美元，其中19.5亿美元由七位创始人出售股份所得。正如此前报道，每位创始人拿出约340万股在本次IPO中发售。不过Anthropic在IPO申报文件中表示，创始人投身这项事业并不完全是为了金钱，并披露到2030年底前基础设施承诺支出需要4516亿美元。`

	ds := &fakeSearcher{
		name: spec.ProviderDeepSeek,
		caps: core.Capabilities{MaxCount: 20},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				// 同分支返回 2 篇同一通稿（http 同花顺 + https wap 东方财富）
				{
					Rank: 1, Title: "Anthropic七位创始人IPO套现近20亿美元",
					URL: "http://stock.10jqka.com.cn/usstock/20261120/c682040206.shtml", Site: "同花顺",
					Published: &t1, Summary: phoenixCopy1, Authority: &auth75,
				},
				{
					Rank: 2, Title: "Anthropic七位创始人IPO套现近20亿美元",
					URL: "https://wap.eastmoney.com/a/202611203936005697.html", Site: "东方财富网",
					Published: &t1, Summary: phoenixCopy2,
				},
				{
					Rank: 3, Title: "英伟达三季度财报深度解读",
					URL: "https://example.cn/nvda-q3", Site: "财经日报",
					Summary: "英伟达第三财季数据中心营收突破千亿美元大关，新架构平台已进入大规模量产交付阶段，市场关注未来资本开支节奏。",
				},
			},
		},
	}

	wsa := &fakeSearcher{
		name: spec.ProviderTencentWSA,
		caps: core.Capabilities{MaxCount: 20},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				// 独立报道（标题高度相似但正文完全独立）不应被误合并
				{
					Rank: 1, Title: "史上最大IPO刷新 Anthropic七位创始人套现近20亿美元",
					URL: "https://finance.sina.com.cn/stock/usstock/c/2026-11-20/doc-1.shtml", Site: "新浪财经",
					Summary: sinaIndependent,
				},
				// 跨源命中同一凤凰网通稿（https 桌面端 + 更高 authority 0.80 -> 应胜出作为代表 URL，且合并为 deepseek+tencent_wsa 共识）
				{
					Rank: 2, Title: "Anthropic七位创始人IPO套现近20亿美元",
					URL: "https://news.qq.com/rain/a/20261120A08E8Q00", Site: "腾讯新闻",
					Published: &t2, Summary: phoenixCopy3, Authority: &auth80,
				},
			},
		},
	}

	r := New(Options{
		SearchProviders: []string{spec.ProviderDeepSeek, spec.ProviderTencentWSA},
		FusionProviders: []string{spec.ProviderDeepSeek, spec.ProviderTencentWSA},
		FusionK:         60,
	}, []core.Provider{ds, wsa})

	env, err := r.Search(context.Background(), &core.SearchRequest{
		Query:    "Anthropic 上市 IPO 创始人 套现",
		Count:    5,
		Strategy: core.StrategyFusion,
	})
	if err != nil || !env.OK {
		t.Fatalf("fusion search failed: env=%+v err=%v", env, err)
	}
	if len(env.Results) != 3 {
		t.Fatalf("expected 3 unique results after collapsing 3 Phoenix copies into 1, got %d: %+v", len(env.Results), env.Results)
	}

	top := env.Results[0]
	if top.URL != "https://news.qq.com/rain/a/20261120A08E8Q00" {
		t.Fatalf("expected HTTPS desktop high-authority representative URL, got %s", top.URL)
	}
	if !slices.Equal(top.Providers, []string{spec.ProviderDeepSeek, spec.ProviderTencentWSA}) {
		t.Fatalf("expected deduplicated cross-engine consensus providers [deepseek tencent_wsa], got %v", top.Providers)
	}
	if top.Published == nil || !top.Published.Equal(t1) {
		t.Fatalf("expected earliest published timestamp %v preserved, got %v", t1, top.Published)
	}
	if !strings.Contains(strings.Join(env.Meta.Notes, "; "), "deduped 2 near-duplicate results by content fingerprint") {
		t.Fatalf("expected transparent dedup note, got %v", env.Meta.Notes)
	}

	// 验证簇内代表页择优与字段合并后，URL 绑定元数据（Site/Icon）绝不跨站错配，但同 URL 可正常回填
	resolved := resolveClusterResult([]clusterMember{
		{result: core.Result{URL: "http://mirror.example/1", Title: "Story", Site: "OldMirror", Icon: "http://mirror.example/fav.ico"}, urlKey: urlnorm.Key("http://mirror.example/1"), branchID: 0, rank: 1},
		{result: core.Result{URL: "https://origin.example/1", Title: "Story", Site: "", Icon: ""}, urlKey: urlnorm.Key("https://origin.example/1"), branchID: 1, rank: 1},
		{result: core.Result{URL: "https://zaker.example/2", Title: "Story", Site: "ZAKER", Icon: "https://zaker.example/fav.ico"}, urlKey: urlnorm.Key("https://zaker.example/2"), branchID: 2, rank: 2},
	}, []string{"a", "b", "c"}, false)
	if resolved.URL != "https://origin.example/1" || resolved.Site != "" || resolved.Icon != "" {
		t.Fatalf("cross-host cluster members must not inject foreign Site/Icon onto winning representative, got %+v", resolved)
	}
	sameHost := core.Result{URL: "https://origin.example/1", Title: "Story", Site: "OriginSite", Icon: "https://origin.example/fav.ico"}
	mergeResultMetadata(&resolved, sameHost, true)
	if resolved.Site != "OriginSite" || resolved.Icon != "https://origin.example/fav.ico" {
		t.Fatalf("same-URL merge should still backfill missing Site/Icon, got %+v", resolved)
	}

	// 验证同分支防双计分：单个分支内 3 条同稿转载（Rank 1,2,3）的 RRF 分数必须严格等于单次 Rank 1，不得超越跨源双命中条目
	spamBranch := &fakeSearcher{
		name: "spammer",
		caps: core.Capabilities{MaxCount: 20},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 1, Title: "Anthropic七位创始人IPO套现近20亿美元", URL: "https://a.example/1", Summary: phoenixCopy1},
				{Rank: 2, Title: "Anthropic七位创始人IPO套现近20亿美元", URL: "https://b.example/2", Summary: phoenixCopy2},
				{Rank: 3, Title: "Anthropic七位创始人IPO套现近20亿美元", URL: "https://c.example/3", Summary: phoenixCopy3},
				{Rank: 4, Title: "Official SEC Filing", URL: "https://sec.gov/424b4", Summary: "Official SEC filing prospectus details for Class A and Class F voting shares."},
			},
		},
	}
	cleanBranch := &fakeSearcher{
		name: "clean",
		caps: core.Capabilities{MaxCount: 20},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 2, Title: "Official SEC Filing", URL: "https://sec.gov/424b4", Summary: "Official SEC filing prospectus details for Class A and Class F voting shares."},
			},
		},
	}
	rGuard := New(Options{
		FusionProviders: []string{"spammer", "clean"},
		FusionK:         60,
	}, []core.Provider{spamBranch, cleanBranch})
	guardEnv, err := rGuard.Search(context.Background(), &core.SearchRequest{
		Query:    "Anthropic IPO",
		Count:    5,
		Strategy: core.StrategyFusion,
	})
	if err != nil || len(guardEnv.Results) != 2 {
		t.Fatalf("anti-double-count fusion failed: env=%+v err=%v", guardEnv, err)
	}
	// sec.gov 跨源双命中（1/64 + 1/62 > 1/61）必须排第一；若同分支未防双计分则 spammer 三副本会以 1/61+1/62+1/63 反超！
	if guardEnv.Results[0].URL != "https://sec.gov/424b4" {
		t.Fatalf("same-branch syndications must not double-count RRF score; expected sec.gov #1, got %+v", guardEnv.Results)
	}
	if !slices.Equal(guardEnv.Results[1].Providers, []string{"spammer"}) {
		t.Fatalf("same-branch reprints must not duplicate provider name in Providers[], got %v", guardEnv.Results[1].Providers)
	}
}

func TestAutoDedupPrefersCleanTitleAndSingleStaysRaw(t *testing.T) {
	body := `北京时间11月18日清晨，英伟达公布了黄仁勋离职后的首份财报。第三财季，英伟达实现营收约1158亿美元，同比增长103%，环比增长20%。按GAAP口径，净利润为652.87亿美元，数据中心业务营收1076亿美元，新一代平台已开始量产出货。`
	auth := 1.0
	ds := &fakeSearcher{
		name: spec.ProviderDeepSeek,
		caps: core.Capabilities{MaxCount: 20},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{
					Rank: 1, Title: "英伟达，最新业绩、黄仁勋动向→ - 英伟达，最新业绩、黄仁勋动向→",
					URL: "https://www.163.com/dy/article/LC0RDKIK053469RG.html", Site: "网易手机",
					Summary: body, Authority: &auth,
				},
				{
					Rank: 2, Title: "英伟达，最新业绩、黄仁勋动向",
					URL: "https://stcn.com/article/detail/4341466.html", Site: "证券时报",
					Summary: body, Authority: &auth,
				},
				{
					Rank: 3, Title: "英伟达，最新业绩、黄仁勋动向→ - 今日头条",
					URL: "https://www.toutiao.com/article/7709327222608069129/", Site: "今日头条",
					Summary: body, Authority: &auth,
				},
			},
		},
	}

	r := New(Options{SearchProviders: []string{spec.ProviderDeepSeek}}, []core.Provider{ds})

	// 1. auto 默认链启用指纹去重，并优选无重复拼接脏标题的证券时报原发链接
	autoEnv, err := r.Search(context.Background(), &core.SearchRequest{Query: "英伟达财报", Count: 5})
	if err != nil || !autoEnv.OK {
		t.Fatalf("auto search failed: %v", err)
	}
	if len(autoEnv.Results) != 1 || autoEnv.Results[0].URL != "https://stcn.com/article/detail/4341466.html" {
		t.Fatalf("expected auto dedup to keep clean stcn.com original, got %+v", autoEnv.Results)
	}
	if !strings.Contains(strings.Join(autoEnv.Meta.Notes, "; "), "deduped 2 near-duplicate results") {
		t.Fatalf("expected auto dedup note, got %v", autoEnv.Meta.Notes)
	}

	// 2. 显式单源锁定 single（-p deepseek）保留上游原始条数，便于单源排错审计
	singleEnv, err := r.Search(context.Background(), &core.SearchRequest{
		Query:     "英伟达财报",
		Count:     5,
		Providers: []string{spec.ProviderDeepSeek},
	})
	if err != nil || !singleEnv.OK || len(singleEnv.Results) != 3 {
		t.Fatalf("expected single locked search to stay raw with 3 results, got len=%d err=%v", len(singleEnv.Results), err)
	}

	// 3. 融合模式下同 URL 命中：干净标题可修复前序重复拼接脏标题，且高排名的脏标题不得覆盖已有的干净标题
	dirtyHi := &fakeSearcher{
		name: "dirty",
		caps: core.Capabilities{MaxCount: 20},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 1, Title: "英伟达最新业绩发布 - 英伟达最新业绩发布", URL: "https://example.org/nvda", Summary: "Summary A"},
			},
		},
	}
	cleanLo := &fakeSearcher{
		name: "clean",
		caps: core.Capabilities{MaxCount: 20},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 1, Title: "其他结果", URL: "https://example.org/other", Summary: "Other"},
				{Rank: 2, Title: "英伟达最新业绩发布", URL: "https://example.org/nvda", Summary: "Summary B"},
			},
		},
	}
	rRepair := New(Options{FusionProviders: []string{"dirty", "clean"}, FusionK: 60}, []core.Provider{dirtyHi, cleanLo})
	repEnv, err := rRepair.Search(context.Background(), &core.SearchRequest{Query: "nvda", Count: 5, Strategy: core.StrategyFusion})
	if err != nil || len(repEnv.Results) != 2 || repEnv.Results[0].Title != "英伟达最新业绩发布" {
		t.Fatalf("expected clean title to repair duplicated title on same URL, got %+v err=%v", repEnv.Results, err)
	}
	rKeep := New(Options{FusionProviders: []string{"clean", "dirty"}, FusionK: 60}, []core.Provider{cleanLo, dirtyHi})
	keepEnv, err := rKeep.Search(context.Background(), &core.SearchRequest{Query: "nvda", Count: 5, Strategy: core.StrategyFusion})
	if err != nil || len(keepEnv.Results) != 2 || keepEnv.Results[0].Title != "英伟达最新业绩发布" {
		t.Fatalf("expected better-ranked dirty title not to overwrite clean title on same URL, got %+v err=%v", keepEnv.Results, err)
	}
}

func TestCanonicalSelectionHygieneConsensusAndOrderIndependence(t *testing.T) {
	body := `Anthropic于美东时间周四登陆美股，本次IPO合计募资813亿美元，其中19.5亿美元由七位创始人出售股份所得。本次IPO发行价上调至每股83美元，这意味着在扣除承销费与税费之前，每位创始人套现约2.8亿美元。公司披露可预见周期内基础设施相关承诺规模约在1.05万亿美元。`
	auth75, auth80 := 0.75, 0.80
	cleanURL := "https://finance.sina.com.cn/stock/usstock/c/2026-11-20/doc-inizkuqy7097417.shtml"
	mdMirrorURL := cleanURL + ".md"
	dirtyFMTitle := "---title: 史上最大IPO刷新 Anthropic七位创始人套现近20亿美元source: 环球市场播报datetime: 2026-11-20T18:26:1408:00canonical_url: " + cleanURL + "--- " + body

	// 1. 复现实测 Rank 2：两家引擎返回干净 .shtml，tencent_wsa 返回 .shtml.md（含脏 frontmatter 标题且 authority=0.80 > 0.75）
	//    无论分支顺序如何，干净正式 .shtml 页面必须稳定胜出！
	mkTriple := func(order []string) *core.Envelope {
		ds := &fakeSearcher{
			name: spec.ProviderDeepSeek,
			caps: core.Capabilities{MaxCount: 10},
			outcome: &core.SearchOutcome{Results: []core.Result{
				{Rank: 1, Title: "史上最大IPO刷新 Anthropic七位创始人套现近20亿美元", URL: cleanURL, Site: "新浪财经", Summary: body, Authority: &auth75},
			}},
		}
		kn := &fakeSearcher{
			name: spec.ProviderKeenable,
			caps: core.Capabilities{MaxCount: 10},
			outcome: &core.SearchOutcome{Results: []core.Result{
				{Rank: 1, Title: "史上最大IPO刷新 Anthropic七位创始人套现近20亿美元", URL: cleanURL, Summary: body},
			}},
		}
		wsa := &fakeSearcher{
			name: spec.ProviderTencentWSA,
			caps: core.Capabilities{MaxCount: 10},
			outcome: &core.SearchOutcome{Results: []core.Result{
				{Rank: 1, Title: dirtyFMTitle, URL: mdMirrorURL, Site: "新浪财经", Summary: body, Authority: &auth80},
			}},
		}
		r := New(Options{FusionProviders: order, FusionK: 60}, []core.Provider{ds, kn, wsa})
		env, err := r.Search(context.Background(), &core.SearchRequest{Query: "Anthropic IPO", Count: 5, Strategy: core.StrategyFusion})
		if err != nil || !env.OK {
			t.Fatalf("fusion order %v failed: %v", order, err)
		}
		return env
	}

	for _, order := range [][]string{
		{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderTencentWSA},
		{spec.ProviderTencentWSA, spec.ProviderKeenable, spec.ProviderDeepSeek},
	} {
		env := mkTriple(order)
		if len(env.Results) != 1 || env.Results[0].URL != cleanURL || env.Results[0].Title != "史上最大IPO刷新 Anthropic七位创始人套现近20亿美元" {
			t.Fatalf("order %v: expected clean .shtml representative, got %+v", order, env.Results)
		}
	}

	// 2. 即使 1v1 且 .shtml.md 标题已干净、authority 更高，派生镜像后缀也不得覆盖正式 HTML 原页
	res1v1 := resolveClusterResult([]clusterMember{
		{result: core.Result{Title: "史上最大IPO刷新", URL: mdMirrorURL, Authority: &auth80}, urlKey: urlnorm.Key(mdMirrorURL), branchID: 0, rank: 1},
		{result: core.Result{Title: "史上最大IPO刷新", URL: cleanURL, Authority: &auth75}, urlKey: urlnorm.Key(cleanURL), branchID: 1, rank: 2},
	}, []string{"wsa", "ds"}, false)
	if res1v1.URL != cleanURL {
		t.Fatalf("expected HTML canonical URL over .shtml.md mirror, got %s", res1v1.URL)
	}

	// 3. 跨引擎 URL 共识（2 票 > 1 票）与顺序无关，且不跨异构引擎盲比未校准 Authority
	urlConsensus := "https://finance.eastmoney.com/a/20261120.html"
	urlSingle := "https://reprint.example.com/a/20261120.html"
	for _, members := range [][]clusterMember{
		{
			{result: core.Result{Title: "Headline", URL: urlSingle, Authority: &auth80}, urlKey: urlnorm.Key(urlSingle), branchID: 0, rank: 1},
			{result: core.Result{Title: "Headline", URL: urlConsensus, Authority: &auth75}, urlKey: urlnorm.Key(urlConsensus), branchID: 1, rank: 2},
			{result: core.Result{Title: "Headline", URL: urlConsensus}, urlKey: urlnorm.Key(urlConsensus), branchID: 2, rank: 3},
		},
		{
			{result: core.Result{Title: "Headline", URL: urlConsensus}, urlKey: urlnorm.Key(urlConsensus), branchID: 2, rank: 3},
			{result: core.Result{Title: "Headline", URL: urlConsensus, Authority: &auth75}, urlKey: urlnorm.Key(urlConsensus), branchID: 1, rank: 2},
			{result: core.Result{Title: "Headline", URL: urlSingle, Authority: &auth80}, urlKey: urlnorm.Key(urlSingle), branchID: 0, rank: 1},
		},
	} {
		got := resolveClusterResult(members, []string{"a", "b", "c"}, false)
		if got.URL != urlConsensus {
			t.Fatalf("expected 2-vote canonical URL %s independent of order, got %s", urlConsensus, got.URL)
		}
	}

	// 4. 同簇存在无 .md 孪生网页时优先网页版；独立原生 Markdown（如 GitHub README.md）不受任何惩罚
	twinRes := resolveClusterResult([]clusterMember{
		{result: core.Result{Title: "API Guide", URL: "https://docs.example.com/guide.md"}, urlKey: urlnorm.Key("https://docs.example.com/guide.md"), branchID: 0, rank: 1},
		{result: core.Result{Title: "API Guide", URL: "https://docs.example.com/guide"}, urlKey: urlnorm.Key("https://docs.example.com/guide"), branchID: 1, rank: 2},
	}, []string{"a", "b"}, false)
	if twinRes.URL != "https://docs.example.com/guide" {
		t.Fatalf("expected HTML twin /guide over /guide.md, got %s", twinRes.URL)
	}
	ghURL := "https://github.com/org/repo/blob/main/README.md"
	ghMember := clusterMember{result: core.Result{Title: "README.md", URL: ghURL}, urlKey: urlnorm.Key(ghURL)}
	if canonicalHygieneScore(ghMember, map[string]struct{}{ghMember.urlKey: {}}) != 12 {
		t.Fatalf("standalone GitHub README.md must not be penalized as derived mirror")
	}

	// 5. 同引擎 auto 去重仍可参考同尺度的校准 Authority，且无 frontmatter 的超长正文脏标题被卫生门禁淘汰
	auth50, auth90 := 0.50, 0.90
	singleAuth := resolveClusterResult([]clusterMember{
		{result: core.Result{Title: "Headline", URL: "https://low.example/1", Authority: &auth50}, urlKey: urlnorm.Key("https://low.example/1"), branchID: 0, rank: 1},
		{result: core.Result{Title: "Headline", URL: "https://high.example/1", Authority: &auth90}, urlKey: urlnorm.Key("https://high.example/1"), branchID: 0, rank: 2},
	}, nil, true)
	if singleAuth.URL != "https://high.example/1" {
		t.Fatalf("same-engine auto dedup should honor calibrated authority, got %s", singleAuth.URL)
	}
	overlongTitle := strings.Repeat("正文误入标题段落内容", 30)
	dumpGuard := resolveClusterResult([]clusterMember{
		{result: core.Result{Title: overlongTitle, URL: "https://dump.example/1"}, urlKey: urlnorm.Key("https://dump.example/1"), branchID: 0, rank: 1},
		{result: core.Result{Title: "Clean Headline", URL: "https://clean.example/1"}, urlKey: urlnorm.Key("https://clean.example/1"), branchID: 1, rank: 2},
	}, []string{"a", "b"}, false)
	if dumpGuard.URL != "https://clean.example/1" {
		t.Fatalf("overlong body-dumped title should lose to clean headline, got %+v", dumpGuard)
	}

	// 6. 转载页摘要夹带 180 字 YAML frontmatter 经单源清洗后，不再凭空“注水”覆盖原发站更长的干净摘要，且不泄露异站 canonical_url
	origBody := "人工智能公司Anthropic完成史上规模最大IPO，以高于发行区间的价格定价，募资总额远超此前所有纪录，标志着资本市场对AI领域的押注正式从芯片基础设施延伸至前沿模型平台。承销商还获得绿鞋超额配售权，总募资规模有望进一步提升至1195亿美元，完全稀释估值约1.5万亿美元。"
	reprintShorterBody := "人工智能公司Anthropic完成史上规模最大IPO，以高于发行区间的价格定价，募资总额远超此前所有纪录，标志着资本市场对AI领域的押注正式从芯片基础设施延伸至前沿模型平台。"
	paddedSummary := "--- title: 史上最大IPO周四上市 source: 市场资讯 datetime: 2026-11-19T09:42:05+08:00 importance: 0 canonical_url: https://finance.sina.com.cn/roll/2026-11-19/doc-inizfqei4335722.shtml --- " + reprintShorterBody
	wscnClean, _ := core.SanitizeResults(spec.ProviderKeenable, []core.Result{
		{Title: "史上最大IPO周四上市 - 华尔街见闻", URL: "https://wallstreetcn.com/articles/3786222", Summary: origBody},
	}, core.DetailSummary, false, urlnorm.Key)
	sinaPadded, _ := core.SanitizeResults(spec.ProviderTencentWSA, []core.Result{
		{Title: "史上最大IPO周四上市", URL: "https://finance.sina.com.cn/roll/2026-11-19/doc-inizfqei4335722.shtml.md", Summary: paddedSummary},
	}, core.DetailSummary, false, urlnorm.Key)
	rNoPad := New(Options{FusionProviders: []string{spec.ProviderKeenable, spec.ProviderTencentWSA}, FusionK: 60}, []core.Provider{
		&fakeSearcher{name: spec.ProviderKeenable, caps: core.Capabilities{MaxCount: 10}, outcome: &core.SearchOutcome{Results: wscnClean}},
		&fakeSearcher{name: spec.ProviderTencentWSA, caps: core.Capabilities{MaxCount: 10}, outcome: &core.SearchOutcome{Results: sinaPadded}},
	})
	noPadEnv, err := rNoPad.Search(context.Background(), &core.SearchRequest{Query: "Anthropic IPO", Count: 5, Strategy: core.StrategyFusion})
	if err != nil || len(noPadEnv.Results) != 1 {
		t.Fatalf("expected 1 merged result, got env=%+v err=%v", noPadEnv, err)
	}
	if noPadEnv.Results[0].URL != "https://wallstreetcn.com/articles/3786222" || strings.Contains(noPadEnv.Results[0].Summary, "canonical_url") || noPadEnv.Results[0].Summary != origBody {
		t.Fatalf("frontmatter-padded reprint must not leak canonical_url or overwrite longer clean original summary, got %+v", noPadEnv.Results[0])
	}

	// 7. 单源 auto 模式下简繁同稿镜像（星岛头条简体 + 繁体）自动去重，且对外展示标题/摘要原样保留不改写字形
	stSimpTitle := "特朗普吁辉达让黄仁勋回任 称习近平赞他是人才 黄同意赴美国会作证"
	stSimpBody := "美国总统特朗普再度表态，力挺黄仁勋重返辉达，表明希望黄仁勋郑重道歉后回去工作，并呼吁辉达应给这位全球顶尖人才第2次机会。特朗普还透露，曾跟中国国家主席习近平谈及黄仁勋事件，对方也称赞黄是杰出人才。与此同时，黄仁勋已同意出席参议院公开听证会作证。"
	stTradTitle := "特朗普籲輝達讓黃仁勳回任 稱習近平讚他是人才 黃同意赴美國會作證"
	stTradBody := "美國總統特朗普再度表態，力挺黃仁勳重返輝達，表明希望黃仁勳鄭重道歉後回去工作，並呼籲輝達應給這位全球頂尖人才第2次機會。特朗普還透露，曾跟中國國家主席習近平談及黃仁勳事件，對方也稱讚黃是傑出人才。與此同時，黃仁勳已同意出席參議院公開聽證會作證。"
	stDS := &fakeSearcher{
		name: spec.ProviderDeepSeek,
		caps: core.Capabilities{MaxCount: 10},
		outcome: &core.SearchOutcome{Results: []core.Result{
			{Rank: 1, Title: stSimpTitle, URL: "https://www.stheadline.com/zh-hans/loadnext/0/3639517", Site: "星島頭條", Summary: stSimpBody},
			{Rank: 2, Title: stTradTitle, URL: "https://www.stheadline.com/realtime-world/3639517/article", Site: "星島頭條", Summary: stTradBody},
		}},
	}
	rST := New(Options{SearchProviders: []string{spec.ProviderDeepSeek}}, []core.Provider{stDS})
	stEnv, err := rST.Search(context.Background(), &core.SearchRequest{Query: "英伟达 黄仁勋 特朗普", Count: 5})
	if err != nil || len(stEnv.Results) != 1 || stEnv.Results[0].Title != stSimpTitle {
		t.Fatalf("expected auto search to collapse Simplified/Traditional mirror while keeping original text, got env=%+v err=%v", stEnv, err)
	}
}
