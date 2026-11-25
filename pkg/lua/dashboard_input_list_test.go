package lua

import (
	"encoding/json"
	"strings"
	"testing"
)

// Text input and list: what the page gets, the text as onSubmit/onAdd get it (trimmed, empty
// and too long refused), item ids checked against getItems(), refusals with their message.

const dashInputListSetup = `
	_dashboards = {}
	A = { sent = {}, items = { { id = 1, label = 'Mleko' }, { id = 'b', label = 'Chleb', done = true, note = '2 szt.' } }, calls = 0 }
	Dashboard.new('app'):remote()
		:section('Zakupy', 'cart')
			:input('in', 'Notatka', nil, function(text)
				if text == 'zła' then return false, 'Nie tak' end
				if text == 'nil' then return nil, 'Brak' end
				if text == 'boom' then error('wybuch', 0) end
				A.sent[#A.sent + 1] = text
				if text == 'toast' then return 'Zapisano' end
			end, { placeholder = 'Wpisz…', button = 'Zapisz', maxLength = 10, span = 2 })
			:list('ls', 'Lista', function() return A.items end, {
				onToggle = function(id, done) A.calls = A.calls + 1; A.toggled = { id, done }
					for _, it in ipairs(A.items) do if it.id == id then it.done = done end end end,
				onDelete = function(id) A.calls = A.calls + 1; A.deleted = id
					for i, it in ipairs(A.items) do if it.id == id then table.remove(A.items, i); break end end end,
				onAdd = function(text)
					if #A.items >= 3 then return false, 'Lista pełna' end
					A.items[#A.items + 1] = { id = 'n' .. #A.items, label = text }
				end,
				placeholder = 'Dodaj…', empty = 'Pusto', maxItems = 2 })
			:list('ro', 'Tylko odczyt', function() return A.items end, { remote = 'readonly', onDelete = function() A.calls = A.calls + 1 end })
			:list('bare', 'Bez akcji', function() return A.bad or {} end)
		:build()
`

func newInputListEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashInputListSetup)
	return engine
}

func actionJSON(t *testing.T, engine *LuaEngine, call string) map[string]interface{} {
	t.Helper()
	var res map[string]interface{}
	dashJSON(t, engine, "return Dashboard.API.executeAction("+call+")", &res)
	return res
}

func TestDashboardInputListDefinition(t *testing.T) {
	engine := newInputListEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("app")`, &def)

	in := def.widget("in")
	if in["type"] != "input" || in["icon"] != "input-cursor-text" || in["placeholder"] != "Wpisz…" || in["button"] != "Zapisz" ||
		in["maxLength"] != float64(10) || in["span"] != float64(2) {
		t.Errorf("input = %v", in)
	}
	ls := def.widget("ls")
	caps, _ := ls["capabilities"].(map[string]interface{})
	if ls["type"] != "list" || ls["icon"] != "list-check" || ls["empty"] != "Pusto" || ls["placeholder"] != "Dodaj…" ||
		ls["maxLength"] != float64(200) || ls["total"] != float64(2) ||
		caps["toggle"] != true || caps["delete"] != true || caps["add"] != true {
		t.Errorf("list = %v", ls)
	}
	items := list(ls["items"])
	if len(items) != 2 || items[0]["id"] != float64(1) || items[0]["label"] != "Mleko" || items[0]["done"] != false ||
		items[1]["id"] != "b" || items[1]["done"] != true || items[1]["note"] != "2 szt." {
		t.Errorf("items = %v", ls["items"])
	}
	bare := def.widget("bare")
	caps, _ = bare["capabilities"].(map[string]interface{})
	if caps["toggle"] != false || caps["delete"] != false || caps["add"] != false || bare["empty"] != "—" {
		t.Errorf("bare = %v", bare)
	}
	if items, ok := bare["items"].([]interface{}); !ok || len(items) != 0 {
		t.Errorf("bare items = %#v (want an empty list)", bare["items"])
	}

	// maxItems: the page gets the first ones, total counts all
	mustExec(t, engine, `A.items[3] = { id = 3, label = 'Masło' }`)
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("app")`, &vals)
	if len(list(vals["ls"]["items"])) != 2 || vals["ls"]["total"] != float64(3) || vals["ls"]["disabled"] != false {
		t.Errorf("values ls = %v", vals["ls"])
	}
}

