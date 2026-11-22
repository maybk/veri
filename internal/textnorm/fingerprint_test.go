package textnorm

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFingerprintSyndicatedArticlesAndGuardrails(t *testing.T) {
	// 1. 实测真实同稿跨站转载：同花顺 / 东方财富 / 腾讯新闻（标点全半角差异 + 问AI导语前缀差异）
	title1 := NormalizeTitleForDedup("Anthropic七位创始人IPO套现近20亿美元", "同花顺")
	body1 := BuildTextFingerprint(`# Anthropic七位创始人IPO套现近20亿美元
2026-11-20 13:36:24 来源：凤凰网
凤凰网科技讯 11月20日，据TechCrunch报道，Anthropic（ANT）七位联合创始人在公司首次公开募股中合计出售约2364.86万股股票。按照每股83美元的IPO发行价计算，相关股份价值约19.63亿美元，平均每人套现约2.8亿美元。美国证券交易委员会披露的最终招股书显示，七位创始人此次出售的股份数量相同，均为337.84万股，对应售股金额约2.804亿美元。`)

	title2 := NormalizeTitleForDedup("Anthropic七位创始人IPO套现近20亿美元", "腾讯新闻")
	body2 := BuildTextFingerprint(`问AI · 创始人小幅套现为何仍掌控公司过半投票权? 凤凰网科技讯 11月20日,据TechCrunch报道,Anthropic七位联合创始人在公司首次公开募股中合计出售约2364.86万股股票。按照每股83美元的IPO发行价计算,相关股份价值约19.63亿美元,平均每人套现约2.8亿美元。美国证券交易委员会披露的最终招股书显示,七位创始人此次出售的股份数量相同,均为337.84万股,对应售股金额约2.804亿美元。`)

	if !FingerprintsNearDuplicate(title1, body1, title2, body2) {
		t.Fatal("expected syndicated Chinese news articles with punctuation/prefix differences to match as near-duplicates")
	}

	// 2. 同稿改标题（门户重起标题或重复拼接标题）仍通过正文强命中识别
	dupTitle := "英伟达，最新业绩、黄仁勋动向→ - 英伟达，最新业绩、黄仁勋动向→"
	if !IsDuplicatedTitle(dupTitle) {
		t.Fatalf("expected IsDuplicatedTitle(%q) = true", dupTitle)
	}
	if IsDuplicatedTitle("英伟达，最新业绩、黄仁勋动向") {
		t.Fatal("clean title should not be flagged as duplicated")
	}
	normDup := NormalizeTitleForDedup(dupTitle, "网易手机")
	normOrig := NormalizeTitleForDedup("英伟达，最新业绩、黄仁勋动向", "证券时报")
	normToutiao := NormalizeTitleForDedup("英伟达，最新业绩、黄仁勋动向→ - 今日头条", "今日头条")
	if !slices.Equal(normDup, normOrig) || !slices.Equal(normToutiao, normOrig) {
		t.Fatalf("expected normalized titles to match: dup=%v orig=%v toutiao=%v", normDup, normOrig, normToutiao)
	}

	rewrittenTitle := NormalizeTitleForDedup("突发！AI巨头高管齐抛股套现真相曝光", "财经快讯")
	if !FingerprintsNearDuplicate(title1, body1, rewrittenTitle, body2) {
		t.Fatal("expected verbatim body reprint with rewritten headline to match via body-only gate")
	}

	// 3. 同一事件独立报道（标题高度相似但正文独立撰写）严禁误杀！
	titleSina := NormalizeTitleForDedup("史上最大IPO刷新 Anthropic七位创始人套现近20亿美元", "新浪财经")
	bodySina := BuildTextFingerprint(`Anthropic于美东时间周四登陆美股，本次IPO合计募资813亿美元，其中19.5亿美元由七位创始人出售股份所得。正如此前报道，每位创始人拿出约340万股在本次IPO中发售。不过Anthropic在IPO申报文件中表示，创始人投身这项事业并不完全是为了金钱。截至2026年9月30日的季度数据，Anthropic仅未来12个月算力与基础设施投入承诺就需要707亿美元；到2030年底前，这部分承诺支出需要4516亿美元。`)
	if FingerprintsNearDuplicate(title1, body1, titleSina, bodySina) {
		t.Fatal("independent report on same event must NOT be collapsed as near-duplicate")
	}

	// 4. 英文新闻带 Markdown 内链 vs 纯文本的同稿转载匹配
	enTitleA := NormalizeTitleForDedup("Trump asks Nvidia to give Jensen Huang a second chance - POLITICO", "POLITICO")
	enBodyA := BuildTextFingerprint("President Donald Trump on Friday implored [Nvidia](https://politico.com/nvidia)'s board to reinstate [Jensen Huang](https://politico.com/huang), the former chief executive who resigned after multiple affairs came to light, arguing that his leadership is crucial for the United States to remain ahead of competitors in the artificial intelligence race during the coming decade.")
	enTitleB := NormalizeTitleForDedup("Trump asks Nvidia to give Jensen Huang a second chance", "Yahoo News")
	enBodyB := BuildTextFingerprint("President Donald Trump on Friday implored Nvidia's board to reinstate Jensen Huang, the former chief executive who resigned after multiple affairs came to light, arguing that his leadership is crucial for the United States to remain ahead of competitors in the artificial intelligence race during the coming decade.")
	if !FingerprintsNearDuplicate(enTitleA, enBodyA, enTitleB, enBodyB) {
		t.Fatal("expected English syndicated article with inline markdown links to match plain-text copy")
	}

	// 5. 通用短标题（Quickstart / Release Notes）不同文档严禁误杀；极短/空正文严禁跨 URL 合并
	shortA := NormalizeTitleForDedup("Quickstart", "Go Docs")
	shortB := NormalizeTitleForDedup("Quickstart", "Rust Docs")
	docA := BuildTextFingerprint("Install the Go toolchain and initialize your first module with go mod init example.com/hello to build fast binaries.")
	docB := BuildTextFingerprint("Install rustup from the official installer and run cargo new hello_world to compile your first Rust crate cleanly.")
	if FingerprintsNearDuplicate(shortA, docA, shortB, docB) {
		t.Fatal("distinct Quickstart docs must never be collapsed")
	}
	stubA := BuildTextFingerprint("Short stub text.")
	stubB := BuildTextFingerprint("Short stub text.")
	if stubA.Matchable() || FingerprintsNearDuplicate(title1, stubA, title2, stubB) {
		t.Fatal("too-short stub bodies must never merge across different URLs")
	}

	// 6. 对抗性大量未闭合方括号与超长多字节 UTF-8 文本必须线性结束且不切碎 UTF-8 码点
	adversarial := strings.Repeat("[", 40000) + "](" + strings.Repeat("x", 1024)
	if fp := BuildTextFingerprint(adversarial); fp.Matchable() {
		t.Fatal("adversarial bracket noise should not become matchable")
	}
	clipped := trimUTF8Bytes(strings.Repeat("汉", 15000), maxFingerprintInputBytes)
	if len(clipped) > maxFingerprintInputBytes || !utf8.ValidString(clipped) {
		t.Fatalf("trimUTF8Bytes produced invalid UTF-8 or exceeded bound: len=%d valid=%v", len(clipped), utf8.ValidString(clipped))
	}
}

