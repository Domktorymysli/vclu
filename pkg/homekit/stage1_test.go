package homekit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/brutella/hap/characteristic"
)

// recLua records scripts; GetCurrentState gets state.
type recLua struct {
	mu      sync.Mutex
	scripts []string
	state   string
}

func (r *recLua) Execute(script string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scripts = append(r.scripts, script)
	if isStateRead(script) {
		return r.state, nil
	}
	return "", nil
}

func (r *recLua) writes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, s := range r.scripts {
		if strings.Contains(s, "setValue") || strings.Contains(s, "executeScene") {
			out = append(out, s)
		}
	}
	return out
}

// The Home app ties rooms and automations to the accessory id: an object keeps
// its id across restarts and an id is never handed to another object.
func TestAccessoryIDsAreStable(t *testing.T) {
	dir := t.TempDir()
	objs := []ExposedObject{
		{Path: "b", Type: "switch", Name: "B"},
		{Path: "a", Type: "switch", Name: "A"},
		{Path: "c", Type: "temperature", Name: "C"},
	}
	ids := func(s *Server, objs []ExposedObject) map[string]uint64 {
		out := map[string]uint64{}
		for p, w := range s.buildAccessories(objs) {
			out[p] = w.Accessory.Id
		}
		if err := s.aidStore().save(); err != nil {
			t.Fatal(err)
		}
		return out
	}

	first := ids(&Server{aids: loadAIDStore(dir)}, objs)
	for p, id := range first {
		if id < firstAID {
			t.Errorf("%s got reserved id %d", p, id)
		}
	}
	// restart, in another order
	second := ids(&Server{aids: loadAIDStore(dir)}, []ExposedObject{objs[2], objs[0], objs[1]})
	for p := range first {
		if first[p] != second[p] {
			t.Errorf("%s: id %d after restart, was %d", p, second[p], first[p])
		}
	}
	// "a" removed, "d" added: d must not take a's id
	third := ids(&Server{aids: loadAIDStore(dir)}, []ExposedObject{objs[0], objs[2], {Path: "d", Type: "switch", Name: "D"}})
	for p, id := range first {
		if third["d"] == id {
			t.Errorf("new object reused the id of %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "homekit-aids.json")); err != nil {
		t.Errorf("ids not saved: %v", err)
	}
}

func TestTypesWithoutAccessoryAreLeftOut(t *testing.T) {
	s := &Server{}
	for _, typ := range []string{"number", "button", "lock", "garage_door", "climate", "something"} {
		if w := s.createExposedAccessory(ExposedObject{Path: "x", Type: typ, Name: "X", Value: float64(1)}, false); w != nil {
			t.Errorf("%s became an accessory; a switch would let the Home app write 0/1 into it", typ)
		}
	}
}

func TestSensorAccessories(t *testing.T) {
	s := &Server{}
	lux := s.createExposedAccessory(ExposedObject{Path: "l", Type: "illuminance", Name: "L", Value: float64(0)}, false)
	if lux == nil || findChar(lux.Accessory, characteristic.TypeCurrentAmbientLightLevel) == nil {
		t.Fatal("illuminance is not a light sensor")
	}
	if v := findChar(lux.Accessory, characteristic.TypeCurrentAmbientLightLevel).Value(); v.(float64) < 0.0001 {
		t.Errorf("light level %v below the HAP minimum", v)
	}

	contact := s.createExposedAccessory(ExposedObject{Path: "c", Type: "binary_sensor", Name: "C", Value: float64(1)}, false)
	c := findChar(contact.Accessory, characteristic.TypeContactSensorState)
	if c == nil || c.Value().(int) != characteristic.ContactSensorStateContactNotDetected {
		t.Fatalf("binary_sensor 1 must be an open contact, got %v", c)
	}
	contact.UpdateFunc(0)
	if c.Value().(int) != characteristic.ContactSensorStateContactDetected {
		t.Error("binary_sensor 0 must be a closed contact")
	}

	therm := s.createExposedAccessory(ExposedObject{Path: "t", Type: "temperature", Name: "T", Value: float64(-7.5)}, false)
	if v := findChar(therm.Accessory, characteristic.TypeCurrentTemperature).Value(); v.(float64) != -7.5 {
		t.Errorf("outdoor -7.5 °C shown as %v", v)
	}
}

// Satel objects are read-only in the Home app (decision 2026-10-09).
func TestSatelIsReadonly(t *testing.T) {
	s := &Server{
		luaExecutor:   fakeLua{json: `[{"path":"satel.partition.1","type":"switch","name":"Parter","value":0,"readonly":false,"satel":true,"caps":{}}]`},
		accessManager: accesscontrol.NewManager(t.TempDir()),
	}
	objs, err := s.fetchExposed()
	if err != nil {
		t.Fatal(err)
	}
	w := s.buildAccessories(objs)["satel.partition.1"]
	if w == nil {
		t.Fatal("partition not shown")
	}
	if on := findChar(w.Accessory, characteristic.TypeOn); hasPerm(on, characteristic.PermissionWrite) {
		t.Error("a Satel partition must not be switchable from the Home app")
	}
}

// A write is refused when the object became read-only for HomeKit after the
// bridge was built, and the tile goes back to the real state.
func TestWriteRefusedWhenReadonlyNow(t *testing.T) {
	mgr := accesscontrol.NewManager(t.TempDir())
	lua := &recLua{state: "0"}
	s := &Server{luaExecutor: lua, accessManager: mgr}
	w := s.createExposedAccessory(ExposedObject{Path: "t:sw", Type: "switch", Name: "Piec", Value: float64(0)}, false)
	on := findChar(w.Accessory, characteristic.TypeOn)

	mgr.SetAccess(accesscontrol.IntegrationHomeKit, "t:sw", accesscontrol.AccessReadonly)
	on.SetValueRequest(true, homeAppRequest())
	if got := lua.writes(); len(got) != 0 {
		t.Fatalf("readonly object was written: %v", got)
	}
	if on.Value().(bool) {
		t.Error("tile not reverted to the real state")
	}

	mgr.SetAccess(accesscontrol.IntegrationHomeKit, "t:sw", accesscontrol.AccessFull)
	on.SetValueRequest(true, homeAppRequest())
	if got := lua.writes(); len(got) != 1 || !strings.Contains(got[0], `ExposedObjects:get("t:sw")`) {
		t.Fatalf("full access write: %v", got)
	}
}

// Paths are quoted in generated scripts.
func TestPathIsQuotedInScripts(t *testing.T) {
	lua := &recLua{state: "0"}
	s := &Server{luaExecutor: lua}
	path := "x\") os.exit(1) --\n"
	w := s.createExposedAccessory(ExposedObject{Path: path, Type: "switch", Name: "X"}, false)
	findChar(w.Accessory, characteristic.TypeOn).SetValueRequest(true, homeAppRequest())
	for _, sc := range lua.scripts {
		if strings.Contains(sc, `x") os.exit`) || strings.Contains(sc, "--\n") {
			t.Fatalf("path not quoted: %s", sc)
		}
	}
}

// stop must not hold s.mu while waiting for goroutines that need it (the Lua
// loop calls NotifyStateChange, which takes s.mu).
func TestStopDoesNotDeadlock(t *testing.T) {
	s := &Server{accessories: map[string]*AccessoryWrapper{}}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-ctx.Done()
		s.NotifyStateChange("p", 1, "switch") // takes s.mu.RLock
	}()
	done := make(chan struct{})
	go func() { s.stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop deadlocked")
	}
}

// Equal exposed sets need no rebuild; an access change does.
func TestSignatureFollowsAccess(t *testing.T) {
	mgr := accesscontrol.NewManager(t.TempDir())
	s := &Server{accessManager: mgr}
	objs := []ExposedObject{{Path: "a", Type: "switch", Name: "A"}, {Path: "b", Type: "switch", Name: "B"}}
	sig := s.signatureOf(objs)
	if s.signatureOf([]ExposedObject{objs[1], objs[0]}) != sig {
		t.Error("order changed the signature")
	}
	mgr.SetAccess(accesscontrol.IntegrationHomeKit, "a", accesscontrol.AccessHidden)
	if s.signatureOf(objs) == sig {
		t.Error("hiding an object did not change the signature")
	}
}

// homeAppRequest marks a write as coming from a controller (hap calls the
// remote-update callbacks only for those).
func homeAppRequest() *http.Request {
	return httptest.NewRequest(http.MethodPut, "/characteristics", nil)
}

// After a reload, objects missing from the list (a module that failed to load,
// a read in the middle of the reload) stay in the bridge.
func TestSyncKeepsMissingObjects(t *testing.T) {
	prev := []ExposedObject{{Path: "a", Type: "switch", Name: "A"}, {Path: "b", Type: "switch", Name: "B"}}
	got := mergeExposed([]ExposedObject{{Path: "b", Type: "switch", Name: "B2"}, {Path: "c", Type: "switch", Name: "C"}}, prev)
	paths := map[string]string{}
	for _, o := range got {
		paths[o.Path] = o.Name
	}
	if len(got) != 3 || paths["a"] != "A" || paths["b"] != "B2" || paths["c"] != "C" {
		t.Fatalf("merge: %v", paths)
	}
	if len(mergeExposed(nil, prev)) != 2 {
		t.Fatal("an empty read emptied the bridge")
	}

	// a hidden object does leave
	mgr := accesscontrol.NewManager(t.TempDir())
	mgr.SetAccess(accesscontrol.IntegrationHomeKit, "a", accesscontrol.AccessHidden)
	s := &Server{accessManager: mgr}
	if _, ok := s.buildAccessories(got)["a"]; ok {
		t.Error("hidden object kept")
	}
}

// Sync never starts a stopped bridge (Start does, after the first load).
func TestSyncDoesNotStartStoppedBridge(t *testing.T) {
	lua := &recLua{}
	s := &Server{luaExecutor: lua}
	if err := s.Sync(); err != nil || len(lua.scripts) != 0 || s.hapServer != nil {
		t.Fatalf("Sync on a stopped bridge: err=%v scripts=%v", err, lua.scripts)
	}
}

func TestBrokenAIDFileDoesNotReuseIDs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "homekit-aids.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := loadAIDStore(dir)
	if id := st.id("x"); id < recoveredAID {
		t.Errorf("after a broken file new ids start at %d, want >= %d", id, recoveredAID)
	}
	if bad, _ := filepath.Glob(filepath.Join(dir, "homekit-aids.json.bad*")); len(bad) != 1 {
		t.Errorf("broken file not kept aside: %v", bad)
	}
	first := st.id("y")
	if err := st.save(); err != nil {
		t.Fatal(err)
	}

	// broken again: ids given after the first recovery are not reused
	if err := os.WriteFile(filepath.Join(dir, "homekit-aids.json"), []byte(fmt.Sprintf(`{"next":%d,"ids":{"x":`, first+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // .bad names carry seconds
	st2 := loadAIDStore(dir)
	if id := st2.id("z"); id <= first {
		t.Errorf("second recovery gave %d, ids up to %d were already used", id, first)
	}
	if bad, _ := filepath.Glob(filepath.Join(dir, "homekit-aids.json.bad*")); len(bad) != 2 {
		t.Errorf("earlier broken file overwritten: %v", bad)
	}
}

// Over the limit the new objects are left out, not those with an id.
func TestLimitKeepsKnownAccessories(t *testing.T) {
	old := maxBridgedAccessories
	maxBridgedAccessories = 2
	defer func() { maxBridgedAccessories = old }()
	s := &Server{}
	s.aidStore().id("known1")
	s.aidStore().id("known2")
	got := s.buildAccessories([]ExposedObject{
		{Path: "new", Type: "switch", Name: "N"},
		{Path: "known2", Type: "switch", Name: "K2"},
		{Path: "known1", Type: "switch", Name: "K1"},
	})
	if _, ok := got["new"]; ok || len(got) != 2 {
		t.Fatalf("kept %v", got)
	}
}

// A refused fan write leaves speed and power as they were.
func TestFanRefusedWriteKeepsState(t *testing.T) {
	mgr := accesscontrol.NewManager(t.TempDir())
	lua := &recLua{state: "2"}
	s := &Server{luaExecutor: lua, accessManager: mgr}
	w := s.createExposedAccessory(ExposedObject{Path: "f", Type: "fan", Name: "Rekuperator", Value: float64(2)}, false)
	speed := findChar(w.Accessory, characteristic.TypeRotationSpeed)
	mgr.SetAccess(accesscontrol.IntegrationHomeKit, "f", accesscontrol.AccessReadonly)
	speed.SetValueRequest(float64(100), homeAppRequest())
	if v := speed.Value().(float64); v != 50 {
		t.Errorf("speed %v after a refused write, want 50 (level 2)", v)
	}
	if len(lua.writes()) != 0 {
		t.Errorf("refused fan write executed: %v", lua.writes())
	}
}

func TestReadonlySceneCannotRun(t *testing.T) {
	s := &Server{}
	w := s.createExposedAccessory(ExposedObject{Path: "s", Type: "scene", Name: "Film"}, true)
	if hasPerm(findChar(w.Accessory, characteristic.TypeOn), characteristic.PermissionWrite) {
		t.Error("readonly scene can be run from the Home app")
	}
}

// A newer pause is not ended by an older reload's resume; a pause nobody ends
// expires.
func TestInitializingPauses(t *testing.T) {
	old := initializingMax
	initializingMax = 50 * time.Millisecond
	defer func() { initializingMax = old }()
	s := &Server{}
	s.SetInitializing(true)
	s.ResumeAfter(20 * time.Millisecond) // first reload
	s.SetInitializing(true)              // second reload begins
	time.Sleep(30 * time.Millisecond)
	if !s.IsInitializing() {
		t.Fatal("first reload ended the second reload's pause")
	}
	time.Sleep(60 * time.Millisecond)
	if s.IsInitializing() {
		t.Fatal("pause never ended")
	}
}

// The reload hook takes its pause before Sync, which can wait: a later reload
// starting meanwhile keeps its own pause.
func TestResumeOfOlderReloadKeepsNewerPause(t *testing.T) {
	s := &Server{}
	s.SetInitializing(true) // reload 1
	gen1 := s.InitGen()
	s.SetInitializing(true) // reload 2 begins while reload 1's Sync runs
	s.ResumeGen(gen1, 0)
	time.Sleep(20 * time.Millisecond)
	if !s.IsInitializing() {
		t.Fatal("reload 1 ended reload 2's pause")
	}
}

// A write during a pause is not lost silently: the tile goes back.
func TestWriteDuringPauseReverts(t *testing.T) {
	lua := &recLua{state: "0"}
	s := &Server{luaExecutor: lua}
	w := s.createExposedAccessory(ExposedObject{Path: "p", Type: "switch", Name: "P", Value: float64(0)}, false)
	on := findChar(w.Accessory, characteristic.TypeOn)
	s.SetInitializing(true)
	on.SetValueRequest(true, homeAppRequest())
	time.Sleep(50 * time.Millisecond)
	if on.Value().(bool) || len(lua.writes()) != 0 {
		t.Fatalf("paused write: value %v, writes %v", on.Value(), lua.writes())
	}
}

// A tile whose object is gone (kept after a reload) does not pretend success.
func TestWriteToMissingObjectReverts(t *testing.T) {
	lua := &missingLua{}
	s := &Server{luaExecutor: lua}
	w := s.createExposedAccessory(ExposedObject{Path: "gone", Type: "switch", Name: "G", Value: float64(0)}, false)
	on := findChar(w.Accessory, characteristic.TypeOn)
	on.SetValueRequest(true, homeAppRequest())
	if on.Value().(bool) {
		t.Fatal("tile shows ON for an object that is no longer exposed")
	}
}

type missingLua struct{}

func (missingLua) Execute(script string) (string, error) {
	if isStateRead(script) {
		return "0", nil
	}
	return "missing", nil
}

// Paths carry long numbers (CLU212345678.DOU3988); recovery must not take them for ids.
func TestRecoveryIgnoresDigitsInPaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "homekit-aids.json"), []byte(`{"next":40,"ids":{"CLU212345678.DOU3988":12,`), 0o600); err != nil {
		t.Fatal(err)
	}
	if id := loadAIDStore(dir).id("x"); id != recoveredAID {
		t.Errorf("numbering from %d, want %d", id, recoveredAID)
	}
}

// isStateRead tells GetCurrentState's script from a write.
func isStateRead(script string) bool { return strings.Contains(script, "return tostring(v)") }
