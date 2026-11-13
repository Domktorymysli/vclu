package lua

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// fakeCLU answers a script the way a Grenton CLU does: it puts return before the script,
// runs it next to the getVar/setVar that Object Manager uploads, and answers with the
// value as plain text (a string without quotes, nil as "nil", a number as Lua 5.1 prints
// it, so 1e+15 and 5e-05 in exponent form). A script that does not
// compile gets no answer, so the caller sees a timeout.
type fakeCLU struct {
	mu   sync.Mutex
	L    *lua.LState
	sent []string
	down bool
}

func newFakeCLU(t *testing.T) *fakeCLU {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	if err := L.DoString(`
function setVar(name, value) _G[name] = value end
function getVar(name) return _G[name] end
`); err != nil {
		t.Fatal(err)
	}
	return &fakeCLU{L: L}
}

func (c *fakeCLU) send(ip, script string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, script)
	if c.down {
		return "", errors.New("i/o timeout")
	}
	fn, err := c.L.LoadString("return " + script)
	if err != nil {
		return "", errors.New("i/o timeout")
	}
	c.L.Push(fn)
	if err := c.L.PCall(0, 1, nil); err != nil {
		return "", errors.New("i/o timeout")
	}
	v := c.L.Get(-1)
	c.L.Pop(1)
	if n, ok := v.(lua.LNumber); ok {
		return fmt.Sprintf("%.14g", float64(n)), nil
	}
	return v.String(), nil
}

func (c *fakeCLU) global(name string) lua.LValue {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.L.GetGlobal(name)
}

func (c *fakeCLU) last() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sent) == 0 {
		return ""
	}
	return c.sent[len(c.sent)-1]
}

func newFeaturesEngine(t *testing.T) (*LuaEngine, *fakeCLU) {
	t.Helper()
	clu := newFakeCLU(t)
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	engine.SetCommandSender(clu.send)
	mustExec(t, engine, `
CLU221000290 = CLU_REMOTE:new(221000290, 0xC0A80003, EventBus:getShared())
WARN = {}
local logger = Logger:getShared()
local warn = logger.warn
logger.warn = function(self, fmt, ...)
	local ok, line = pcall(string.format, tostring(fmt), ...)
	WARN[#WARN + 1] = ok and line or tostring(fmt)
	return warn(self, fmt, ...)
end`)
	return engine, clu
}

// get returns the feature with its type; an unset feature is nil without an error.
func TestCLUFeatureGetTyped(t *testing.T) {
	engine, clu := newFeaturesEngine(t)
	if err := clu.L.DoString(`
temp_zadana = 21.5
licznik = 3
duza = 1e15
mala = 5e-05
ujemna = -2.5e+20
tryb = "21"
pusty = "nil"
dwukropek = "a:b"
wlaczony = true
wylaczony = false
`); err != nil {
		t.Fatal(err)
	}
	got := mustExec(t, engine, `
local out = {}
for _, n in ipairs({"temp_zadana", "licznik", "duza", "mala", "ujemna", "tryb", "pusty", "dwukropek", "wlaczony", "wylaczony", "nie_ma"}) do
	local v, err = CLU221000290:get(n)
	out[#out + 1] = n .. "=" .. type(v) .. ":" .. tostring(v) .. ":" .. tostring(err)
end
return table.concat(out, " ")`)
	want := "temp_zadana=number:21.5:nil licznik=number:3:nil duza=number:1000000000000000:nil mala=number:5e-05:nil " +
		"ujemna=number:-2.5e+20:nil tryb=string:21:nil pusty=string:nil:nil " +
		"dwukropek=string:a:b:nil wlaczony=boolean:true:nil wylaczony=boolean:false:nil nie_ma=nil:nil:nil"
	if got != want {
		t.Errorf("get:\n got %s\nwant %s", got, want)
	}
	if s := clu.last(); strings.HasPrefix(strings.TrimSpace(s), "return") {
		t.Errorf("script starts with return, the CLU adds it: %s", s)
	}
}