func TestDashboardInputSubmit(t *testing.T) {
	engine := newInputListEngine(t)

	if r := actionJSON(t, engine, `"app", "in", "submit", "  mleko \t"`); r["success"] != true {
		t.Errorf("submit = %v", r)
	}
	if got := mustExec(t, engine, `return A.sent[1]`); got != "mleko" {
		t.Errorf("onSubmit got %q, want trimmed", got)
	}
	// control characters become spaces; a string is a toast
	if r := actionJSON(t, engine, `"app", "in", "submit", "toast"`); r["message"] != "Zapisano" {
		t.Errorf("toast = %v", r)
	}
	if got := mustExec(t, engine, `return Dashboard.API.executeAction("app", "in", "submit", "a\nb") and A.sent[3]`); got != "a b" {
		t.Errorf("newline: %q", got)
	}

	// refused without calling onSubmit
	for call, want := range map[string]string{
		`"app", "in", "submit", "   "`:         "Empty text",
		`"app", "in", "submit", ""`:            "Empty text",
		`"app", "in", "submit", 5`:             "Value must be text",
		`"app", "in", "submit", { a = 1 }`:     "Value must be text",
		`"app", "in", "submit", "12345678901"`: "longer than 10",
		`"app", "in", "tap", "x"`:              "Invalid action",
	} {
		r := actionJSON(t, engine, call)
		if e, _ := r["error"].(string); !strings.Contains(e, want) {
			t.Errorf("%s = %v, want %q", call, r, want)
		}
	}
	// 10 characters of Polish letters are 10, not 20 bytes
	if r := actionJSON(t, engine, `"app", "in", "submit", "żółćżółćżó"`); r["success"] != true {
		t.Errorf("10 Polish characters = %v", r)
	}
	if got := mustExec(t, engine, `return #A.sent`); got != "4" {
		t.Errorf("onSubmit called %s times, want 4", got)
	}

	// onSubmit's refusal and error carry the message for the field
	for call, want := range map[string]string{
		`"app", "in", "submit", "zła"`:  "Nie tak",
		`"app", "in", "submit", "nil"`:  "Brak",
		`"app", "in", "submit", "boom"`: "wybuch",
	} {
		r := actionJSON(t, engine, call)
		if r["error"] != want || r["field"] != true || r["success"] != nil {
			t.Errorf("%s = %v, want error %q", call, r, want)
		}
	}
}

func TestDashboardInputHardCap(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `_dashboards = {}; GOT = nil
		Dashboard.new('c'):section('S'):input('in', 'In', 'pencil', function(t) GOT = t end, { maxLength = 1000 }):build()`)
	long := strings.Repeat("a", 1001)
	r := actionJSON(t, engine, `"c", "in", "submit", "`+long+`"`)
	if e, _ := r["error"].(string); !strings.Contains(e, "longer than 1000") {
		t.Errorf("1001 characters = %v", r)
	}
	if r := actionJSON(t, engine, `"c", "in", "submit", "`+long[:1000]+`"`); r["success"] != true {
		t.Errorf("1000 characters = %v", r)
	}
}

