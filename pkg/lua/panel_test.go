package lua

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Harness for the Smart Panel: a virtual clock for RemotePanel and lib/panel, a CLU that
// records every script sent (and can lose the answer to a frame), a panel with 4 buttons.
const panelHarness = `
T = { now = 0, q = {}, seq = 0 }
function T.after(ms, fn) T.seq = T.seq + 1; T.q[T.seq] = { at = T.now + ms, fn = fn }; return T.seq end
function T.cancel(id) if id then T.q[id] = nil end end
function T.advance(ms)
	local target = T.now + ms
	while true do
		local best, bid
		for id, t in pairs(T.q) do
			if t.at <= target and (not best or t.at < best.at or (t.at == best.at and id < bid)) then best, bid = t, id end
		end
		if not best then break end
		T.q[bid] = nil
		T.now = best.at
		best.fn()
	end
	T.now = target
end
RemotePanel.timer = { after = T.after }
Panel = require("lib/panel")
Panel.timer = { after = T.after, cancel = T.cancel, now = function() return T.now end,
	clock = function() return "12:34  16.10" end }
SENT = {}
AT = {}
REPLY = { fail = 0 }
CLU = {
	executeNoReply = function(_, _, s) SENT[#SENT + 1] = s; AT[#AT + 1] = T.now end,
	executeAsync = function(_, s, done)
		SENT[#SENT + 1] = s; AT[#AT + 1] = T.now
		if REPLY.fail > 0 then
			REPLY.fail = REPLY.fail - 1
			T.after(100, function() done(nil, "timeout") end)
		else
			T.after(10, function() done("ok", nil) end)
		end
	end,
}
function newPanel(id, name)
	local p = RemotePanel:new(CLU, id, name)
	for i = 0, 3 do p:addButton(i, RemotePanelButton:new(CLU, id .. "B" .. i)) end
	return p
end
P = newPanel("PAN1", "P1_PANEL_SALON")
function sent() local s = table.concat(SENT, "\n"); SENT = {}; AT = {}; return s end
LAMP = VirtualSwitch:new("lamp")
SET = { n = 0, v = nil }
TEMP = { v = 21 }
`

func newPanelEngine(t *testing.T, setup string) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(panelHarness); err != nil {
		t.Fatalf("harness: %v", err)
	}
	if setup != "" {
		if _, err := engine.Execute(setup); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	return engine
}

func mustContain(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s: missing %q in\n%s", what, w, got)
		}
	}
}

func mustNotContain(t *testing.T, what, got string, nots ...string) {
	t.Helper()
	for _, w := range nots {
		if strings.Contains(got, w) {
			t.Errorf("%s: unexpected %q in\n%s", what, w, got)
		}
	}
}

// Strings reach the CLU as quoted, escaped Lua literals.
func TestRemoteObjectStringArguments(t *testing.T) {
	engine := newPanelEngine(t, "")
	got := mustExec(t, engine, `(function()
		P:execute(3, 0, 0, 'a"b\\c' .. "\n" .. "d", 1)
		P:set(4, "x\"y")
		return sent()
	end)()`)
	want := `PAN1:execute(3, 0, 0, "a\"b\\c\nd", 1)` + "\n" + `PAN1:set(4, "x\"y")`
	if got != want {
		t.Errorf("sent\n got: %s\nwant: %s", got, want)
	}
	if lit := mustExec(t, engine, `return RemoteObject.literal("\1z")`); lit != `"\001z"` {
		t.Errorf("control character = %s", lit)
	}
}

const menuSetup = `
PANEL = Panel.new(P)
	:home(Panel.menu("Dom")
		:toggle("Światło", LAMP)
		:number("Zadana", function() return TEMP.v end, function(v) SET.n = SET.n + 1; SET.v = v; TEMP.v = v end,
			{ step = 0.5, min = 19, max = 22, format = "%.1f" })
		:item("Sypialnia", Panel.menu("Sypialnia"):value("Temp", function() return 20.5 end, "%.1f")))
	:idle(Panel.idle{ { "Salon", function() return TEMP.v end } })
	:sleepAfter(30)
`

