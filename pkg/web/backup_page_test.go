package web

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// One click on a Backup page button sends one request (#102).

var (
	buttonTagRe = regexp.MustCompile(`(?s)<button\b[^>]*>`)
	idAttrRe    = regexp.MustCompile(`\bid="([^"]*)"`)
	onclickRe   = regexp.MustCompile(`\bonclick="([^"]*)"`)
)

type pageButton struct {
	ID      string `json:"id"`
	Onclick string `json:"onclick"`
}

func backupPageButtons(t *testing.T) []pageButton {
	t.Helper()
	html, err := os.ReadFile("../../web/templates/backup.html")
	if err != nil {
		t.Fatal(err)
	}
	var buttons []pageButton
	for _, tag := range buttonTagRe.FindAll(html, -1) {
		var b pageButton
		if m := idAttrRe.FindSubmatch(tag); m != nil {
			b.ID = string(m[1])
		}
		if m := onclickRe.FindSubmatch(tag); m != nil {
			b.Onclick = string(m[1])
		}
		buttons = append(buttons, b)
	}
	return buttons
}

func TestBackupPageOneRequestPerClick(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	data, err := json.Marshal(backupPageButtons(t))
	if err != nil {
		t.Fatal(err)
	}
	buttonsFile := filepath.Join(t.TempDir(), "buttons.json")
	if err := os.WriteFile(buttonsFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "testdata/backup_clicks.js", "../../web/static/js/backup.js", buttonsFile).Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var got map[string][]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}

	count := func(scenario, request string) int {
		n := 0
		for _, r := range got[scenario] {
			if r == request {
				n++
			}
		}
		return n
	}
	cases := []struct{ scenario, request string }{
		{"create", "POST /api/backup/create"},
		{"createTwice", "POST /api/backup/create"},
		{"createLegacyTwice", "POST /api/backup/create"},
		{"save", "POST /api/backup/settings"},
		{"refresh", "GET /api/backup/list"},
	}
	for _, c := range cases {
		if n := count(c.scenario, c.request); n != 1 {
			t.Errorf("%s: %d x %s, want 1 (requests %v)", c.scenario, n, c.request, got[c.scenario])
		}
	}
}
