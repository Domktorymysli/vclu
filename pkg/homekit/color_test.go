package homekit

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/brutella/hap/characteristic"
)

// A Grenton RGB light in Apple Home (#60): a Lightbulb with Hue and Saturation next to On and
// Brightness; the brightness stays the light's value.

const colorSetup = `
	_sent = {}
	local clu = { executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end }
	rgb = RemoteRGBLight:new(clu, "LED1", "salon")
	rgbw = RemoteRGBLight:new(clu, "LED2", "kuchnia")
	rgbw._hasWhite = true
	expose(rgb, "light", { name = "Salon", path = "hk:salon" })
	expose(rgbw, "light", { name = "Kuchnia", path = "hk:kuchnia" })
	expose(RemoteRGBLight:new(clu, "LED5", "taras"), "light", { name = "Taras", path = "hk:taras", readonly = true })
	expose(RemoteLight:new(clu, "DIM1", "hol"), "light", { name = "Hol", path = "hk:hol" })
	expose(RemoteSwitch:new(clu, "DOU1", "kanapa"), "light", { name = "Kanapa", path = "hk:kanapa" })
	expose(RemoteLedChannel:new(clu, "LED3", "w"), "light", { name = "Biały", path = "hk:w" })
	expose(VirtualDimmer:new("kinkiet"), "dimmer", { name = "Kinkiet", path = "hk:kinkiet" })
`

func newColorBridge(t *testing.T, mgr *accesscontrol.Manager) (*lua.LuaEngine, *Server) {
	t.Helper()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(colorSetup); err != nil {
		t.Fatal(err)
	}
	s := &Server{luaExecutor: engine, accessManager: mgr}
	objs, err := s.fetchExposed()
	if err != nil {
		t.Fatal(err)
	}
	s.accessories = s.buildAccessories(objs)
	return engine, s
}

func lightbulbChars(t *testing.T, w *AccessoryWrapper) []string {
	t.Helper()
	var out []string
	for _, sv := range w.Accessory.Ss {
		if sv.Type != "43" { // Lightbulb
			continue
		}
		for _, c := range sv.Cs {
			out = append(out, c.Type)
		}
	}
	return out
}

func exec(t *testing.T, engine *lua.LuaEngine, script string) string {
	t.Helper()
	v, err := engine.Execute(script)
	if err != nil {
		t.Fatalf("%s: %v", script, err)
	}
	return v
}

// fakeColorTimer replaces the coalescing delay: the test runs the pending write itself.
func fakeColorTimer(t *testing.T) *[]func() {
	t.Helper()
	var due []func()
	old := afterColorWrite
	afterColorWrite = func(f func()) { due = append(due, f) }
	t.Cleanup(func() { afterColorWrite = old })
	return &due
}

func TestColorLightHasHueAndSaturation(t *testing.T) {
	_, s := newColorBridge(t, nil)
	on, bri := characteristic.TypeOn, characteristic.TypeBrightness
	hue, sat := characteristic.TypeHue, characteristic.TypeSaturation
	for path, want := range map[string][]string{
		"hk:salon":   {on, bri, hue, sat},
		"hk:kuchnia": {on, bri, hue, sat},
		"hk:taras":   {on, bri, hue, sat},
		// a dimmer, a DOUT light and an LED channel stay exactly as they were
		"hk:hol":     {on, bri},
		"hk:kanapa":  {on},
		"hk:w":       {on, bri},
		"hk:kinkiet": {on, bri},
	} {
		w := s.accessories[path]
		if w == nil {
			t.Fatalf("%s has no accessory", path)
		}
		if got := lightbulbChars(t, w); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s Lightbulb characteristics %v, want %v", path, got, want)
		}
	}
}

func TestColorReportShowsInHome(t *testing.T) {
	engine, s := newColorBridge(t, nil)
	engine.SetHomeKitStateHandler(s.NotifyStateChange)
	wake := make(chan struct{}, 1)
	s.wake = wake
	go s.runNotifier(t.Context(), wake)

	w := s.accessories["hk:salon"]
	hue := findChar(w.Accessory, characteristic.TypeHue)
	sat := findChar(w.Accessory, characteristic.TypeSaturation)
	bri := findChar(w.Accessory, characteristic.TypeBrightness)
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: hue %v sat %v brightness %v", what, hue.Value(), sat.Value(), bri.Value())
			}
		}
	}

	// value first, then the colour, as the subscription reports them
	exec(t, engine, `(function() rgb:updateState(rgb:fromFeature(0, 0.5)); rgb:fromFeature(6, "#7f0000") end)()`)
	waitFor("red at 50 %", func() bool {
		return sat.Value().(float64) == 100 && hue.Value().(float64) == 0 && bri.Value().(int) == 50
	})

	// a colour alone, the value unchanged
	exec(t, engine, `rgb:fromFeature(6, "#007f00")`)
	waitFor("green from a colour report alone", func() bool { return math.Abs(hue.Value().(float64)-120) < 0.5 })
	if bri.Value().(int) != 50 {
		t.Errorf("a colour report changed the brightness to %v", bri.Value())
	}

	exec(t, engine, `rgb:fromFeature(6, "#7f7f7f")`)
	waitFor("white", func() bool { return sat.Value().(float64) == 0 })
}

