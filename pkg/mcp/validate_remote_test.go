package mcp

import (
	"strings"
	"testing"
)

func TestRemoteWarnings(t *testing.T) {
	tests := []struct {
		name, code string
		want       string // "" no warning, "pin" or "confirm"
	}{
		{"satel partition, no pin", `expose(_registry["satel.partition.1"], "switch", { id = "alarm", name = "Alarm" }):remote()`, "pin"},
		{"gate alarm zone, no pin", `expose(_:get("CLU221000123.SAT0001"), "switch", { name = "Parter" }):remote():roles('domownicy')`, "pin"},
		{"alarm with pin", `expose(STREFA, "switch", { name = "Alarm parter" }):pin("4821"):remote()`, ""},
		{"alarm, pin in remote table", `expose(STREFA, "switch", { name = "Alarm" }):remote({ pin = true }):pin(4821)`, ""},
		{"alarm, confirm is not enough", `expose(STREFA, "switch", { name = "Alarm" }):confirm():remote()`, "pin"},
		{"alarm read only", `expose(STREFA, "switch", { name = "Alarm" }):remote({ readonly = true })`, ""},
		{"alarm read only in options", `expose(STREFA, "switch", { name = "Alarm", readonly = true }):remote()`, ""},
		{"alarm kept home", `expose(STREFA, "switch", { name = "Alarm" }):remote(false)`, ""},
		{"alarm not remote", `expose(STREFA, "switch", { name = "Alarm" })`, ""},
		{"alarm sensor", `expose(_registry["satel.partition.1.alarm"], "binary_sensor", { name = "Alarm" }):remote()`, ""},
		{"door lock", `expose(ZAMEK, "switch", { id = "zamek", name = "Zamek drzwi" }):remote()`, "pin"},
		{"gate, no confirm", `expose(BRAMA, "switch", { id = "brama", name = "Brama" }):remote()`, "confirm"},
		{"gate by pulse", `expose(OUT3, "switch", { id = "wjazd", name = "Wjazd", pulse = 1000 }):remote()`, "confirm"},
		{"gate by class", `expose(OUT3, "cover", { name = "Wjazd", device_class = "gate" }):remote()`, "confirm"},
		{"garage door type", `expose(OUT4, "garage_door", { name = "G1" }):remote()`, "confirm"},
		{"gate with confirm", `expose(BRAMA, "switch", { name = "Brama" }):confirm():remote()`, ""},
		{"gate, confirm taken back", `expose(BRAMA, "switch", { name = "Brama" }):confirm(false):remote()`, "confirm"},
		{"gate with pin", `expose(BRAMA, "switch", { name = "Garaż" }):pin("1111"):remote()`, ""},
		{"lamp", `expose(SALON, "light", { name = "Lampa" }):remote()`, ""},
		{"in a comment", `-- expose(BRAMA, "switch", { name = "Brama" }):remote()`, ""},
		{"in a string", `local s = 'expose(BRAMA, "switch", { name = "Brama" }):remote()'`, ""},
		{"across lines", "expose(BRAMA, \"switch\", {\n    id = \"brama\",\n    name = \"Brama\",\n})\n    :remote()", "confirm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remoteWarnings(tt.code)
			switch {
			case tt.want == "" && len(got) > 0:
				t.Errorf("unexpected warning: %v", got)
			case tt.want != "" && len(got) != 1:
				t.Fatalf("want one %s warning, got %v", tt.want, got)
			case tt.want == "pin" && !strings.Contains(got[0], ":pin(...)") && !strings.Contains(got[0], "without :pin"):
				t.Errorf("want a PIN warning, got %q", got[0])
			case tt.want == "confirm" && !strings.Contains(got[0], "without :confirm()"):
				t.Errorf("want a confirm warning, got %q", got[0])
			}
		})
	}
}

// The draft stays valid: the owner may want it, the agent has to say so.
func TestValidateDraft_RemoteGateIsAWarning(t *testing.T) {
	report := ValidateDraft("local x = 1\nexpose(BRAMA, \"switch\", { name = \"Brama\" }):remote()\n")
	if !report.Valid || len(report.Warnings) != 1 || !strings.HasPrefix(report.Warnings[0], "line 2:") {
		t.Errorf("report = %+v", report)
	}
}