func TestDashboardListActions(t *testing.T) {
	engine := newInputListEngine(t)

	// a toggle by the id as text; onToggle gets it as getItems gave it (a number)
	r := actionJSON(t, engine, `"app", "ls", "toggle", { id = "1", done = true }`)
	if r["success"] != true {
		t.Fatalf("toggle = %v", r)
	}
	if got := mustExec(t, engine, `return type(A.toggled[1]) .. ' ' .. tostring(A.toggled[1]) .. ' ' .. tostring(A.toggled[2])`); got != "number 1 true" {
		t.Errorf("onToggle got %s", got)
	}
	// the answer has the list as it is now
	if items := list(r["items"]); len(items) != 2 || items[0]["done"] != true {
		t.Errorf("toggle reply items = %v", r["items"])
	}
	r = actionJSON(t, engine, `"app", "ls", "toggle", { id = "b", done = false }`)
	if r["success"] != true || mustExec(t, engine, `return tostring(A.items[2].done)`) != "false" {
		t.Errorf("toggle b = %v", r)
	}

	// unknown or bad ids: refused, callback not called
	calls := mustExec(t, engine, `return A.calls`)
	for call, want := range map[string]string{
		`"app", "ls", "toggle", { id = "nope", done = true }`: "Unknown item",
		`"app", "ls", "delete", { id = "1.0" }`:               "Unknown item",
		`"app", "ls", "delete", { id = "" }`:                  "Unknown item",
		`"app", "ls", "toggle", { id = "1" }`:                 "Value must be {id, done}",
		`"app", "ls", "toggle", { id = "1", done = "yes" }`:   "Value must be {id, done}",
		`"app", "ls", "delete", "1"`:                          "Value must be {id}",
		`"app", "ls", "delete", nil`:                          "Value must be {id}",
		`"app", "ls", "rename", { id = "1" }`:                 "Invalid action for list",
		`"app", "bare", "delete", { id = "1" }`:               "No onDelete function",
		`"app", "bare", "toggle", { id = "1", done = true }`:  "No onToggle function",
		`"app", "bare", "add", "x"`:                           "No onAdd function",
	} {
		r := actionJSON(t, engine, call)
		if e, _ := r["error"].(string); !strings.Contains(e, want) {
			t.Errorf("%s = %v, want %q", call, r, want)
		}
	}
	if got := mustExec(t, engine, `return A.calls`); got != calls {
		t.Errorf("callbacks called on refused actions: %s, was %s", got, calls)
	}

	// a number id from the page is compared as text too
	if r := actionJSON(t, engine, `"app", "ls", "delete", { id = 1 }`); r["success"] != true {
		t.Errorf("delete 1 = %v", r)
	}
	if got := mustExec(t, engine, `return #A.items .. ' ' .. A.items[1].id`); got != "1 b" {
		t.Errorf("after delete: %s", got)
	}

	// add: trimmed, empty refused, onAdd's refusal for the field
	if r := actionJSON(t, engine, `"app", "ls", "add", "  Jajka  "`); r["success"] != true || len(list(r["items"])) != 2 {
		t.Errorf("add = %v", r)
	}
	if got := mustExec(t, engine, `return A.items[2].label`); got != "Jajka" {
		t.Errorf("added %q", got)
	}
	if r := actionJSON(t, engine, `"app", "ls", "add", "   "`); r["error"] != "Empty text" {
		t.Errorf("empty add = %v", r)
	}
	if r := actionJSON(t, engine, `"app", "ls", "add", "`+strings.Repeat("x", 201)+`"`); !strings.Contains(r["error"].(string), "longer than 200") {
		t.Errorf("long add = %v", r)
	}
	actionJSON(t, engine, `"app", "ls", "add", "Ser"`)
	if r := actionJSON(t, engine, `"app", "ls", "add", "Woda"`); r["error"] != "Lista pełna" || r["field"] != true {
		t.Errorf("full add = %v", r)
	}
}