// set writes the value with its type; get reads back what set wrote.
func TestCLUFeatureSetRoundTrip(t *testing.T) {
	engine, clu := newFeaturesEngine(t)
	got := mustExec(t, engine, `
local out = {}
for _, v in ipairs({21, 21.5, -0.125, 1e15, "tekst", "21", "", "żółw", true, false}) do
	local ok, err = CLU221000290:set("cecha", v)
	local back = CLU221000290:get("cecha")
	out[#out + 1] = tostring(ok) .. tostring(err) .. ":" .. type(back) .. ":" .. tostring(back == v)
end
assert(CLU221000290:set("cecha", nil))
out[#out + 1] = tostring(CLU221000290:get("cecha"))
return table.concat(out, " ")`)
	want := strings.Repeat("truenil:number:true ", 4) + strings.Repeat("truenil:string:true ", 4) +
		"truenil:boolean:true truenil:boolean:true nil"
	if got != want {
		t.Errorf("round trip:\n got %s\nwant %s", got, want)
	}
	if v := clu.global("cecha"); v != lua.LNil {
		t.Errorf("cecha on the CLU after set(nil) = %v", v)
	}
}

// A string value cannot leave its literal: quotes, backslashes, newlines and other
// control characters reach the CLU as the same bytes, and nothing else runs.
func TestCLUFeatureSetEscapes(t *testing.T) {
	engine, clu := newFeaturesEngine(t)
	values := []string{
		`a"b`,
		`a\`,
		`a\"); setVar("hacked", 1); x = ("`,
		"line1\nline2\r\x00end",
		`"]] .. hacked = 1 .. [["`,
		"\x01\x1f\x7f",
		"\x001",
		"\x0011",
		`\065`,
	}
	for _, v := range values {
		engine.SetGlobal("V", v)
		got := mustExec(t, engine, `local ok, err = CLU221000290:set("cecha", V); return tostring(ok) .. tostring(err)`)
		if got != "truenil" {
			t.Fatalf("set(%q) = %s, script %s", v, got, clu.last())
		}
		if s := clu.global("cecha"); s.Type() != lua.LTString || string(s.(lua.LString)) != v {
			t.Errorf("set(%q): the CLU has %q, script %s", v, s, clu.last())
		}
	}
	if v := clu.global("hacked"); v != lua.LNil {
		t.Errorf("a value ran code on the CLU: hacked = %v", v)
	}
}

// A name that is not a Lua identifier is an error, and nothing is sent.
func TestCLUFeatureNameChecked(t *testing.T) {
	engine, clu := newFeaturesEngine(t)
	for _, call := range []string{
		`CLU221000290:get('x") os.exit() --')`,
		`CLU221000290:get("1abc")`,
		`CLU221000290:get("")`,
		`CLU221000290:get("a.b")`,
		`CLU221000290:get(17)`,
		`CLU221000290:get(nil)`,
		`CLU221000290:get("żółw")`,
		`CLU221000290:get("x\n")`,
		`CLU221000290:get("x\0")`,
		`CLU221000290:set("a b", 1)`,
		`CLU221000290:set(17, 1)`,
	} {
		_, err := engine.Execute(call)
		if err == nil || !strings.Contains(err.Error(), "Lua identifier") {
			t.Errorf("%s: error = %v, want a name error", call, err)
		}
	}
	if len(clu.sent) != 0 {
		t.Errorf("sent to the CLU: %q", clu.sent)
	}
	if _, err := engine.Execute(`assert(CLU221000290:get("_Temp_2") == nil)`); err != nil {
		t.Errorf("get(_Temp_2): %v", err)
	}
	if got := clu.last(); !strings.Contains(got, `getVar("_Temp_2")`) {
		t.Errorf("get(_Temp_2) sent %s", got)
	}
}

// A value that has no literal (a table, a function, NaN, infinity) is an error, and
// nothing is sent.
func TestCLUFeatureSetRejectsValues(t *testing.T) {
	engine, clu := newFeaturesEngine(t)
	for _, v := range []string{"{}", "print", "0/0", "1/0", "-1/0"} {
		_, err := engine.Execute(fmt.Sprintf(`CLU221000290:set("cecha", %s)`, v))
		if err == nil || !strings.Contains(err.Error(), "cannot send") {
			t.Errorf("set(%s): error = %v, want cannot send", v, err)
		}
	}
	if len(clu.sent) != 0 {
		t.Errorf("sent to the CLU: %q", clu.sent)
	}
}

// When the CLU does not answer, get and set give nil and the error, and log a warning.
func TestCLUFeatureTimeout(t *testing.T) {
	engine, clu := newFeaturesEngine(t)
	clu.down = true
	got := mustExec(t, engine, `
local v, err = CLU221000290:get("temp_zadana")
local ok, err2 = CLU221000290:set("temp_zadana", 20)
return tostring(v) .. "|" .. tostring(err) .. "|" .. tostring(ok) .. "|" .. tostring(err2) .. "|" .. #WARN`)
	if got != "nil|i/o timeout|nil|i/o timeout|2" {
		t.Errorf("timeout: %s", got)
	}
}

// Answers as a CLU may send them: numbers in any form Lua 5.1 prints, and answers that
// are not type:value (an older or changed getVar), which are an error, not a value.
func TestCLUFeatureAnswers(t *testing.T) {
	engine, _ := newFeaturesEngine(t)
	for answer, want := range map[string]string{
		"number:21.50":      "number:21.5:nil",
		"number:1e+15":      "number:true:nil",
		"number:-5E-05":     "number:true:nil",
		"number:3":          "number:3:nil",
		"string:":           "string::nil",
		"string:a\nb":       "string:a\nb:nil",
		"number:inf":        "nil:nil:unexpected answer: number:inf",
		"21.50":             "nil:nil:unexpected answer: 21.50",
		"nil":               "nil:nil:unexpected answer: nil",
		"":                  "nil:nil:empty answer (a value longer than about 2000 characters?)",
		"table:table: 0x01": "nil:nil:unexpected answer: table:table: 0x01",
	} {
		engine.SetCommandSender(func(ip, script string) (string, error) { return answer, nil })
		got := mustExec(t, engine, `local v, err = CLU221000290:get("x")
local shown = tostring(v)
if v == 1e15 or v == -5e-05 then shown = "true" end
return type(v) .. ":" .. shown .. ":" .. tostring(err)`)
		if got != want {
			t.Errorf("answer %q: got %s, want %s", answer, got, want)
		}
	}
}

// The MCP sandbox records a script and does not answer: get and set give nil, "no answer"
// without a warning per call.
func TestCLUFeatureSandboxNoAnswer(t *testing.T) {
	engine, _ := newFeaturesEngine(t)
	got := mustExec(t, engine, `
__go_send_command = function(ip, script) end
local v, err = CLU221000290:get("x")
local ok, err2 = CLU221000290:set("x", 1)
return tostring(v) .. "|" .. tostring(err) .. "|" .. tostring(ok) .. "|" .. tostring(err2) .. "|" .. #WARN`)
	if got != "nil|no answer|nil|no answer|0" {
		t.Errorf("sandbox: %s", got)
	}
}

// execute with a script that starts with return warns, since the CLU will not answer it;
// an expression that only contains the word does not.
func TestCLUExecuteReturnWarns(t *testing.T) {
	engine, _ := newFeaturesEngine(t)
	got := mustExec(t, engine, `
CLU221000290:execute(0, "return 1+1")
CLU221000290:execute(0, "  return(1)")
CLU221000290:execute(0, "return")
CLU221000290:execute(0, "-- odczyt\n  -- drugi\nreturn 1")
local n = #WARN
CLU221000290:execute(0, "returned_value")
CLU221000290:execute(0, '(function() return 1 end)()')
return n .. "|" .. #WARN .. "|" .. WARN[1]`)
	if !strings.HasPrefix(got, "4|4|") || !strings.Contains(got, "adds return itself") {
		t.Errorf("warnings: %s", got)
	}
}