// The home page goes out as short messages spaced on the clock, the last with a reply;
// the corners show hints and the LEDs follow them.
func TestPanelHomeFrame(t *testing.T) {
	engine := newPanelEngine(t, menuSetup)
	got := mustExec(t, engine, `(function()
		T.advance(1000)
		local out, max, spaced = sent(), 0, true
		return out
	end)()`)
	mustContain(t, "home frame", got,
		`PAN1:set(20, 0)`,
		`PAN1:execute(2)`,
		`PAN1:execute(3, 0, 14, ">", 1)`, `PAN1:execute(3, 8, 14, "Swiatlo", 1)`,
		// right-aligned by the measured widths: O 8 + F 7 + F 7, 2 8 + 1 7 + . 4 + 0 8
		`PAN1:execute(3, 106, 14, "OFF", 1)`, `PAN1:execute(3, 101, 27, "21.0", 1)`, `"Dom"`,
		`PAN1:execute(3, 120, 54, ">", 1)`, `PAN1:execute(3, 120, 0, "<", 1)`, `"<"`,
		`PAN1:execute(7)`,
		`PAN1B0:execute(1)`, `PAN1B1:execute(1)`, `PAN1B2:execute(1)`, `PAN1B3:execute(1)`)

	// no message over the limit, consecutive messages at least SPACING_MS apart, and each
	// message one expression: the CLU runs nothing of a message with two statements
	check := mustExec(t, engine, `(function()
		RemotePanel.MAX_MESSAGE = 120
		PANEL:redraw(true)
		T.advance(1000)
		local bad = {}
		for i, s in ipairs(SENT) do
			if #s > RemotePanel.MAX_MESSAGE then bad[#bad + 1] = "long " .. #s end
			if i > 1 and AT[i] - AT[i - 1] < RemotePanel.SPACING_MS then bad[#bad + 1] = "close " .. i end
			if s:sub(1, 12) ~= "(function() " or s:sub(-7) ~= " end)()" then bad[#bad + 1] = "bare " .. i end
		end
		RemotePanel.MAX_MESSAGE = 300
		return #SENT .. "|" .. table.concat(bad, ",")
	end)()`)
	if !strings.HasSuffix(check, "|") || strings.HasPrefix(check, "1|") || strings.HasPrefix(check, "0|") {
		t.Errorf("parts|problems = %s, want several parts and no problems", check)
	}
}

// One statement that does not fit the budget still goes out, alone and wrapped.
func TestRemotePanelPackWrapsEveryPart(t *testing.T) {
	engine := newPanelEngine(t, "")
	got := mustExec(t, engine, `(function()
		local parts = RemotePanel._pack({ "a()", string.rep("b", 400), "c()" })
		return #parts .. "|" .. parts[1] .. "|" .. #parts[2] .. "|" .. parts[3]
	end)()`)
	if want := "3|(function() a() end)()|419|(function() c() end)()"; got != want {
		t.Errorf("pack = %s, want %s", got, want)
	}
}

// Moving the cursor redraws the whole screen (nothing on the panel fills a box) and
// leaves the LEDs alone; OK on a toggle row switches the object and the row follows it.
func TestPanelMenuCursorAndToggle(t *testing.T) {
	engine := newPanelEngine(t, menuSetup)
	mustExec(t, engine, `T.advance(1000); sent()`)

	moved := mustExec(t, engine, `(function() click("P1_PANEL_SALON", "btn3"); T.advance(1000); return sent() end)()`)
	mustContain(t, "cursor down", moved, `PAN1:execute(2)`, `PAN1:execute(3, 8, 14, "Swiatlo", 1)`, `PAN1:execute(3, 0, 27, ">", 1)`, `PAN1:execute(3, 8, 27, "Zadana", 1)`, `"Dom"`, `PAN1:execute(7)`)
	mustNotContain(t, "cursor down", moved, `PAN1B0:execute(1)`, `PAN1B3:execute(1)`)

	toggled := mustExec(t, engine, `(function()
		click("PAN1B0", "btn1") -- up, back to the toggle row (by the button's id this time)
		click("PAN1", "btn4")   -- OK
		T.advance(1000)
		return LAMP:getValue() .. "|" .. sent()
	end)()`)
	mustContain(t, "toggle", toggled, "1|", `"ON"`)

	// a change from elsewhere redraws the row
	other := mustExec(t, engine, `(function() LAMP:setValue(0); T.advance(1000); return sent() end)()`)
	mustContain(t, "external change", other, `"OFF"`)
}