// Remote viewers: a readonly list refuses its actions; disabled and hidden refuse as for a toggle.
func TestDashboardListReadonlyDisabled(t *testing.T) {
	engine := newInputListEngine(t)
	r := actionJSON(t, engine, `"app", "ro", "delete", { id = "1" }, { role = "gosc" }`)
	if r["error"] != "readonly" || r["status"] != float64(403) {
		t.Errorf("readonly delete = %v", r)
	}
	if got := mustExec(t, engine, `return A.calls`); got != "0" {
		t.Errorf("readonly onDelete called")
	}
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("app", "gosc")`, &def)
	if w := def.widget("ro"); w["readonly"] != true || w["disabled"] != true {
		t.Errorf("remote ro = %v", w)
	}

	mustExec(t, engine, `_dashboards = {}; D = { off = true, show = true, got = 0 }
		Dashboard.new('d'):section('S')
			:input('in', 'In', nil, function() D.got = D.got + 1 end, { disabled = function() return D.off end,
				visible = function() return D.show end })
			:list('ls', 'Ls', function() return { { id = 1, label = 'a' } } end, { onDelete = function() D.got = D.got + 1 end,
				disabled = function() return D.off end })
		:build()`)
	for _, call := range []string{`"d", "in", "submit", "x"`, `"d", "ls", "delete", { id = "1" }`} {
		if r := actionJSON(t, engine, call); r["error"] != "Widget is disabled" {
			t.Errorf("%s disabled = %v", call, r)
		}
	}
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("d")`, &vals)
	if vals["in"]["disabled"] != true || vals["ls"]["disabled"] != true {
		t.Errorf("disabled values = %v", vals)
	}
	mustExec(t, engine, `D.off = false; D.show = false`)
	if r := actionJSON(t, engine, `"d", "in", "submit", "x"`); r["error"] != "Widget is hidden" {
		t.Errorf("hidden input = %v", r)
	}
	if got := mustExec(t, engine, `return D.got`); got != "0" {
		t.Errorf("callbacks ran: %s", got)
	}
}

