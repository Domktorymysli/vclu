package lua

import (
	"strings"
	"testing"
)

// Media player tile (#95): a MusicCast device or a table of functions, its state in the values,
// each control calling its function.

const dashPlayerSetup = `
	_dashboards = {}
	P = { calls = {} }
	local function log(s) P.calls[#P.calls + 1] = s end

	-- like @vclu/musiccast: methods copied onto the instance, called with ':'
	local M = {}
	function M:getState()
		return { id = 'salon', name = 'Salon', ip = '192.0.2.10', zone = 'main', power = self._power,
			volume = self._volume, mute = self._mute, input = 'spotify', playback = self._playback,
			track = self._track, online = self._online, lastError = nil }
	end
	function M:getTrack() return self._track end
	function M:isOn() return self._power end
	function M:play(cb) log('mc:play') end
	function M:pause(cb) log('mc:pause') end
	function M:stop(cb) log('mc:stop') end
	function M:next(cb) log('mc:next') end
	function M:previous(cb) log('mc:previous') end
	function M:setVolume(p, cb) log('mc:setVolume:' .. tostring(p)); self._volume = p end
	function M:setMute(b, cb) log('mc:setMute:' .. tostring(b)) end
	function M:on(cb) log('mc:on') end
	function M:off(cb) log('mc:off') end
	MC = { _power = true, _volume = 35, _mute = false, _playback = 'play', _online = true,
		_track = { artist = 'Artist', title = 'Song', album = 'Album', input = 'spotify',
			artwork = 'http://192.0.2.10/YamahaRemoteControl/AlbumART/AlbumART.ymf?t=1' } }
	for k, f in pairs(M) do MC[k] = f end

	-- an adapter with no next, no mute and no power
	S = { playing = false, title = 'Radio', volume = 70 }
	Dashboard.new('pl')
		:section('Music', 'music-note')
			:player('mc', 'Salon', MC)
			:player('ad', 'Kitchen', {
				getState = function()
					if S.fail then error('player gone') end
					return S
				end,
				play = function() log('ad:play') end,
				pause = function() log('ad:pause') end,
				previous = function() log('ad:previous') end,
				setVolume = function(p) log('ad:setVolume:' .. tostring(p)) end,
			}, { icon = 'speaker', span = 3, disabled = function() return S.off == true end })
		:build()
`

func newPlayerEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashPlayerSetup)
	return engine
}

func playerAction(t *testing.T, engine *LuaEngine, widget, action, value string) map[string]interface{} {
	t.Helper()
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("pl", "`+widget+`", "`+action+`", `+value+`)`, &res)
	return res
}

func playerCalls(t *testing.T, engine *LuaEngine) string {
	t.Helper()
	out := mustExec(t, engine, `local s = table.concat(P.calls, ','); P.calls = {}; return s`)
	return out
}

func TestDashboardPlayerDefinition(t *testing.T) {
	engine := newPlayerEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("pl")`, &def)

	mc := def.widget("mc")
	if mc["type"] != "player" || mc["icon"] != "music-note-beamed" || mc["label"] != "Salon" {
		t.Errorf("mc = %v", mc)
	}
	want := map[string]interface{}{"playing": true, "title": "Song", "artist": "Artist", "album": "Album",
		"artwork": "http://192.0.2.10/YamahaRemoteControl/AlbumART/AlbumART.ymf?t=1",
		"volume":  float64(35), "muted": false, "power": true, "online": true}
	for k, v := range want {
		if mc[k] != v {
			t.Errorf("mc[%s] = %v, want %v", k, mc[k], v)
		}
	}
	// the device's other fields stay on the server
	for _, k := range []string{"ip", "input", "zone", "playback", "track", "lastError"} {
		if _, ok := mc[k]; ok {
			t.Errorf("mc has %s: %v", k, mc[k])
		}
	}
	caps, _ := mc["capabilities"].(map[string]interface{})
	for _, k := range []string{"play", "pause", "next", "previous", "volume", "mute", "power"} {
		if caps[k] != true {
			t.Errorf("mc capability %s = %v", k, caps[k])
		}
	}

	ad := def.widget("ad")
	if ad["icon"] != "speaker" || ad["span"] != float64(3) || ad["playing"] != false || ad["title"] != "Radio" {
		t.Errorf("ad = %v", ad)
	}
	caps, _ = ad["capabilities"].(map[string]interface{})
	wantCaps := map[string]bool{"play": true, "pause": true, "previous": true, "volume": true, "next": false, "mute": false, "power": false}
	for k, v := range wantCaps {
		if caps[k] != v {
			t.Errorf("ad capability %s = %v, want %v", k, caps[k], v)
		}
	}
}

