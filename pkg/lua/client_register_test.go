package lua

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// A bad registration is refused and kept out of the subscriptions, and a broken entry is
// dropped by the reporter: the reports of other clients go on.
func TestClientRegisterBadEntryDoesNotStopReports(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		lamp = { value = 1, get = function(self, i) return self.value end }
		myVar = 5
		SYSTEM:clientRegister("10.0.0.1", 1000, 1, {{lamp,0}, {nosuch,0}})
		SYSTEM:getShared():getPendingClientReports()
	`); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{
		`SYSTEM:clientRegister("10.0.0.2", 1000, 2, {{myVar,0}})`,
		`SYSTEM:clientRegister("10.0.0.2", 1000, 2, {lamp,0})`,
		`SYSTEM:clientRegister('a"b', 1000, 2, {{lamp,0}})`,
		`SYSTEM:clientRegister("10.0.0.2", "x", 2, {{lamp,0}})`,
	} {
		if _, err := engine.Execute(bad); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
	if n, _ := engine.Execute(`return SYSTEM:getShared():getClientSubscriptionCount()`); n != "1" {
		t.Fatalf("subscriptions after bad registrations: %s, want 1", n)
	}

	// An entry broken after registering (e.g. by a reload of the object) is dropped
	if _, err := engine.Execute(`
		SYSTEM:getShared().clientSubscriptions["10.0.0.3:1000:3"] = { ip = "10.0.0.3", port = 1000, sessionID = 3, subscriptions = {5} }
		lamp.value = 0
	`); err != nil {
		t.Fatal(err)
	}
	got, err := engine.Execute(`return SYSTEM:getShared():getPendingClientReports()`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"sessionID":1,"values":"{0,nil}"`) {
		t.Errorf("reports: %s, want the report of session 1", got)
	}
	if n, _ := engine.Execute(`return SYSTEM:getShared():getClientSubscriptionCount()`); n != "1" {
		t.Errorf("subscriptions after the broken entry: %s, want 1", n)
	}
}

// A string value with quotes, backslashes or new lines keeps the report valid JSON, its
// values a valid Lua table, and the reports of other clients go on.
func TestClientReportEscapesStrings(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		txt = { value = "a\"b\\c\nd\0e", get = function(self) return self.value end }
		lamp = { value = 1, get = function(self) return self.value end }
		SYSTEM:clientRegister("10.0.0.1", 1000, 1, {{txt,0}})
		SYSTEM:clientRegister("10.0.0.2", 1000, 2, {{lamp,0}})`)
	got := mustExec(t, engine, `return SYSTEM:getShared():getPendingClientReports()`)
	var reports []struct {
		IP        string `json:"ip"`
		Port      int    `json:"port"`
		SessionID int    `json:"sessionID"`
		Values    string `json:"values"`
	}
	if err := json.Unmarshal([]byte(got), &reports); err != nil || len(reports) != 2 {
		t.Fatalf("reports %s: %v", got, err)
	}
	for _, r := range reports {
		if r.SessionID != 1 {
			continue
		}
		// the values read back in Lua as the same string
		back := mustExec(t, engine, `local t = loadstring("return "..`+strconv.Quote(r.Values)+`)() return tostring(t[1] == txt.value)`)
		if back != "true" || strings.Contains(r.Values, "\n") {
			t.Errorf("values %q", r.Values)
		}
	}
	if got := mustExec(t, engine, `myVar = "x\"y" return SYSTEM:fetchValues({"myVar"})`); got != `values:{"x\"y"}` {
		t.Errorf("fetchValues %s", got)
	}
}

// A port outside 1-65535 is refused (0,0 is replaced by the sender's address before Lua).
func TestClientRegisterPortRange(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `lamp = { get = function() return 1 end }`)
	for _, port := range []string{"0", "-1", "65536", "1.5", "1e9"} {
		if _, err := engine.Execute(`SYSTEM:clientRegister("10.0.0.1", ` + port + `, 1, {{lamp,0}})`); err == nil {
			t.Errorf("port %s accepted", port)
		}
	}
	for _, port := range []string{"1", "65535"} {
		if _, err := engine.Execute(`SYSTEM:clientRegister("10.0.0.1", ` + port + `, 1, {{lamp,0}})`); err != nil {
			t.Errorf("port %s: %v", port, err)
		}
	}
}
