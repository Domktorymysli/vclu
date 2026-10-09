package homekit

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/brutella/hap/characteristic"
)

// A DOUT exposed as "light" is switched with 1/0 (it used to get 100).
func TestOnOffLightWritesOne(t *testing.T) {
	rec := &recLua{state: "0"}
	s := &Server{luaExecutor: rec}
	w := s.createExposedAccessory(ExposedObject{Path: "CLU1.DOU1", Type: "light", Name: "Kanapa", Value: float64(0)}, false)
	if findChar(w.Accessory, characteristic.TypeBrightness) != nil {
		t.Error("on/off light got a brightness slider")
	}
	findChar(w.Accessory, characteristic.TypeOn).SetValueRequest(true, homeAppRequest())
	got := rec.writes()
	if len(got) != 1 || !strings.Contains(got[0], "h:setValue(1)") {
		t.Fatalf("ON wrote %v, want setValue(1)", got)
	}
}

// Dimmers, on the real runtime: value 0..1 shown as 0..100 %, the slider and
// ON/OFF use the dimmer's own methods (ON keeps the last brightness).
func TestDimmerOnRealLua(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		dim = VirtualDimmer:new("dim")
		expose(dim, "dimmer", { name = "Kinkiet", path = "hk:dim" })
	`); err != nil {
		t.Fatal(err)
	}
	s := &Server{luaExecutor: engine}
	objs, err := s.fetchExposed()
	if err != nil || len(objs) != 1 || !objs[0].Dimmable {
		t.Fatalf("dimmer not marked dimmable: %+v %v", objs, err)
	}
	w := s.createExposedAccessory(objs[0], false)
	on := findChar(w.Accessory, characteristic.TypeOn)
	bri := findChar(w.Accessory, characteristic.TypeBrightness)
	if bri == nil {
		t.Fatal("dimmer has no brightness")
	}

	bri.SetValueRequest(40, homeAppRequest())
	if v, _ := engine.Execute(`return tostring(dim:getValue())`); v != "0.4" {
		t.Fatalf("slider 40%% gave value %s", v)
	}
	on.SetValueRequest(false, homeAppRequest())
	if v, _ := engine.Execute(`return tostring(dim:getValue())`); v != "0" {
		t.Fatalf("OFF gave %s", v)
	}
	on.SetValueRequest(true, homeAppRequest())
	if v, _ := engine.Execute(`return tostring(dim:getValue())`); v != "0.4" {
		t.Fatalf("ON did not restore the last brightness: %s", v)
	}

	w.UpdateFunc(0.73)
	if bri.Value().(int) != 73 || !on.Value().(bool) {
		t.Errorf("value 0.73 shown as %v %%, on=%v", bri.Value(), on.Value())
	}
}

// Covers: vCLU 0 = open, HomeKit 100 = open; the position, not the value, is shown.
func TestCoverOnRealLua(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		cov = VirtualCover:new("cov")
		cov:setPosition(30)
		expose(cov, "cover", { name = "Roleta", path = "hk:cov" })
	`); err != nil {
		t.Fatal(err)
	}
	s := &Server{luaExecutor: engine}
	objs, err := s.fetchExposed()
	if err != nil || objs[0].Position == nil || *objs[0].Position != 30 {
		t.Fatalf("cover position not exported: %+v %v", objs, err)
	}
	w := s.createExposedAccessory(objs[0], false)
	if !w.Refetch {
		t.Error("cover must re-read its position on state events")
	}
	cur := findChar(w.Accessory, characteristic.TypeCurrentPosition)
	if cur.Value().(int) != 70 {
		t.Fatalf("vCLU 30 (mostly open) shown as %v, want 70", cur.Value())
	}
	findChar(w.Accessory, characteristic.TypeTargetPosition).SetValueRequest(100, homeAppRequest())
	if v, _ := engine.Execute(`return tostring(cov:getPosition())`); v != "0" {
		t.Fatalf("'open fully' gave vCLU position %s, want 0", v)
	}
	if v, ok := s.GetCurrentState("hk:cov"); !ok || v != 0 {
		t.Errorf("state read %v %v, want the position", v, ok)
	}
}

// The Lua loop only queues; a slow Home app connection does not block it,
// and only the latest value per object is sent.
func TestNotifyDoesNotBlockLuaLoop(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var got []float64
	s := &Server{accessories: map[string]*AccessoryWrapper{
		"p": {UpdateFunc: func(v float64) {
			<-release
			mu.Lock()
			got = append(got, v)
			mu.Unlock()
		}},
	}}
	wake := make(chan struct{}, 1)
	s.wake = wake
	done := make(chan struct{})
	go func() { s.runNotifier(t.Context(), wake); close(done) }()

	start := time.Now()
	s.NotifyStateChange("p", 1, "switch") // worker blocks in UpdateFunc
	time.Sleep(20 * time.Millisecond)
	for i := 2; i <= 50; i++ {
		s.NotifyStateChange("p", float64(i), "switch")
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("NotifyStateChange blocked for %v", d)
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) < 1 || got[len(got)-1] != 50 || len(got) > 3 {
		t.Errorf("delivered %v, want the first and then only the latest (50)", got)
	}
}