func TestDashboardPlayerValuesFollowState(t *testing.T) {
	engine := newPlayerEngine(t)
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("pl")`, &vals)
	if v := vals["mc"]; v["playing"] != true || v["title"] != "Song" || v["volume"] != float64(35) || v["disabled"] != false {
		t.Errorf("mc values = %v", v)
	}
	if v := vals["ad"]; len(v) != 4 || v["playing"] != false || v["title"] != "Radio" || v["volume"] != float64(70) {
		t.Errorf("ad values = %v", v)
	}

	mustExec(t, engine, `MC._playback = 'pause'; MC._mute = true; MC._volume = 140; MC._track = nil
		S.playing = true; S.artist = 'Host'; S.volume = 'loud'; S.power = false; S.online = 'yes'`)
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("pl")`, &vals)
	if v := vals["mc"]; v["playing"] != false || v["muted"] != true || v["volume"] != float64(100) || v["title"] != nil {
		t.Errorf("mc values = %v", v)
	}
	if v := vals["ad"]; v["playing"] != true || v["artist"] != "Host" || v["volume"] != nil || v["power"] != false || v["online"] != nil {
		t.Errorf("ad values = %v", v)
	}

	// a long text is cut, never mid-character
	mustExec(t, engine, `S.title = string.rep('ż', 200)`)
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("pl")`, &vals)
	if title, _ := vals["ad"]["title"].(string); title != strings.Repeat("ż", 150) {
		t.Errorf("long title = %q", title)
	}

	// offline and switched off as the device reports it
	mustExec(t, engine, `MC._online = false; MC._power = false`)
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("pl")`, &vals)
	if v := vals["mc"]; v["online"] != false || v["power"] != false {
		t.Errorf("mc values = %v", v)
	}
}

// Only an absolute http(s) URL reaches the page; it goes into an <img> src.
func TestDashboardPlayerArtworkFiltered(t *testing.T) {
	engine := newPlayerEngine(t)
	cases := map[string]interface{}{
		`'https://example.com/a.jpg'`:           "https://example.com/a.jpg",
		`'HTTP://192.0.2.10/art.jpg?x=1'`:       "HTTP://192.0.2.10/art.jpg?x=1",
		`'javascript:alert(1)'`:                 nil,
		`'data:image/png;base64,AAAA'`:          nil,
		`'/YamahaRemoteControl/AlbumART.ymf'`:   nil,
		`'//evil.example/a.jpg'`:                nil,
		`'http://'`:                             nil,
		`'http://x/a b.jpg'`:                    nil,
		`'http://x/a.jpg" onerror="alert(1)'`:   nil,
		`'https://x/' .. string.rep('a', 3000)`: nil,
		`42`:                                    nil,
		`{ 'http://x/a.jpg' }`:                  nil,
	}
	for lua, want := range cases {
		mustExec(t, engine, `S.artwork = `+lua)
		var vals map[string]map[string]interface{}
		dashJSON(t, engine, `Dashboard.API.getValues("pl")`, &vals)
		if got := vals["ad"]["artwork"]; got != want {
			t.Errorf("artwork %s = %v, want %v", lua, got, want)
		}
	}
}

