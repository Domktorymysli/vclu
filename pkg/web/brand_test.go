package web

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

const brandSpan = `<span class="vclu">vclu</span>`

// brandCases hold for brandHTML in templates and VCLU.brand in utils.js alike.
var brandCases = map[string]string{
	"vclu":                            brandSpan,
	"Nowa wersja vclu":                "Nowa wersja " + brandSpan,
	"vclu trzyma metryki.":            brandSpan + " trzyma metryki.",
	"Zrestartować vclu?":              "Zrestartować " + brandSpan + "?",
	"przez vclu — z reguł":            "przez " + brandSpan + " — z reguł",
	"koniec: vclu.":                   "koniec: " + brandSpan + ".",
	"(vclu)":                          "(" + brandSpan + ")",
	"vclu.pl, /opt/vclu, vclu-update": "vclu.pl, /opt/vclu, vclu-update",
	"vclu_session vclu:disk vcluX":    "vclu_session vclu:disk vcluX",
	"vcluvclu vclu":                   "vcluvclu " + brandSpan,
	"<b>vclu</b> & co":                "&lt;b&gt;" + brandSpan + "&lt;/b&gt; &amp; co",
}

func TestBrandHTML(t *testing.T) {
	for in, want := range brandCases {
		if got := string(brandHTML(in)); got != want {
			t.Errorf("brandHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBrandJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	var inputs []string
	for in := range brandCases {
		inputs = append(inputs, in)
	}
	stdin, _ := json.Marshal(inputs)
	cmd := exec.Command(node, "testdata/brand.js", "../../web/static/js/utils.js")
	cmd.Stdin = strings.NewReader(string(stdin))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("node output %q: %v", out, err)
	}
	for i, in := range inputs {
		if got[i] != brandCases[in] {
			t.Errorf("VCLU.brand(%q) = %q, want %q", in, got[i], brandCases[in])
		}
	}
}
