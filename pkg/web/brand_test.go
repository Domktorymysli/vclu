package web

import "testing"

func TestBrandHTML(t *testing.T) {
	const v = `<span class="vclu">vclu</span>`
	cases := map[string]string{
		"vclu":                            v,
		"Nowa wersja vclu":                "Nowa wersja " + v,
		"vclu trzyma metryki.":            v + " trzyma metryki.",
		"Zrestartować vclu?":              "Zrestartować " + v + "?",
		"przez vclu — z reguł":            "przez " + v + " — z reguł",
		"koniec: vclu.":                   "koniec: " + v + ".",
		"(vclu)":                          "(" + v + ")",
		"vclu.pl, /opt/vclu, vclu-update": "vclu.pl, /opt/vclu, vclu-update",
		"vclu_session vclu:disk vcluX":    "vclu_session vclu:disk vcluX",
		"<b>vclu</b> & co":                "&lt;b&gt;" + v + "&lt;/b&gt; &amp; co",
	}
	for in, want := range cases {
		if got := string(brandHTML(in)); got != want {
			t.Errorf("brandHTML(%q) = %q, want %q", in, got, want)
		}
	}
}