func TestFrontmatterTitleHygieneAndBodyStripping(t *testing.T) {
	// 1. 实测真实腾讯云 WSA 收录新浪 .shtml.md 导致的单行 YAML frontmatter + 全文灌入标题
	wsaDirty := "---title: 史上最大IPO刷新 Anthropic七位创始人套现近20亿美元source: 环球市场播报datetime: 2026-11-20T18:26:1408:00importance: 0canonical_url: https://finance.sina.com.cn/stock/usstock/c/2026-11-20/doc-inizkuqy7097417.shtml--- Anthropic于美东时间周四登陆美股,本次IPO合计募资813亿美元。"
	if got := CleanResultTitle(wsaDirty); got != "史上最大IPO刷新 Anthropic七位创始人套现近20亿美元" {
		t.Fatalf("expected clean extracted title from inline frontmatter, got %q", got)
	}
	if IsCleanResultTitle(wsaDirty) {
		t.Fatal("raw frontmatter dump must be rejected by IsCleanResultTitle")
	}

	// 2. 标准多行 YAML frontmatter（保留标题中的冒号与引号清洗）
	multiline := "---\ntitle: \"Understanding Open Source: A Guide\"\nsource: TechBlog\ndate: 2026-11-20\n---\nBody text here."
	if got := CleanResultTitle(multiline); got != "Understanding Open Source: A Guide" {
		t.Fatalf("expected multiline frontmatter title extraction, got %q", got)
	}

	// 3. 正常标题与连字符词（open-source / --- 装饰线）严禁误伤
	for _, untouched := range []string{
		"--- Breaking News ---",
		"Open-source: Complete Reference Guide",
		"Anthropic announces closing of initial public offering, including full exercise of underwriters’ option to purchase additional shares, and closing of concurrent private placement",
	} {
		if got := CleanResultTitle(untouched); got != untouched {
			t.Fatalf("normal title %q was unexpectedly modified to %q", untouched, got)
		}
		if !IsCleanResultTitle(untouched) {
			t.Fatalf("legitimate headline %q should pass IsCleanResultTitle", untouched)
		}
	}

	// 4. 超长正文灌入（无 frontmatter 但 > 220 字符）应判定为非干净标题
	longDump := strings.Repeat("这是一段被误写入标题栏的正文内容，", 20)
	if IsCleanResultTitle(longDump) {
		t.Fatal("overlong body-dumped title should fail IsCleanResultTitle")
	}

	// 5. 正文开头 YAML frontmatter 剥离：保留正文高重合，不误剥普通 Markdown 分隔线
	bodyWithFM := "---\ntitle: 史上最大IPO刷新\nsource: 环球市场播报\ndatetime: 2026-11-20\n---\nAnthropic于美东时间周四登陆美股，本次IPO合计募资813亿美元，其中19.5亿美元由七位创始人出售股份所得。"
	bodyClean := "Anthropic于美东时间周四登陆美股，本次IPO合计募资813亿美元，其中19.5亿美元由七位创始人出售股份所得。"
	if !FingerprintsNearDuplicate(nil, BuildTextFingerprint(bodyWithFM), nil, BuildTextFingerprint(bodyClean)) {
		t.Fatal("body with leading YAML frontmatter should match clean body after stripping")
	}
	plainHR := "---\nPlain prose quote without metadata\n---\nMore body."
	if StripLeadingFrontmatter(plainHR) != plainHR {
		t.Fatalf("plain markdown horizontal rule should not be stripped, got %q", StripLeadingFrontmatter(plainHR))
	}
	unclosedMultiline := "---\ntitle: Hello --- World\nbody without closing delimiter"
	if _, ok := ExtractFrontmatterTitle(unclosedMultiline); ok {
		t.Fatal("multiline text without standalone closing --- line must not match inline ---")
	}

	// 6. Summary/Snippet 窄口剥离真实 .shtml.md 单行 frontmatter（含前导 BOM），严防单行散文误剥与 HTML 二次反转义
	wscnSummary := "\ufeff--- title: 史上最大IPO周四上市! source: 市场资讯 datetime: 2026-11-19T09:42:05+08:00 importance: 0 canonical_url: https://finance.sina.com.cn/doc.shtml --- 来源:华尔街见闻 正文开始。"
	if got := CleanResultSummary(wscnSummary); got != "来源:华尔街见闻 正文开始。" {
		t.Fatalf("expected inline YAML frontmatter stripped from summary, got %q", got)
	}
	if got := CleanResultSnippet(wscnSummary); got != "来源:华尔街见闻 正文开始。" {
		t.Fatalf("expected inline YAML frontmatter stripped from snippet, got %q", got)
	}
	for _, prose := range []string{
		"--- Reminder: check the source: docs --- Next paragraph",
		"--- source: Reuters --- Stocks rose 5% today.",
		"--- title: Only frontmatter without trailing body ---",
	} {
		if got := CleanResultSummary(prose); got != prose {
			t.Fatalf("non-frontmatter or bodyless text %q should stay intact, got %q", prose, got)
		}
	}
	if !IsCleanResultTitle("--- Go 1.26: What's New ---") {
		t.Fatal("decorative title with colon must not be flagged as YAML frontmatter")
	}
	if got := CleanResultTitle("&amp;lt;b&amp;gt;Title&amp;lt;/b&amp;gt;"); got != "&lt;b&gt;Title&lt;/b&gt;" {
		t.Fatalf("CleanResultTitle must unescape HTML entities at most once, got %q", got)
	}
}