// Hue and Saturation written at once from the Home app make one setColor; the brightness,
// On and the white stay.
func TestHueAndSaturationMakeOneSetColor(t *testing.T) {
	due := fakeColorTimer(t)
	engine, s := newColorBridge(t, nil)
	exec(t, engine, `(function()
		rgbw:updateState(rgbw:fromFeature(0, 0.5))
		rgbw:fromFeature(6, "#7f0000")
		rgbw:fromFeature(15, 77)
		_sent = {}
	end)()`)
	w := s.accessories["hk:kuchnia"]
	s.refreshAllStates(t.Context())
	on := findChar(w.Accessory, characteristic.TypeOn)
	bri := findChar(w.Accessory, characteristic.TypeBrightness)

	findChar(w.Accessory, characteristic.TypeHue).SetValueRequest(240.0, homeAppRequest())
	findChar(w.Accessory, characteristic.TypeSaturation).SetValueRequest(50.0, homeAppRequest())
	if len(*due) != 1 {
		t.Fatalf("%d colour writes pending, want 1", len(*due))
	}
	if got := exec(t, engine, `table.concat(_sent, ";")`); got != "" {
		t.Fatalf("sent before the writes were combined: %s", got)
	}
	(*due)[0]()

	// hue 240, saturation 50 at full V is 128,128,255; out at brightness 0.5
	if got, want := exec(t, engine, `table.concat(_sent, ";")`), `LED2:execute(6, "#404080")`; got != want {
		t.Errorf("sent %q, want one setColor %q", got, want)
	}
	if got := exec(t, engine, `(function()
		local r, g, b = rgbw:getColor()
		return r .. "," .. g .. "," .. b .. "|" .. tostring(rgbw:getValue()) .. "|" .. tostring(rgbw:getWhite())
	end)()`); got != "128,128,255|0.5|77" {
		t.Errorf("colour|value|white after the write = %s, want 128,128,255|0.5|77", got)
	}
	if !on.Value().(bool) || bri.Value().(int) != 50 {
		t.Errorf("On %v brightness %v after a colour write, want true 50", on.Value(), bri.Value())
	}
}

// A colour picked for a light that is off does not light it; it waits for the next
// switch-on from the Home app.
func TestColorForLightThatIsOff(t *testing.T) {
	due := fakeColorTimer(t)
	engine, s := newColorBridge(t, nil)
	exec(t, engine, `(function()
		rgb:updateState(rgb:fromFeature(0, 0.5))
		rgb:fromFeature(6, "#7f0000")
		rgb:updateState(rgb:fromFeature(0, 0))
		_sent = {}
	end)()`)
	w := s.accessories["hk:salon"]
	s.refreshAllStates(t.Context())
	hue := findChar(w.Accessory, characteristic.TypeHue)

	hue.SetValueRequest(120.0, homeAppRequest())
	for _, f := range *due {
		f()
	}
	if got := exec(t, engine, `table.concat(_sent, ";")`); got != "" {
		t.Fatalf("a colour for an off light sent %q", got)
	}
	s.refreshAllStates(t.Context())
	if v := hue.Value().(float64); math.Abs(v-120) > 0.5 {
		t.Errorf("the picked colour was not kept: hue %v", v)
	}

	findChar(w.Accessory, characteristic.TypeOn).SetValueRequest(true, homeAppRequest())
	if got, want := exec(t, engine, `table.concat(_sent, ";")`), `LED1:execute(6, "#00ff00");LED1:execute(9, 0)`; got != want {
		t.Errorf("switching on sent %q, want %q", got, want)
	}
}

// A light that is off, lit from the Home app with a brightness: the picked colour goes out
// at that brightness, not at full first.
func TestColorForLightThatIsOffLitWithBrightness(t *testing.T) {
	due := fakeColorTimer(t)
	engine, s := newColorBridge(t, nil)
	exec(t, engine, `(function()
		rgb:updateState(rgb:fromFeature(0, 0.5))
		rgb:fromFeature(6, "#7f0000")
		rgb:updateState(rgb:fromFeature(0, 0))
		_sent = {}
	end)()`)
	w := s.accessories["hk:salon"]
	s.refreshAllStates(t.Context())
	findChar(w.Accessory, characteristic.TypeHue).SetValueRequest(120.0, homeAppRequest())
	for _, f := range *due {
		f()
	}

	findChar(w.Accessory, characteristic.TypeBrightness).SetValueRequest(30, homeAppRequest())
	if got, want := exec(t, engine, `table.concat(_sent, ";")`), `LED1:execute(0, 0.3);LED1:execute(6, "#004d00")`; got != want {
		t.Errorf("lighting at 30 %% sent %q, want %q", got, want)
	}
}

