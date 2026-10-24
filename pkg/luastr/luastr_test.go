package luastr

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestQuoteRoundTrips(t *testing.T) {
	for _, s := range []string{
		"", "plain", `pa"ss`, `back\slash`, `end\`, "two\nlines", "cr\rlf", "nul\x00byte", "nul\x001digit",
		`]]`, `"); os.exit(1) --`, "zażółć", "\\\"\n",
	} {
		L := lua.NewState()
		if err := L.DoString("return " + Quote(s)); err != nil {
			t.Fatalf("Quote(%q) = %s: %v", s, Quote(s), err)
		}
		if got := L.Get(-1).String(); got != s {
			t.Errorf("Quote(%q) reads back as %q", s, got)
		}
		L.Close()
	}
}
