package lua

import (
	"encoding/json"
	"testing"
)

// What the HomeKit bridge reads of an RGB light (#60): the exposed list says it has a
// colour and gives it; a colour report alone reaches __go_homekit_notify.

const homekitColorSetup = `
	_hk = {}
	__go_homekit_notify = function(path, value, typ) table.insert(_hk, path .. "=" .. tostring(value)) end
	local clu = { executeNoReply = function() end }
	_rgb = RemoteRGBLight:new(clu, "LED1", "salon")
	_rgbw = RemoteRGBLight:new(clu, "LED2", "kuchnia")
	_rgbw._hasWhite = true
	expose(_rgb, "light", { name = "Salon", path = "t:salon" })
	expose(_rgbw, "light", { name = "Kuchnia", path = "t:kuchnia" })
	expose(RemoteLight:new(clu, "DIM1", "hol"), "light", { name = "Hol", path = "t:hol" })
	expose(RemoteSwitch:new(clu, "DOU1", "kanapa"), "light", { name = "Kanapa", path = "t:kanapa" })
	expose(RemoteLedChannel:new(clu, "LED3", "w"), "light", { name = "Biały", path = "t:w" })
	expose(VirtualDimmer:new("kinkiet"), "dimmer", { name = "Kinkiet", path = "t:kinkiet" })
	_hidden = RemoteRGBLight:new(clu, "LED4", "strych")
	expose(_hidden, "light", { name = "Strych", path = "t:strych", homekit = false })
`

type homekitColorObj struct {
	Path     string `json:"path"`
	HasColor bool   `json:"hasColor"`
	Color    *struct {
		R, G, B int
	} `json:"color"`
}

func homekitColorList(t *testing.T, engine *LuaEngine) map[string]homekitColorObj {
	t.Helper()
	var objs []homekitColorObj
	if err := json.Unmarshal([]byte(mustExec(t, engine, `ExposedObjects:toJSON({ homekit = true })`)), &objs); err != nil {
		t.Fatal(err)
	}
	out := map[string]homekitColorObj{}
	for _, o := range objs {
		out[o.Path] = o
	}
	return out
}

func TestHomeKitExposedListHasColor(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, homekitColorSetup)

	objs := homekitColorList(t, engine)
	if o := objs["t:salon"]; !o.HasColor || o.Color != nil {
		t.Errorf("RGB light before a report: hasColor %v color %+v, want hasColor and no colour", o.HasColor, o.Color)
	}
	for _, p := range []string{"t:hol", "t:kanapa", "t:w", "t:kinkiet"} {
		if objs[p].HasColor || objs[p].Color != nil {
			t.Errorf("%s has a colour in the exposed list", p)
		}
	}

	mustExec(t, engine, `(function()
		_rgb:updateState(_rgb:fromFeature(0, 0.5))
		_rgb:fromFeature(6, "#7f2a00")
	end)()`)
	if c := homekitColorList(t, engine)["t:salon"].Color; c == nil || c.R != 254 || c.G != 84 || c.B != 0 {
		t.Errorf("colour after a report at 0.5 = %+v, want the light's colour 254,84,0", c)
	}
}

func TestHomeKitNotifiedOfColourAlone(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, homekitColorSetup)
	mustExec(t, engine, `(function()
		_rgb:updateState(_rgb:fromFeature(0, 1))
		_rgbw:updateState(_rgbw:fromFeature(0, 1))
		_hidden:updateState(_hidden:fromFeature(0, 1))
		_hk = {}
	end)()`)

	got := mustExec(t, engine, `(function()
		_rgb:fromFeature(6, "#00ff00")
		_rgbw:fromFeature(15, 200)
		_hidden:fromFeature(6, "#0000ff")
		return table.concat(_hk, ";")
	end)()`)
	// the white of an RGBW module changes no colour HomeKit shows, but one more read is harmless
	if want := "t:salon=1;t:kuchnia=1"; got != want {
		t.Errorf("notified %q, want %q (no value change, and nothing for a light kept from HomeKit)", got, want)
	}

	if got := mustExec(t, engine, `(function()
		_hk = {}
		_rgb:fromFeature(6, "#00ff00")
		return tostring(#_hk)
	end)()`); got != "0" {
		t.Errorf("the same colour again notified %s times", got)
	}
}
