package homekit

import (
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
)

// ExposedColor is the colour of a colour light, brightest channel 255 (RemoteRGBLight:getColor).
type ExposedColor struct {
	R int `json:"r"`
	G int `json:"g"`
	B int `json:"b"`
}

// colorWriteDelay: the Home app sends Hue and Saturation as two writes; both reach the
// light in one setColor this long after the first.
var colorWriteDelay = 100 * time.Millisecond

// afterColorWrite runs f once colorWriteDelay has passed (tests run it themselves).
var afterColorWrite = func(f func()) { time.AfterFunc(colorWriteDelay, f) }

// rgbToHS gives the hue (0..360) and saturation (0..100) of a colour; its brightness is
// left out. false for black, which has neither.
func rgbToHS(r, g, b int) (h, s float64, ok bool) {
	rf, gf, bf := float64(r), float64(g), float64(b)
	maxC := math.Max(rf, math.Max(gf, bf))
	minC := math.Min(rf, math.Min(gf, bf))
	if maxC <= 0 {
		return 0, 0, false
	}
	d := maxC - minC
	switch {
	case d == 0:
		h = 0
	case maxC == rf:
		h = 60 * math.Mod((gf-bf)/d+6, 6)
	case maxC == gf:
		h = 60 * ((bf-rf)/d + 2)
	default:
		h = 60 * ((rf-gf)/d + 4)
	}
	return h, 100 * d / maxC, true
}

// hsToRGB gives the colour of a hue and saturation at full V: the brightest channel is 255.
func hsToRGB(h, s float64) (r, g, b int) {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	c := math.Max(0, math.Min(100, s)) / 100
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := 1 - c
	var rf, gf, bf float64
	switch {
	case h < 60:
		rf, gf = c, x
	case h < 120:
		rf, gf = x, c
	case h < 180:
		gf, bf = c, x
	case h < 240:
		gf, bf = x, c
	case h < 300:
		rf, bf = x, c
	default:
		rf, bf = c, x
	}
	ch := func(v float64) int { return int(math.Round((v + m) * 255)) }
	return ch(rf), ch(gf), ch(bf)
}

func near(a, b int) bool { return a-b <= 1 && b-a <= 1 }

// addColor gives a Lightbulb Hue and Saturation, after On and Brightness so their
// instance ids stay. It returns what shows a colour reported by the light.
func (s *Server) addColor(svc *service.Lightbulb, obj ExposedObject, readonly bool) func(r, g, b int) {
	hue := characteristic.NewHue()
	sat := characteristic.NewSaturation()
	svc.AddC(hue.C)
	svc.AddC(sat.C)

	show := func(r, g, b int) {
		h, sv, ok := rgbToHS(r, g, b)
		if !ok {
			return
		}
		// a colour just set from the Home app comes back rounded: keep its values
		if cr, cg, cb := hsToRGB(hue.Value(), sat.Value()); near(cr, r) && near(cg, g) && near(cb, b) {
			return
		}
		if sv > 0 {
			hue.SetValue(h) // white has no hue: the wheel stays where it was
		}
		sat.SetValue(sv)
	}
	if c := obj.Color; c != nil {
		show(c.R, c.G, c.B)
	}
	if readonly {
		hue.Permissions = readonlyPerms
		sat.Permissions = readonlyPerms
		return show
	}

	revert := func(float64) {
		if r, g, b, ok := s.currentColor(obj.Path); ok {
			show(r, g, b)
		}
	}
	var mu sync.Mutex
	pending := false
	write := func(float64) {
		if s.paused(obj.Path, revert) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if pending {
			return // the write already waiting reads both values
		}
		pending = true
		afterColorWrite(func() {
			mu.Lock()
			pending = false
			mu.Unlock()
			r, g, b := hsToRGB(hue.Value(), sat.Value())
			s.command(obj.Path, fmt.Sprintf("hk.setColor(h, %d, %d, %d)", r, g, b), revert)
		})
	}
	hue.OnValueRemoteUpdate(write)
	sat.OnValueRemoteUpdate(write)
	return show
}

// currentColor reads the colour the Home app shows for path; false when unknown.
func (s *Server) currentColor(path string) (r, g, b int, ok bool) {
	res, err := s.luaExecutor.Execute(currentColorLua + "\n" + fmt.Sprintf("return currentColor(%s)", luaString(path)))
	if err != nil {
		log.Printf("[HomeKit] colour of %s not read: %v", path, err)
		return 0, 0, 0, false
	}
	if _, err := fmt.Sscanf(res, "%d,%d,%d", &r, &g, &b); err != nil {
		return 0, 0, 0, false
	}
	return r, g, b, true
}

// showColor shows the current colour on a colour accessory.
func (s *Server) showColor(path string, w *AccessoryWrapper) {
	if w.ColorFunc == nil {
		return
	}
	if r, g, b, ok := s.currentColor(path); ok {
		w.ColorFunc(r, g, b)
	}
}
