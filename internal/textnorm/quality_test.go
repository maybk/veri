package textnorm

import (
	"strings"
	"testing"
)

func TestDetectChallengeWallAndEvaluatePageQuality(t *testing.T) {
	// 1. 现代反爬盾：Anubis PoW、Cloudflare Turnstile、中文人机验证
	for _, tc := range []struct {
		title string
		body  string
	}{
		{title: "Making sure you're not a bot!", body: "Loading Anubis proof-of-work challenge..."},
		{title: "Just a moment...", body: "Performing security verification. Cloudflare Ray ID: 99aabbcc"},
		{title: "Vercel Security Checkpoint", body: "We are verifying your browser."},
		{title: "访问拦截", body: "请完成滑动验证以继续访问当前网页。"},
	} {
		q := EvaluatePageQuality(tc.title, tc.body, 300)
		if !q.LowQuality || !q.IsWall || q.Reason != QualityReasonBotOrJSWall {
			t.Fatalf("expected bot_or_js_wall for title=%q, got %+v", tc.title, q)
		}
	}

	// 2. 软 404 空壳页识别（HTTP 200 返回 404 标题与简短文案）
	soft404 := EvaluatePageQuality("404 Not Found | Docs", "# Page not found\n\nThe requested URL does not exist.", 300)
	if !soft404.LowQuality || !soft404.IsWall || soft404.Reason != QualityReasonSoft404 {
		t.Fatalf("expected soft_404 wall, got %+v", soft404)
	}

	// 3. 代码块中出现 "403 Forbidden" / "Access Denied" 的技术文档绝不误判为反爬盾，且精炼代码块文档豁免 too_short
	conciseAPIDoc := "# Auth Error Reference\n\nWhen token scope is missing, the gateway returns:\n\n```http\nHTTP/1.1 403 Forbidden\nAccess Denied: missing scope read:org\n```\n\nRotate the token and retry."
	apiQ := EvaluatePageQuality("Auth Error Reference", conciseAPIDoc, 300)
	if apiQ.LowQuality {
		t.Fatalf("expected concise API doc with code block (and '403 Forbidden' inside fence) to pass quality gate, got %+v", apiQ)
	}

	// 4. 图片语法 ![alt](url) 与含括号链接不误触发 link_heavy_shell
	imgDoc := "# Architecture Diagram\n\n![System Architecture Diagram Overview](https://cdn.example.com/a(1).png)\n\n" +
		"See [Go Wiki](https://en.wikipedia.org/wiki/Go_(programming_language)) for background details.\n\n" +
		strings.Repeat("Deterministic memory layout improves cache locality across concurrent workers. ", 4)
	imgQ := EvaluatePageQuality("Architecture", imgDoc, 200)
	if imgQ.LowQuality {
		t.Fatalf("expected doc with images and balanced-paren wiki URL to pass, got %+v", imgQ)
	}

	// 5. 纯导航链接壳页仍准确拦截为 link_heavy_shell
	navShell := strings.Repeat("[Product Navigation Item](https://example.com/item) ", 25)
	navQ := EvaluatePageQuality("Site Map", navShell, 100)
	if !navQ.LowQuality || navQ.IsWall || navQ.Reason != QualityReasonLinkHeavyShell {
		t.Fatalf("expected link_heavy_shell, got %+v", navQ)
	}
}