func TestDashboardPlayerActions(t *testing.T) {
	engine := newPlayerEngine(t)
	cases := []struct{ widget, action, value, call string }{
		{"mc", "play", "nil", "mc:play"},
		{"mc", "pause", "nil", "mc:pause"},
		{"mc", "next", "nil", "mc:next"},
		{"mc", "previous", "nil", "mc:previous"},
		{"mc", "volume", "42", "mc:setVolume:42"},
		{"mc", "volume", "0", "mc:setVolume:0"},
		{"mc", "volume", `"100"`, "mc:setVolume:100"},
		{"mc", "mute", "true", "mc:setMute:true"},
		{"mc", "mute", "false", "mc:setMute:false"},
		{"mc", "power", "false", "mc:off"},
		{"mc", "power", "true", "mc:on"},
		{"ad", "play", "nil", "ad:play"},
		{"ad", "pause", "nil", "ad:pause"},
		{"ad", "previous", "nil", "ad:previous"},
		{"ad", "volume", "7.5", "ad:setVolume:7.5"},
	}
	for _, c := range cases {
		res := playerAction(t, engine, c.widget, c.action, c.value)
		if res["success"] != true || res["error"] != nil {
			t.Errorf("%s %s(%s) = %v", c.widget, c.action, c.value, res)
		}
		if got := playerCalls(t, engine); got != c.call {
			t.Errorf("%s %s(%s) called %q, want %q", c.widget, c.action, c.value, got, c.call)
		}
	}
	if res := playerAction(t, engine, "mc", "volume", "42"); res["value"] != float64(42) {
		t.Errorf("volume reply = %v", res)
	}
	playerCalls(t, engine)

	// missing functions, bad values, unknown actions: an error, nothing called
	bad := []struct{ widget, action, value, want string }{
		{"ad", "next", "nil", "No next function"},
		{"ad", "mute", "true", "No setMute function"},
		{"ad", "power", "true", "No on function"},
		{"ad", "power", "false", "No off function"},
		{"mc", "volume", "101", "out of range"},
		{"mc", "volume", "-1", "out of range"},
		{"mc", "volume", `"loud"`, "must be a number"},
		{"mc", "volume", "nil", "must be a number"},
		{"mc", "mute", "1", "true or false"},
		{"mc", "power", "nil", "true or false"},
		{"mc", "stop", "nil", "Invalid action"},
		{"mc", "set", "5", "Invalid action"},
	}
	for _, c := range bad {
		res := playerAction(t, engine, c.widget, c.action, c.value)
		if e, _ := res["error"].(string); !strings.Contains(e, c.want) || res["success"] != nil {
			t.Errorf("%s %s(%s) = %v, want error %q", c.widget, c.action, c.value, res, c.want)
		}
	}
	if got := playerCalls(t, engine); got != "" {
		t.Errorf("refused actions called %q", got)
	}

	// a function that fails gives its error; disabled refuses everything
	mustExec(t, engine, `MC.next = function() error('receiver busy') end`)
	if res := playerAction(t, engine, "mc", "next", "nil"); !strings.Contains(res["error"].(string), "receiver busy") {
		t.Errorf("failing next = %v", res)
	}
	mustExec(t, engine, `S.off = true`)
	if res := playerAction(t, engine, "ad", "play", "nil"); res["error"] != "Widget is disabled" {
		t.Errorf("disabled play = %v", res)
	}
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("pl")`, &vals)
	if vals["ad"]["disabled"] != true {
		t.Errorf("ad values = %v", vals["ad"])
	}
	if got := playerCalls(t, engine); got != "" {
		t.Errorf("disabled player called %q", got)
	}
}

// An error in getState shows on the tile (the error mark); the rest of the dashboard keeps working.
func TestDashboardPlayerStateError(t *testing.T) {
	engine := newPlayerEngine(t)
	mustExec(t, engine, `S.fail = true`)
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("pl")`, &vals)
	if e, _ := vals["ad"]["error"].(string); !strings.Contains(e, "player gone") || vals["ad"]["title"] != nil {
		t.Errorf("ad values = %v", vals["ad"])
	}
	if vals["mc"]["title"] != "Song" {
		t.Errorf("mc values = %v", vals["mc"])
	}
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("pl")`, &def)
	if e, _ := def.widget("ad")["error"].(string); !strings.Contains(e, "player gone") {
		t.Errorf("ad definition = %v", def.widget("ad"))
	}

	mustExec(t, engine, `S.fail = nil; _dashboards.pl._sections[1].widgets[2].fns.getState = function() return 'playing' end`)
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("pl")`, &vals)
	if e, _ := vals["ad"]["error"].(string); !strings.Contains(e, `section:player("ad")`) || !strings.Contains(e, "string") {
		t.Errorf("ad values = %v", vals["ad"])
	}
}

// Wrong arguments fail at the module's line and name section:player.
func TestDashboardPlayerArgumentErrors(t *testing.T) {
	cases := []struct{ call, want string }{
		{`:section('S', 'grid'):player('', 'L', { getState = function() end })`, "id must be"},
		{`:section('S', 'grid'):player('a', nil, { getState = function() end })`, "label must be"},
		{`:section('S', 'grid'):player('a', 'L', nil)`, "src must be"},
		{`:section('S', 'grid'):player('a', 'L', 'CLU1.MC')`, "src must be"},
		{`:section('S', 'grid'):player('a', 'L', {})`, "getState must be a function"},
		{`:section('S', 'grid'):player('a', 'L', { getState = function() end, plai = function() end })`, `unknown key "plai"`},
		{`:section('S', 'grid'):player('a', 'L', { getState = function() end, play = true })`, "src.play must be a function"},
		{`:section('S', 'grid'):player('a', 'L', { getState = function() end }, 'x')`, "opts must be a table"},
		{`:section('S', 'grid'):player('a', 'L', { getState = function() end }, { colour = 1 })`, `unknown option "colour"`},
		{`:section('S', 'grid'):player('a', 'L', { getState = function() end }, { icon = 'x y' })`, "opts.icon"},
		{`:section('S', 'grid'):player('a', 'L', { getState = function() end }, { disabled = true })`, "opts.disabled"},
		{`:header():player('a', 'L', { getState = function() end })`, "not the header"},
	}
	for _, c := range cases {
		engine := NewLuaEngine()
		_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  Dashboard.new('e')" + c.call + "\nend)\nerror(err, 0)")
		engine.Close()
		if err == nil {
			t.Errorf("%s: no error", c.call)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, "section:player") || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %s", c.call, msg)
		}
	}
	// the builder's own method too
	engine := NewLuaEngine()
	defer engine.Close()
	_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  local b = Dashboard.new('f'); b:section('S', 'g')\n  b:player('a', 'L', {})\nend)\nerror(err, 0)")
	if err == nil || !strings.Contains(err.Error(), "<string>:4: ") || !strings.Contains(err.Error(), "section:player") {
		t.Errorf("builder player: %v", err)
	}
}
