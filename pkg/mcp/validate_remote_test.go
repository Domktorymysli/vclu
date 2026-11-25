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
		// read-only or hidden: nothing to command from the cloud
		{"alarm, :readonly()", `expose(STREFA, "switch", { name = "Alarm" }):remote():readonly()`, ""},
		{"gate hidden", `expose(BRAMA, "switch", { name = "Brama", hidden = true }):remote()`, ""},
		// names that do not make a device sensitive
		{"light at the gate", `expose(OUT1, "light", { id = "brama_lampa", name = "Brama" }):remote()`, ""},
		{"dimmer in a zone", `expose(DIM1, "dimmer", { name = "Strefa dzienna" }):remote()`, ""},
		{"alarm clock light", `expose(OUT2, "light", { name = "Alarm świetlny budzik" }):remote()`, ""},
		{"roller in the garage", `expose(ROL1, "cover", { name = "Roleta garaż" }):remote()`, ""},
		{"zone switch", `expose(OUT5, "switch", { name = "Strefa dzienna" }):remote()`, ""},
		{"gateway", `expose(OUT6, "switch", { id = "gateway_reset", name = "Gateway" }):remote()`, ""},
		{"satellite", `expose(OUT7, "switch", { name = "Satelita" }):remote()`, ""},
		{"doorbell", `expose(OUT8, "switch", { name = "Doorbell chime" }):remote()`, ""},
		{"cover gate by name", `expose(ROL2, "cover", { name = "Brama wjazdowa" }):remote()`, "confirm"},
		{"cover garage by class", `expose(ROL3, "cover", { name = "G1", device_class = "garage" }):remote()`, "confirm"},
		{"cover door by class", `expose(ROL4, "cover", { name = "Wejście", device_class = "door" }):remote()`, "pin"},
		{"lock type", `expose(L1, "lock", { name = "Wejście" }):remote()`, "pin"},
		// words joined by _, . or CamelCase
		{"snake case lock", `expose(OUT1, "switch", { id = "front_door_lock" }):remote()`, "pin"},
		{"snake case gate", `expose(OUT1, "switch", { id = "main_gate" }):remote()`, "confirm"},
		{"snake case alarm", `expose(OUT1, "switch", { id = "dom_alarm" }):remote()`, "pin"},
		{"camel case gate", `expose(CLU1.WjazdBrama, "switch", { id = "wjazd" }):remote()`, "confirm"},
		// Lua call forms and a handle kept in a variable
		{"call without parens", `expose(BRAMA, "switch", { name = "Brama" }):remote{ }`, "confirm"},
		{"pin in a table without parens", `expose(BRAMA, "switch", { name = "Brama" }):remote{ pin = true }:pin"1234"`, ""},
		{"name without parens", `expose(OUT3, "switch"):name"Brama":remote()`, "confirm"},
		{"name in single quotes", `expose(OUT3, "switch"):name'Brama':remote()`, "confirm"},
		{"handle in a local", "local h = expose(BRAMA, \"switch\", { name = \"Brama\" })\nh:remote()", "confirm"},
		{"handle guarded later", "local h = expose(BRAMA, \"switch\", { name = \"Brama\" })\nh:remote()\nh:confirm()", ""},
		{"handle in a global", "gate = expose(OUT3, \"switch\", { name = \"Brama\" }):confirm()\ngate:remote()", ""},
		{"handle assigned again", "local h = expose(BRAMA, \"switch\", { name = \"Brama\" })\nh = expose(OUT1, \"light\", {})\nh:remote()", ""},
		// long brackets, comments inside the call, the type after the first comma
		{"long comment with level", "--[==[ expose(BRAMA, \"switch\", { name = \"Brama\" }):remote() ]] ]==]", ""},
		{"long string with level", "local s = [==[ expose(BRAMA, \"switch\", { name = \"Brama\" }):remote() ]] ]==]", ""},
		{"code after a long comment", "--[==[ ]] ]==] expose(BRAMA, \"switch\", { name = \"Brama\" }):remote()", "confirm"},
		{"keyword in a comment in the call", "expose(OUT1, \"switch\", { name = \"Lampa\", -- not the gate (brama)\n}):remote()", ""},
		{"sensor with a call in the object", `expose(get("drzwi", 1), "binary_sensor", { name = "Wejście" }):remote()`, ""},
		{"lock with a call in the object", `expose(get("drzwi", 1), "switch", { name = "Wejście" }):remote()`, "pin"},
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
