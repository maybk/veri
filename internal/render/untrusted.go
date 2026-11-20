package render

import (
	"veri/clikit/term"
	"veri/internal/spec"
)

// WrapUntrusted 将外部网页文本包裹在带调用级随机边界标识的 <untrusted_web_content id="..."> 标签内，并将正文中伪造的同名标签替换为 [removed-tag]。
func WrapUntrusted(content string) string {
	return term.WrapUntrustedRandom(spec.UntrustedWebTag, content)
}