func TestReadonlyColorLightRefusesHueAndSaturation(t *testing.T) {
	due := fakeColorTimer(t)
	mgr := accesscontrol.NewManager(t.TempDir())
	engine, s := newColorBridge(t, mgr)
	exec(t, engine, `_sent = {}`)

	// readonly in expose(): the Home app cannot write
	w := s.accessories["hk:taras"]
	for _, typ := range []string{characteristic.TypeHue, characteristic.TypeSaturation} {
		c := findChar(w.Accessory, typ)
		if hasPerm(c, characteristic.PermissionWrite) {
			t.Errorf("readonly %s is writable", typ)
		}
		if _, code := c.SetValueRequest(200.0, homeAppRequest()); code == 0 {
			t.Errorf("readonly %s accepted a write", typ)
		}
	}

	// readonly for HomeKit after the bridge was built: refused where it executes
	mgr.SetAccess(accesscontrol.IntegrationHomeKit, "hk:salon", accesscontrol.AccessReadonly)
	w = s.accessories["hk:salon"]
	findChar(w.Accessory, characteristic.TypeHue).SetValueRequest(200.0, homeAppRequest())
	findChar(w.Accessory, characteristic.TypeSaturation).SetValueRequest(80.0, homeAppRequest())
	// made readonly in Lua after the bridge was built
	exec(t, engine, `(function() rgbw:updateState(1); ExposedObjects:get("hk:kuchnia"):readonly() end)()`)
	findChar(s.accessories["hk:kuchnia"].Accessory, characteristic.TypeHue).SetValueRequest(200.0, homeAppRequest())
	for _, f := range *due {
		f()
	}
	if got := exec(t, engine, `table.concat(_sent, ";")`); got != "" {
		t.Errorf("a readonly light got %q", got)
	}
}

// Without the fake: the writes reach the light once the delay has passed.
func TestColorWriteIsSentAfterDelay(t *testing.T) {
	engine, s := newColorBridge(t, nil)
	exec(t, engine, `(function() rgb:updateState(rgb:fromFeature(0, 1)); _sent = {} end)()`)
	w := s.accessories["hk:salon"]
	findChar(w.Accessory, characteristic.TypeHue).SetValueRequest(240.0, homeAppRequest())
	findChar(w.Accessory, characteristic.TypeSaturation).SetValueRequest(100.0, homeAppRequest())
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		got := exec(t, engine, `table.concat(_sent, ";")`)
		if got == `LED1:execute(6, "#0000ff")` {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sent %q, want blue", got)
		}
	}
}

// A Home scene writes Brightness, Hue and Saturation at once: the brightness state change
// comes back while the colour waits, and must not put the old colour back before it is sent.
func TestColorWriteSurvivesStateUpdateWhileWaiting(t *testing.T) {
	due := fakeColorTimer(t)
	engine, s := newColorBridge(t, nil)
	exec(t, engine, `(function()
		rgb:updateState(rgb:fromFeature(0, 1))
		rgb:fromFeature(6, "#ff0000")
		_sent = {}
	end)()`)
	w := s.accessories["hk:salon"]
	s.refreshAllStates(t.Context())

	findChar(w.Accessory, characteristic.TypeHue).SetValueRequest(240.0, homeAppRequest())
	findChar(w.Accessory, characteristic.TypeSaturation).SetValueRequest(100.0, homeAppRequest())
	s.deliver("hk:salon", 1)
	s.refreshAllStates(t.Context())
	for _, f := range *due {
		f()
	}
	if got, want := exec(t, engine, `table.concat(_sent, ";")`), `LED1:execute(6, "#0000ff")`; got != want {
		t.Errorf("sent %q, want the picked blue %q", got, want)
	}
}

func TestRGBToHueSaturationRoundTrip(t *testing.T) {
	for _, c := range [][3]int{
		{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, // primaries
		{255, 255, 0}, {0, 255, 255}, {255, 0, 255},
		{255, 255, 255},                                               // white
		{255, 128, 0}, {85, 170, 255}, {200, 40, 255}, {255, 191, 64}, // mixed
	} {
		h, s, ok := rgbToHS(c[0], c[1], c[2])
		if !ok {
			t.Fatalf("%v: no colour", c)
		}
		r, g, b := hsToRGB(h, s)
		if abs(r-c[0]) > 1 || abs(g-c[1]) > 1 || abs(b-c[2]) > 1 {
			t.Errorf("%v -> h %.2f s %.2f -> %d,%d,%d", c, h, s, r, g, b)
		}
	}
	for _, c := range []struct {
		h, s    float64
		r, g, b int
	}{{0, 100, 255, 0, 0}, {120, 100, 0, 255, 0}, {240, 100, 0, 0, 255}, {77, 0, 255, 255, 255}, {360, 100, 255, 0, 0}} {
		if r, g, b := hsToRGB(c.h, c.s); r != c.r || g != c.g || b != c.b {
			t.Errorf("h %v s %v -> %d,%d,%d, want %d,%d,%d", c.h, c.s, r, g, b, c.r, c.g, c.b)
		}
	}
	// a dim colour keeps its hue and saturation; V is the brightness, not the colour
	if h, s, _ := rgbToHS(0, 0, 127); h != 240 || s != 100 {
		t.Errorf("dim blue -> h %v s %v, want 240 100", h, s)
	}
	if _, _, ok := rgbToHS(0, 0, 0); ok {
		t.Error("black has no hue or saturation")
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