// A number edits in place: OK, +/-, OK sets it once; min/max hold; back cancels.
func TestPanelMenuNumberEdit(t *testing.T) {
	engine := newPanelEngine(t, menuSetup)
	got := mustExec(t, engine, `(function()
		T.advance(1000)
		click("PAN1", "btn3")              -- down to Zadana
		click("PAN1", "btn4")              -- edit
		T.advance(1000); local editing = sent()
		for i = 1, 5 do click("PAN1", "btn1") end -- +2.5, held at 22
		T.advance(1000); local up = sent()
		local before = SET.n
		click("PAN1", "btn4")              -- set
		T.advance(1000)
		local after1 = SET.n .. "/" .. tostring(SET.v)
		click("PAN1", "btn4"); click("PAN1", "btn3"); click("PAN1", "btn2") -- edit, -, back = cancel
		T.advance(1000)
		return editing .. "\n#" .. up .. "\n#" .. before .. "|" .. after1 .. "|" .. SET.n .. "/" .. TEMP.v
	end)()`)
	mustContain(t, "edit mode", got, `"+"`, `"-"`, `"x"`, `"21.0"`)
	mustNotContain(t, "edit mode", got, `"[`)
	mustContain(t, "held at max", got, `"22.0"`)
	if !strings.HasSuffix(got, "0|1/22|1/22") {
		t.Errorf("set count/value = %s, want 0|1/22|1/22", got[strings.LastIndex(got, "#"):])
	}
}

// The back corner leaves a submenu; on home it goes to the idle screen; any button there
// opens home; sleepAfter returns to idle.
func TestPanelBackAndIdle(t *testing.T) {
	engine := newPanelEngine(t, menuSetup)
	got := mustExec(t, engine, `(function()
		T.advance(1000); sent()
		click("PAN1", "btn1")             -- up wraps to Sypialnia
		click("PAN1", "btn4")             -- open
		T.advance(1000); local sub = sent()
		click("PAN1", "btn2")             -- back
		T.advance(1000); local home = sent()
		click("PAN1", "btn2")             -- back on home: idle
		T.advance(1000); local idle = sent()
		click("PAN1", "btn3")             -- any button: home
		T.advance(1000); local again = sent()
		T.advance(31000); local slept = sent()
		return sub .. "\n#1\n" .. home .. "\n#2\n" .. idle .. "\n#3\n" .. again .. "\n#4\n" .. slept
	end)()`)
	parts := strings.Split(got, "\n#")
	if len(parts) != 5 {
		t.Fatalf("parts: %q", got)
	}
	mustContain(t, "submenu", parts[0], `"Sypialnia"`, `"20.5"`)
	mustContain(t, "back to home", parts[1], `"Dom"`)
	mustContain(t, "idle", parts[2], `"12:34  16.10"`, `"Salon"`, `"21.0"`)
	mustNotContain(t, "idle", parts[2], `"<"`)
	mustContain(t, "home from idle", parts[3], `"Dom"`)
	mustContain(t, "sleep", parts[4], `"12:34  16.10"`)
}

// A frame whose last message gets no answer is sent again whole; a newer frame replaces
// what is left of an older one.
func TestPanelLostFrameAndCoalescing(t *testing.T) {
	engine := newPanelEngine(t, menuSetup)
	got := mustExec(t, engine, `(function()
		T.advance(1000); sent()
		REPLY.fail = 1
		click("PAN1", "btn3")
		T.advance(2000)
		local resent = sent()
		local clears = select(2, resent:gsub("PAN1:execute%(2%)", ""))
		click("PAN1", "btn3"); T.advance(25); click("PAN1", "btn3")
		T.advance(2000)
		local burst = sent()
		return clears .. "|" .. burst
	end)()`)
	// the frame itself and one resend, each a whole screen
	if !strings.HasPrefix(got, "2|") {
		t.Errorf("lost frame: clear screens sent = %s, want 2 (frame and resend)", got[:strings.Index(got, "|")])
	}
	burst := got[strings.Index(got, "|")+1:]
	mustContain(t, "after two quick moves", burst, `PAN1:execute(3, 0, 14, ">", 1)`, `PAN1:execute(3, 8, 14, "Swiatlo", 1)`)
}