func TestCJKTradToSimpFingerprintFolding(t *testing.T) {
	// 1. 校验编译期繁简折叠表严格升序、范围合法、无自映射且全局满足单步不动点（fold(fold(r)) == fold(r)）
	var prev uint16
	for i, entry := range cjkTradToSimpTable {
		trad := uint16(entry >> 16)
		simp := rune(entry & 0xFFFF)
		if trad < 0x3400 || trad > 0x9FFF || simp < 0x3400 || simp > 0x9FFF || simp == rune(trad) {
			t.Fatalf("entry[%d] invalid pair 0x%04X -> 0x%04X", i, trad, simp)
		}
		if i > 0 && trad <= prev {
			t.Fatalf("cjkTradToSimpTable not strictly ascending at index %d: 0x%04X <= 0x%04X", i, trad, prev)
		}
		prev = trad
		if got := foldCJKForFingerprint(rune(trad)); got != simp {
			t.Fatalf("foldCJKForFingerprint(0x%04X) = 0x%04X, want 0x%04X", trad, got, simp)
		}
		if again := foldCJKForFingerprint(simp); again != simp {
			t.Fatalf("non-idempotent fold: 0x%04X -> 0x%04X -> 0x%04X", trad, simp, again)
		}
	}
	for _, untouched := range []rune{-1, 'A', 'あ', 0x33FF, 0xA000, 0x20000} {
		if got := foldCJKForFingerprint(untouched); got != untouched {
			t.Fatalf("out-of-range rune 0x%X must remain unchanged, got 0x%X", untouched, got)
		}
	}
	if !slices.Equal(
		NormalizeTitleForDedup("特朗普吁辉达让黄仁勋回任 - 星島頭條", "星岛头条"),
		NormalizeTitleForDedup("特朗普籲輝達讓黃仁勳回任", "星島頭條"),
	) {
		t.Fatal("expected Traditional/Simplified site suffix to strip consistently")
	}

	// 2. 实测真实简繁同稿镜像（星岛头条 3639517 简体版 vs 繁体版）必须精准识别为近重复
	stTitleSimp := NormalizeTitleForDedup("特朗普吁辉达让黄仁勋回任 称习近平赞他是人才 黄同意赴美国会作证", "星島頭條")
	stBodySimp := BuildTextFingerprint("美国总统特朗普再度表态，力挺黄仁勋重返辉达，表明希望黄仁勋郑重道歉后回去工作，并呼吁辉达应给这位全球顶尖人才第2次机会。特朗普还透露，曾跟中国国家主席习近平谈及黄仁勋事件，对方也称赞黄是杰出人才。与此同时，黄仁勋已同意出席参议院公开听证会作证。")
	stTitleTrad := NormalizeTitleForDedup("特朗普籲輝達讓黃仁勳回任 稱習近平讚他是人才 黃同意赴美國會作證", "星島頭條")
	stBodyTrad := BuildTextFingerprint("美國總統特朗普再度表態，力挺黃仁勳重返輝達，表明希望黃仁勳鄭重道歉後回去工作，並呼籲輝達應給這位全球頂尖人才第2次機會。特朗普還透露，曾跟中國國家主席習近平談及黃仁勳事件，對方也稱讚黃是傑出人才。與此同時，黃仁勳已同意出席參議院公開聽證會作證。")
	if !slices.Equal(stTitleSimp, stTitleTrad) {
		t.Fatalf("expected Simplified/Traditional mirrored headlines to fold identically: simp=%v trad=%v", stTitleSimp, stTitleTrad)
	}
	if !FingerprintsNearDuplicate(stTitleSimp, stBodySimp, stTitleTrad, stBodyTrad) {
		t.Fatal("expected Simplified and Traditional mirror copies of the same article to match")
	}

	// 3. 实测中新网繁体 vs 网易简体转载同稿匹配，而不同报道（即便同主题含繁简）绝不误合并
	cnTitleTrad := NormalizeTitleForDedup("Anthropic聯合創始人擬IPO套現116.73億元", "中国新闻网")
	cnBodyTrad := BuildTextFingerprint("中新經緯11月11日電 彭博社11日消息，美國人工智能公司Anthropic的七位聯合創始人將在近期IPO中共同出售價值17.5億美元（約合人民幣116.73億元）的股票，每位創始人預計套現約2.5億美元。")
	cnTitleSimp := NormalizeTitleForDedup("Anthropic联合创始人拟IPO套现116.73亿元", "网易订阅")
	cnBodySimp := BuildTextFingerprint("中新经纬11月11日电 彭博社11日消息，美国人工智能公司Anthropic的七位联合创始人将在近期IPO中共同出售价值17.5亿美元（约合人民币116.73亿元）的股票，每位创始人预计套现约2.5亿美元。")
	if !FingerprintsNearDuplicate(cnTitleTrad, cnBodyTrad, cnTitleSimp, cnBodySimp) {
		t.Fatal("expected Traditional ChinaNews and Simplified NetEase reprint to match")
	}

	hkOtherTitle := NormalizeTitleForDedup("Anthropic上市令員工一夜成富豪 7名創辦人套現156億", "香港商报")
	hkOtherBody := BuildTextFingerprint("Anthropic股票掛牌，絕大多數員工一夕間成百萬美元富翁，有數百人身家甚至破1億美元。這批新富豪恐怕很快將令原已瘋漲的三藩市灣區房價再攀高，截至9月員工總數為5300人。")
	if FingerprintsNearDuplicate(cnTitleSimp, cnBodySimp, hkOtherTitle, hkOtherBody) {
		t.Fatal("distinct Traditional article on same topic must NOT be collapsed")
	}
}
