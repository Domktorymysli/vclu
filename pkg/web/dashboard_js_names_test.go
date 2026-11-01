package web

import (
	"os"
	"regexp"
	"testing"
)

// dashboard.js is one ES5 closure: a second `function x` or `var x` at its top level silently
// replaces the first, so one widget breaks another (a merge left three svgEl with two signatures
// and the energy flow took down the whole page).
func TestDashboardJSTopLevelNamesUnique(t *testing.T) {
	src, err := os.ReadFile("../../web/static/js/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^    (?:function|var) ([A-Za-z_$][\w$]*)`).FindAllSubmatch(src, -1) {
		name := string(m[1])
		if seen[name] {
			t.Errorf("%s is declared twice at the top level of dashboard.js", name)
		}
		seen[name] = true
	}
	if len(seen) < 50 {
		t.Fatalf("only %d top-level names found: the indentation of dashboard.js changed", len(seen))
	}
}