// Cleaning: a countdown, inputs ignored, buzzer off and back on, then the page it came from.
func TestPanelCleaning(t *testing.T) {
	engine := newPanelEngine(t, `
		PANEL = Panel.new(P):home(Panel.menu("Dom"):action("Czyszczenie", Panel.cleaning(3)))`)
	got := mustExec(t, engine, `(function()
		T.advance(1000); sent()
		click("PAN1", "btn4")
		T.advance(500); local start = sent()
		click("PAN1", "btn2"); click("PAN1", "btn4")
		T.advance(1000); local tick = sent()
		T.advance(3000); local done = sent()
		return start .. "\n#" .. tick .. "\n#" .. done
	end)()`)
	parts := strings.Split(got, "\n#")
	// the LEDs lit at home (OK and back) go off; the two that were off are left alone
	mustContain(t, "cleaning start", parts[0], `PAN1:set(13, 0)`, `"Czyszczenie"`, `"3", 3`,
		`PAN1B1:execute(2)`, `PAN1B3:execute(2)`)
	mustNotContain(t, "cleaning start", parts[0], `"<"`, `PAN1B0:execute(2)`, `PAN1B2:execute(2)`)
	mustContain(t, "countdown", parts[1], `"2", 3`)
	mustNotContain(t, "inputs ignored", parts[1], `"Dom"`)
	mustContain(t, "cleaning end", parts[2], `PAN1:set(13, 1)`, `"Dom"`, `">", 1)`, `"Czyszczenie"`)
}

// notify shows over the page and goes away; a registered page type gets the back corner;
// two panels keep their own state; wake redraws in full; unknown inputs do nothing.
func TestPanelNotifyRegisterAndState(t *testing.T) {
	engine := newPanelEngine(t, `
		Panel.register("counter", function()
			local page = { n = 0 }
			page.keys = { up = { "+" } }
			page.draw = function(s) s:region("n", 0, 26, 128, 12):text(0, 27, "n=" .. page.n) end
			page.input = function(name) if name == "up" then page.n = page.n + 1 end end
			return page
		end)
		P2 = newPanel("PAN2", "P2_PANEL")
		A = Panel.new(P):home(Panel.menu("A"):item("Licznik", Panel.counter()):item("B", Panel.menu("x")))
		B = Panel.new(P2):home(Panel.menu("B"):item("x", Panel.menu("y")):item("z", Panel.menu("w")))`)
	got := mustExec(t, engine, `(function()
		T.advance(1000); sent()
		click("PAN1", "btn4"); click("PAN1", "btn1"); click("PAN1", "btn1")
		T.advance(1000); local counter = sent()
		click("PAN2", "btn3")
		T.advance(1000); local other = sent()
		click("PAN1", "btn2")
		T.advance(1000); local back = sent()
		A:notify("Pranie gotowe", { seconds = 5 })
		T.advance(1000); local note = sent()
		T.advance(5000); local gone = sent()
		click("PAN1", "wake"); T.advance(1000); local wake = sent()
		local unknown = tostring(click("NOPE", "btn1")) .. tostring(click("PAN1", "btn9"))
		return counter .. "\n#" .. other .. "\n#" .. back .. "\n#" .. note .. "\n#" .. gone .. "\n#" .. wake .. "\n#" .. unknown
	end)()`)
	parts := strings.Split(got, "\n#")
	mustContain(t, "registered page", parts[0], `"n=2"`)
	mustContain(t, "other panel", parts[1], `PAN2:`, `">", 1)`, `, "z", 1)`)
	mustNotContain(t, "other panel", parts[1], `PAN1:`)
	mustContain(t, "back from registered page", parts[2], `">", 1)`, `"Licznik"`)
	mustContain(t, "notify", parts[3], `"Pranie gotowe"`, `PAN1:execute(3, 120, 54, ">", 1)`)
	mustContain(t, "notify gone", parts[4], `">", 1)`, `"Licznik"`)
	mustContain(t, "wake", parts[5], `PAN1:execute(2)`, `">", 1)`, `"Licznik"`)
	if parts[6] != "falsetrue" {
		t.Errorf("unknown panel / input = %s, want falsetrue", parts[6])
	}
}

