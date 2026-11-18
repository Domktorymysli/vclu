package lua

import (
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
