package htmlmd

import (
	"strings"
	"testing"
)

func TestExtractMathMediaWikiAndMathMLLinearization(t *testing.T) {
	t.Run("mediawiki_data_mw_extsrc", func(t *testing.T) {
		raw := `<p>Bayes theorem states <math xmlns="http://www.w3.org/1998/Math/MathML" class="mwe-math-element mwe-math-element-inline" data-mw='{"name":"math","attrs":{},"body":{"extsrc":"P(A\\vert B) = \\frac{P(B \\vert A) P(A)}{P(B)}"}}'><mrow><mi>P</mi><mo>(</mo><mi>A</mi><mo>|</mo><mi>B</mi><mo>)</mo><mo>=</mo><mfrac><mrow><mi>P</mi><mo>(</mo><mi>B</mi><mo>|</mo><mi>A</mi><mo>)</mo><mi>P</mi><mo>(</mo><mi>A</mi><mo>)</mo></mrow><mrow><mi>P</mi><mo>(</mo><mi>B</mi><mo>)</mo></mrow></mfrac></mrow></math>.</p>`
		got, err := ConvertString(raw, Options{})
		if err != nil {
			t.Fatalf("ConvertString: %v", err)
		}
		if !strings.Contains(got, `$P(A\vert B) = \frac{P(B \vert A) P(A)}{P(B)}$`) {
			t.Fatalf("expected MediaWiki data-mw LaTeX extracted, got:\n%s", got)
		}
	})

	t.Run("pure_mathml_fallback_linearization", func(t *testing.T) {
		raw := `<p>Formula: <math display="block"><mrow><mi>x</mi><mo>=</mo><mfrac><mrow><mo>-</mo><mi>b</mi><mo>±</mo><msqrt><mrow><msup><mi>b</mi><mn>2</mn></msup><mo>-</mo><mn>4</mn><mi>a</mi><mi>c</mi></mrow></msqrt></mrow><mrow><mn>2</mn><mi>a</mi></mrow></mfrac></mrow></math></p>`
		got, err := ConvertString(raw, Options{})
		if err != nil {
			t.Fatalf("ConvertString: %v", err)
		}
		if !strings.Contains(got, "$$") || !strings.Contains(got, `\frac{`) || !strings.Contains(got, `\sqrt{`) || !strings.Contains(got, `b^{2}`) {
			t.Fatalf("expected linearized MathML LaTeX block, got:\n%s", got)
		}
	})

	t.Run("math_alttext_and_sub_root", func(t *testing.T) {
		raw := `<p>Alt: <math alttext="a_i + \sqrt[3]{x}"><mi>ignored</mi></math> and raw <math><msub><mi>x</mi><mi>i</mi></msub><mo>+</mo><mroot><mi>y</mi><mn>3</mn></mroot></math></p>`
		got, err := ConvertString(raw, Options{})
		if err != nil {
			t.Fatalf("ConvertString: %v", err)
		}
		if !strings.Contains(got, `$a_i + \sqrt[3]{x}$`) || !strings.Contains(got, `$x_{i} + \sqrt[3]{y}$`) {
			t.Fatalf("unexpected alttext/sub/mroot output:\n%s", got)
		}
	})

	t.Run("mathml_operators_scripts_and_fenced", func(t *testing.T) {
		raw := `<p><math><munderover><mo>∑</mo><mrow><mi>i</mi><mo>=</mo><mn>1</mn></mrow><mi>n</mi></munderover><msubsup><mi>x</mi><mi>i</mi><mn>2</mn></msubsup><mo>+</mo><munder><mi>a</mi><mi>b</mi></munder><mo>+</mo><mover><mi>y</mi><mo>^</mo></mover><mo>+</mo><mfenced open="[" close="]"><mi>u</mi><mi>v</mi></mfenced><mspace width="1em"/><merror><mtext>err</mtext></merror></math></p>`
		got, err := ConvertString(raw, Options{})
		if err != nil {
			t.Fatalf("ConvertString: %v", err)
		}
		if !strings.Contains(got, `_{i = 1}^{n}`) || !strings.Contains(got, `x_{i}^{2}`) || !strings.Contains(got, `[u, v]`) || strings.Contains(got, "err") {
			t.Fatalf("unexpected mathml operators output:\n%s", got)
		}
	})
}
