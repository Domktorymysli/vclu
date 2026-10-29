package lua

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Before a Lua state goes away (reload, shutdown, a start that never ran the loop) every
// VirtualThermostat switches its output off, whatever the next modules do (#63).

// sentCommands records what Lua sends to a CLU without a reply.
type sentCommands struct {
	mu   sync.Mutex
	list []string
}

func (s *sentCommands) send(ip, script string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, script)
	return nil
}

func (s *sentCommands) take() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := strings.Join(s.list, ";")
	s.list = nil
	return out
}

// A Grenton relay on a CLU, heating: the command to it goes through Go.
const heatingModule = `
CLU_FAKE = { executeNoReply = function(self, m, s) __go_send_command_noreply("192.168.0.3", s) end }
RELAY = RemoteSwitch:new(CLU_FAKE, "DOU1", "relay")
TEMP = VirtualNumber:new("t")
TH = VirtualThermostat:new("salon", { source = TEMP, output = RELAY, setpoint = 21 })
TEMP:setValue(START_TEMP or 20)
`

const relayOff = "DOU1:execute(2, 0)"
const relayOn = "DOU1:execute(1, 0)"

func newReleaseEngine(t *testing.T, module string) (*LuaEngine, *sentCommands, string) {
	t.Helper()
	dir := createTestModulesDir(t, map[string]string{"heating.lua": module})
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	rec := &sentCommands{}
	engine.SetCommandNoReplySender(rec.send)
	engine.LoadUserModules(dir)
	if got := rec.take(); got != relayOff+";"+relayOn {
		t.Fatalf("module start: sent %q, want off then on", got)
	}
	return engine, rec, dir
}

func writeModule(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "modules", "heating.lua"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// A reload whose module no longer loads, or with the module removed, leaves the relay off.
func TestVirtualThermostat_ReloadReleasesOutput(t *testing.T) {
	for _, tc := range []struct{ name, next string }{
		{"syntax error", "this is not lua (("},
		{"removed", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, rec, dir := newReleaseEngine(t, heatingModule)
			if tc.next == "" {
				if err := os.Remove(filepath.Join(dir, "modules", "heating.lua")); err != nil {
					t.Fatal(err)
				}
			} else {
				writeModule(t, dir, tc.next)
			}
			if err := engine.Reinitialize("CLU"); err != nil {
				t.Fatal(err)
			}
			engine.LoadUserModules(dir)
			if got := rec.take(); got != relayOff {
				t.Errorf("sent on reload: %q, want %q", got, relayOff)
			}
		})
	}
}

// Shutdown with the loop running and a Close before it ever ran: the off command goes out.
func TestVirtualThermostat_ShutdownReleasesOutput(t *testing.T) {
	t.Run("run loop", func(t *testing.T) {
		engine, rec, _ := newReleaseEngine(t, heatingModule)
		stop := make(chan struct{})
		go engine.RunLoop(stop)
		for engineState(engine.engineState.Load()) != stateRunning {
			time.Sleep(time.Millisecond)
		}
		close(stop)
		engine.Close()
		if got := rec.take(); got != relayOff {
			t.Errorf("sent at shutdown: %q, want %q", got, relayOff)
		}
	})
	t.Run("never ran", func(t *testing.T) {
		engine, rec, _ := newReleaseEngine(t, heatingModule)
		engine.Close()
		if got := rec.take(); got != relayOff {
			t.Errorf("sent at close: %q, want %q", got, relayOff)
		}
	})
}

// An output already off gets no extra command; one output that fails does not keep the
// others on.
func TestVirtualThermostat_ReleaseOnlyWhatIsOnAndAll(t *testing.T) {
	dir := createTestModulesDir(t, map[string]string{"heating.lua": "START_TEMP = 22\n" + heatingModule})
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	rec := &sentCommands{}
	engine.SetCommandNoReplySender(rec.send)
	engine.LoadUserModules(dir)
	if got := rec.take(); got != relayOff {
		t.Fatalf("start above the target: sent %q, want only the first off", got)
	}
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); got != "" {
		t.Errorf("an output already off got %q on reload, want nothing", got)
	}

	// a broken output first in the table or not: the relay is released either way
	writeModule(t, dir, `
BROKEN = { switchOn = function() end, switchOff = function() error("bus down") end }
VirtualThermostat:new("a", { source = VirtualNumber:new("ta"), output = BROKEN })
`+heatingModule+`
VirtualThermostat:new("b", { source = TEMP, output = BROKEN })
`)
	engine.LoadUserModules(dir)
	rec.take()
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); got != relayOff {
		t.Errorf("with a failing output: sent %q, want %q", got, relayOff)
	}
}