func TestValidatePin(t *testing.T) {
	for pin, ok := range map[string]bool{
		"00102003": true, "73519284": true,
		"1234567": false, "123456789": false, "1234abcd": false, "12345678": false, "00000000": false,
	} {
		if err := ValidatePin(pin); (err == nil) != ok {
			t.Errorf("ValidatePin(%q) = %v", pin, err)
		}
	}
}

// An object that has not reported yet is not shown as off.
func TestUnknownStateIsNotZero(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		local o = VirtualObject:new("x")
		o._state = nil
		expose(o, "switch", { path = "hk:unknown" })
	`); err != nil {
		t.Fatal(err)
	}
	s := &Server{luaExecutor: engine}
	if v, ok := s.GetCurrentState("hk:unknown"); ok {
		t.Errorf("unknown state read as %v", v)
	}
}

// Older configs have no PIN or name: the bridge still starts, with hap's defaults.
func TestEmptyPinAndNameGetDefaults(t *testing.T) {
	pin, name := bridgeIdentity(&config.VCLUHomeKitConfig{})
	if pin != "00102003" || name != "vCLU Bridge" || ValidatePin(pin) != nil {
		t.Errorf("defaults: %q %q", pin, name)
	}
}

// A Grenton roller has not reported a position: its movement value must not
// be shown as one.
func TestCoverWithoutPositionIgnoresValue(t *testing.T) {
	s := &Server{}
	w := s.createExposedAccessory(ExposedObject{Path: "r", Type: "cover", Name: "R", Value: float64(2), HasPos: true}, false)
	if v := findChar(w.Accessory, characteristic.TypeCurrentPosition).Value().(int); v == 98 {
		t.Error("movement state 2 shown as position 98")
	}
}

// Changes queued while no bridge ran are delivered when it starts.
func TestQueuedChangesDeliveredOnStart(t *testing.T) {
	got := make(chan float64, 1)
	s := &Server{accessories: map[string]*AccessoryWrapper{"p": {UpdateFunc: func(v float64) { got <- v }}}}
	s.NotifyStateChange("p", 7, "switch") // no notifier yet
	wake := make(chan struct{}, 1)
	wake <- struct{}{} // what start() does
	go s.runNotifier(t.Context(), wake)
	select {
	case v := <-got:
		if v != 7 {
			t.Errorf("delivered %v", v)
		}
	case <-time.After(time.Second):
		t.Fatal("queued change not delivered")
	}
}

// A Grenton roller without a reported position shows the target after a
// write instead of "opening..." forever.
func TestCoverWithoutPositionShowsTarget(t *testing.T) {
	s := &Server{luaExecutor: &missingPosLua{}}
	w := s.createExposedAccessory(ExposedObject{Path: "r", Type: "cover", Name: "R", HasPos: true}, false)
	findChar(w.Accessory, characteristic.TypeTargetPosition).SetValueRequest(60, homeAppRequest())
	if v := findChar(w.Accessory, characteristic.TypeCurrentPosition).Value().(int); v != 60 {
		t.Errorf("current position %d after a write, want the target 60", v)
	}
}

// missingPosLua: writes succeed, the position stays unknown.
type missingPosLua struct{}

func (missingPosLua) Execute(script string) (string, error) {
	if isStateRead(script) {
		return "nil", nil
	}
	return "ok", nil
}

func TestInvalidPinIsNotRetried(t *testing.T) {
	if err := ValidatePin("12345678"); !errors.Is(err, ErrInvalidPin) {
		t.Fatalf("%v", err)
	}
}

type jsonLua struct{ out string }

func (j *jsonLua) Execute(string) (string, error) { return j.out, nil }

// Satel names come from the panel after the bridge started: a changed name
// must rebuild the bridge, an unchanged set must not.
func TestNameChangeRebuildsBridge(t *testing.T) {
	lua := &jsonLua{out: `[{"path":"satel.zone.1","type":"sensor","name":"satel.zone.1","satel":true}]`}
	s := &Server{luaExecutor: lua}
	objs, err := s.fetchExposed()
	if err != nil {
		t.Fatal(err)
	}
	s.built = objs
	s.signature = s.signatureOf(objs)
	if s.exposedChanged() {
		t.Fatal("unchanged set reported as changed")
	}
	lua.out = `[{"path":"satel.zone.1","type":"sensor","name":"PIR HOL","satel":true}]`
	if !s.exposedChanged() {
		t.Fatal("a new name did not trigger a rebuild")
	}
	lua.out = `[]` // a read mid-reload keeps the built set
	if s.exposedChanged() {
		t.Fatal("an empty read triggered a rebuild")
	}
}