// executeAsync does not block: the answer (or the error) comes back later through
// __vclu_command_reply to the callback.
func TestCLURemoteExecuteAsync(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	engine.SetCommandSender(func(ip, script string) (string, error) {
		if strings.Contains(script, "lost") {
			return "", errTimeoutForTest
		}
		return "answer:" + ip + ":" + script, nil
	})
	mustExec(t, engine, `(function()
		GOT = {}
		local clu = CLU_REMOTE:new(1, "192.168.0.9", system:getEventBus())
		clu:executeAsync("x()", function(r, err) GOT.ok = tostring(r) .. "|" .. tostring(err) end)
		clu:executeAsync("lost()", function(r, err) GOT.lost = tostring(err) end)
	end)()`)
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := mustExec(t, engine, `return tostring(GOT.ok) .. "#" .. tostring(GOT.lost)`)
		if got == "answer:192.168.0.9:x()|nil#timeout" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("replies = %s", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var errTimeoutForTest = errors.New("timeout")

// A value row opens its chart: hourly time-weighted means, the current hour chosen,
// down picks the hour before; bars scale between the lowest and highest hour.
func TestPanelChart(t *testing.T) {
	engine := newPanelEngine(t, `
		local H = 3600000
		T.now = 12 * H + H / 2
		HIST = {
			{ value = 22, atMs = 12 * H },
			{ value = 20, atMs = 7 * H + H / 2 },
			{ value = 21, atMs = 0 },
		}
		PANEL = Panel.new(P):home(Panel.menu("Dom")
			:value("Salon", function() return 22 end, "%.1f", Panel.chart("Salon", function() return HIST end)))`)
	got := mustExec(t, engine, `(function()
		T.advance(100); sent()
		click("PAN1", "btn4"); T.advance(1000)
		local a = sent()
		click("PAN1", "btn3"); T.advance(1000)
		return a .. "\n#" .. sent()
	end)()`)
	parts := strings.Split(got, "\n#")
	mustContain(t, "chart", parts[0], `"Salon"`, `"12:00"`, `PAN1:execute(3, 94, 30, "22.0", 2)`,
		`PAN1:execute(4, 76, 57, 78, 55, 1)`, `PAN1:execute(4, 79, 55, 81, 57, 1)`, // ^ under it
		`PAN1:execute(4, 77, 13, 77, 52, 1)`, `PAN1:execute(4, 80, 13, 80, 52, 1)`, // the chosen hour, 4 px
		`PAN1:execute(4, 1, 31, 1, 52, 1)`, `PAN1:execute(4, 2, 31, 2, 52, 1)`, // 21 between 20 and 22
		`PAN1:execute(4, 50, 50, 50, 52, 1)`, // 20, the lowest: a stub
		`PAN1B3:execute(2)`) // no OK on the chart
	mustNotContain(t, "chart", parts[0], `PAN1:execute(4, 0, 31, 0, 52, 1)`)
	mustContain(t, "hour before", parts[1], `"11:00"`, `"20.0", 2)`,
		`PAN1:execute(4, 70, 50, 70, 52, 1)`, `PAN1:execute(4, 73, 50, 73, 52, 1)`)

	mean := mustExec(t, engine, `(function()
		local b = Panel._hourly(HIST, 12, T.now)
		return string.format("%s %s %s %s", b[6], b[7], b[11], b[12])
	end)()`)
	if mean != "21 20.5 20 22" {
		t.Errorf("hourly means = %q, want %q", mean, "21 20.5 20 22")
	}
}