// getItems that fails or returns a bad item is the tile's error; actions on it are refused.
func TestDashboardListItemErrors(t *testing.T) {
	engine := newInputListEngine(t)
	for bad, want := range map[string]string{
		`'x'`:                                    "getItems must return a list",
		`{ a = 1 }`:                              "keys 1..n",
		`{ 5 }`:                                  "item 1 must be a table",
		`{ { label = 'a' } }`:                    "item 1: id must be a string or a number",
		`{ { id = 0/0, label = 'a' } }`:          "id must be",
		`{ { id = true, label = 'a' } }`:         "id must be",
		`{ { id = 1 } }`:                         "label must be a string",
		`{ { id = 1, label = 'a', done = 1 } }`:  "done must be a boolean",
		`{ { id = 1, label = 'a', note = {} } }`: "note must be a string",
		`{ { id = 1, label = 'a' }, { id = '1', label = 'b' } }`: `id "1" is used twice`,
	} {
		mustExec(t, engine, `A.bad = `+bad)
		var vals map[string]map[string]interface{}
		dashJSON(t, engine, `Dashboard.API.getValues("app")`, &vals)
		if e, _ := vals["bare"]["error"].(string); !strings.Contains(e, want) || !strings.Contains(e, `section:list("bare")`) {
			t.Errorf("%s: error %v, want %q", bad, vals["bare"], want)
		}
	}
	mustExec(t, engine, `A.items = function() end`)
	mustExec(t, engine, `_dashboards.app = nil; Dashboard.new('f'):section('S'):list('l', 'L', function() error('db down', 0) end,
		{ onDelete = function() A.calls = A.calls + 1 end }):build()`)
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("f")`, &vals)
	if vals["l"]["error"] != "db down" {
		t.Errorf("failing getItems = %v", vals["l"])
	}
	if r := actionJSON(t, engine, `"f", "l", "delete", { id = "1" }`); r["error"] != "db down" {
		t.Errorf("delete with failing getItems = %v", r)
	}
	if got := mustExec(t, engine, `return A.calls`); got != "0" {
		t.Errorf("onDelete called")
	}
}

func TestDashboardInputListArgumentErrors(t *testing.T) {
	fn := `function() end`
	cases := []struct{ call, where, want string }{
		{`:input('', 'L', nil, ` + fn + `)`, "section:input", "id must be"},
		{`:input('a', 1, nil, ` + fn + `)`, "section:input", "label must be"},
		{`:input('a', 'L', 'x y', ` + fn + `)`, "section:input", "icon must be"},
		{`:input('a', 'L', nil, nil)`, "section:input", "onSubmit must be a function"},
		{`:input('a', 'L', nil, ` + fn + `, 'x')`, "section:input", "opts must be a table"},
		{`:input('a', 'L', nil, ` + fn + `, { size = 'lg' })`, "section:input", `unknown option "size"`},
		{`:input('a', 'L', nil, ` + fn + `, { placeholder = 1 })`, "section:input", "opts.placeholder must be a string"},
		{`:input('a', 'L', nil, ` + fn + `, { button = true })`, "section:input", "opts.button must be a string"},
		{`:input('a', 'L', nil, ` + fn + `, { maxLength = 1001 })`, "section:input", "opts.maxLength must be a whole number 1-1000"},
		{`:input('a', 'L', nil, ` + fn + `, { maxLength = 2.5 })`, "section:input", "opts.maxLength"},
		{`:input('a', 'L', nil, ` + fn + `, { disabled = true })`, "section:input", "opts.disabled must be a function"},
		{`:list('', 'L', ` + fn + `)`, "section:list", "id must be"},
		{`:list('a', nil, ` + fn + `)`, "section:list", "label must be"},
		{`:list('a', 'L', {})`, "section:list", "getItems must be a function"},
		{`:list('a', 'L', ` + fn + `, { onRemove = ` + fn + ` })`, "section:list", `unknown option "onRemove"`},
		{`:list('a', 'L', ` + fn + `, { onToggle = true })`, "section:list", "opts.onToggle must be a function"},
		{`:list('a', 'L', ` + fn + `, { onDelete = 'x' })`, "section:list", "opts.onDelete must be a function"},
		{`:list('a', 'L', ` + fn + `, { onAdd = 1 })`, "section:list", "opts.onAdd must be a function"},
		{`:list('a', 'L', ` + fn + `, { empty = false })`, "section:list", "opts.empty must be a string"},
		{`:list('a', 'L', ` + fn + `, { icon = 'a b' })`, "section:list", "opts.icon"},
		{`:list('a', 'L', ` + fn + `, { maxItems = 0 })`, "section:list", "opts.maxItems must be a whole number 1-1000"},
		{`:list('a', 'L', ` + fn + `, { maxItems = 1001 })`, "section:list", "opts.maxItems"},
		{`:list('a', 'L', ` + fn + `, { maxLength = 0 })`, "section:list", "opts.maxLength"},
	}
	for _, c := range cases {
		for _, via := range []string{":section('S', 'g')", ":header()"} {
			engine := NewLuaEngine()
			_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  Dashboard.new('e')" + via + c.call + "\nend)\nerror(err, 0)")
			engine.Close()
			want := c.want
			if via == ":header()" {
				want = "not the header"
			}
			if err == nil {
				t.Errorf("%s%s: no error", via, c.call)
				continue
			}
			msg := err.Error()
			if !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, c.where) || !strings.Contains(msg, want) {
				t.Errorf("%s%s: %s", via, c.call, msg)
			}
		}
	}
	// the builder's own methods point at the module's line too, and build
	engine := NewLuaEngine()
	defer engine.Close()
	for _, call := range []string{"b:input('a', 'L', nil, " + fn + ", { maxLength = 0 })", "b:list('a', 'L', " + fn + ", { maxItems = 'x' })"} {
		_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  local b = Dashboard.new('f'); b:section('S', 'g')\n  " + call + "\nend)\nerror(err, 0)")
		if err == nil || !strings.Contains(err.Error(), "<string>:4: ") {
			t.Errorf("builder %s: %v", call, err)
		}
	}
	mustExec(t, engine, "_dashboards = {}\nlocal b = Dashboard.new('g'); b:section('S', 'g')\nb:input('i', 'I', 'pencil', "+fn+")\nb:list('l', 'L', "+fn+"):build()")
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("g")`, &def)
	if w := def.widget("i"); w == nil || w["type"] != "input" || w["icon"] != "pencil" || w["maxLength"] != float64(200) {
		t.Errorf("builder input = %v", w)
	}
	if w := def.widget("l"); w == nil || w["type"] != "list" {
		t.Errorf("builder list = %v", w)
	}
	var dashes []map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.list()`, &dashes)
	b, _ := json.Marshal(dashes)
	if !strings.Contains(string(b), `"type":"input"`) || !strings.Contains(string(b), `"type":"list"`) {
		t.Errorf("listing = %s", b)
	}
}
