package updatecheck

import (
	"strings"
	"testing"
)

func TestRenderNotes(t *testing.T) {
	for _, c := range []struct{ name, md, want string }{
		{"bold and paragraph", "**Highlights:** a thermostat.", "<p><strong>Highlights:</strong> a thermostat.</p>"},
		{"list", "**New**\n- one\n- two `x < y`\n\nend",
			"<p><strong>New</strong></p><ul><li>one</li><li>two <code>x &lt; y</code></li></ul><p>end</p>"},
		{"https link", "see [#63](https://github.com/Domktorymysli/vclu/issues/63).",
			`<p>see <a href="https://github.com/Domktorymysli/vclu/issues/63" target="_blank" rel="noopener noreferrer">#63</a>.</p>`},
		{"lines of a paragraph", "a\nb", "<p>a<br>b</p>"},
		{"code is not reworked", "`**x**`", "<p><code>**x**</code></p>"},
		{"code in a link", "[`a`](https://x.y/a)", `<p><a href="https://x.y/a" target="_blank" rel="noopener noreferrer"><code>a</code></a></p>`},
		{"script", "<script>alert(1)</script>", "<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>"},
		{"raw html in a list", "- <img src=x onerror=alert(1)>", "<ul><li>&lt;img src=x onerror=alert(1)&gt;</li></ul>"},
		{"javascript link", "[x](javascript:alert(1))", "<p>[x](javascript:alert(1))</p>"},
		{"http link", "[x](http://a.b)", "<p>[x](http://a.b)</p>"},
		{"data link", "[x](data:text/html,hi)", "<p>[x](data:text/html,hi)</p>"},
		{"quote in url", `[x](https://a.b/"onmouseover="alert)`,
			`<p><a href="https://a.b/&#34;onmouseover=&#34;alert" target="_blank" rel="noopener noreferrer">x</a></p>`},
		{"nul bytes", "a\x000\x00b", "<p>a0b</p>"},
	} {
		if got := RenderNotes(c.md); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestRenderInline(t *testing.T) {
	for md, want := range map[string]string{
		"a **virtual** thermostat\nfor `any` sensor":   "a <strong>virtual</strong> thermostat for <code>any</code> sensor",
		"see [#39](https://x.y/39) <script>x</script>": `see <a href="https://x.y/39" target="_blank" rel="noopener noreferrer">#39</a> &lt;script&gt;x&lt;/script&gt;`,
		"[x](javascript:alert(1))":                     "[x](javascript:alert(1))",
		"[x](https://`y`)":                             "[x](https://<code>y</code>)",
	} {
		if got := RenderInline(md); got != want {
			t.Errorf("%q:\n got %s\nwant %s", md, got, want)
		}
	}
}

func TestRenderNotesNoTagsFromInput(t *testing.T) {
	md := "<b>x</b> **<i>y</i>** [<u>z</u>](https://a.b) `<s>` - <a href=\"javascript:1\">q</a>"
	got := RenderNotes(md)
	for _, tag := range []string{"<b>", "<i>", "<u>", "<s>", "<a href=\"javascript"} {
		if strings.Contains(got, tag) {
			t.Errorf("%q made it through: %s", tag, got)
		}
	}
}
