package lua

import (
	"fmt"
	"strings"
	"testing"
)

// Table widget: rows from a function on every poll, in the columns of opts.columns; sort before
// limit, at most 200 rows sent, cells as numbers, text or booleans, a colour per cell, an error
// as the tile's error.

const dashTableSetup = `
	_dashboards = {}
	T = { rows = {
		{ pokoj = 'Salon', temp = 21.46, okno = false, wilg = 48 },
		{ pokoj = 'Kuchnia', temp = '19.04', okno = true },             -- a Remote value as text
		{ pokoj = 'Łazienka', wilg = 61, okno = false },                -- no temperature
		{ pokoj = 'Biuro', temp = 19.04, okno = false, extra = 'x' },   -- a key without a column
		{ pokoj = 'Strych', temp = 0/0 },                               -- NaN is no value
	} }
	local function rows()
		if T.fail then error('rows gone') end
		return T.rows
	end
	Dashboard.new('tb')
		:section('Rooms', 'house')
			:table('rooms', 'Pokoje', rows, { columns = {
				{ key = 'pokoj', label = 'Pokój' },
				{ key = 'temp', label = 'Temp.', unit = '°C', decimals = 1, align = 'right' },
				{ key = 'okno' },
			} })
			:table('sorted', 'Sorted', rows, { sort = 'temp', limit = 3, span = 3, icon = 'thermometer',
				disabled = function() return T.off == true end,
				columns = { { key = 'pokoj' }, { key = 'temp', decimals = 1 }, { key = 'wilg' } },
				color = function(row, key)
					if key == 'temp' and type(row.temp) == 'number' and row.temp > 20 then return 'danger' end
					if key == 'pokoj' then return 'red; background: url(x)' end
				end })
	:build()
`

func newTableEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashTableSetup)
	return engine
}

func tableValues(t *testing.T, engine *LuaEngine, dash string) map[string]map[string]interface{} {
	t.Helper()
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("`+dash+`")`, &vals)
	return vals
}

// cells of a row; an empty row comes as [] (no keys)
func tableCells(row map[string]interface{}) map[string]interface{} {
	v, _ := row["v"].(map[string]interface{})
	return v
}

func TestDashboardTableDefinition(t *testing.T) {
	engine := newTableEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("tb")`, &def)

	w := def.widget("rooms")
	if w["type"] != "table" || w["icon"] != "table" || w["label"] != "Pokoje" || w["total"] != float64(5) {
		t.Fatalf("rooms = %v", w)
	}
	if _, ok := w["sort"]; ok {
		t.Errorf("rooms has a sort: %v", w["sort"])
	}
	cols := list(w["columns"])
	if len(cols) != 3 || cols[0]["label"] != "Pokój" || cols[1]["unit"] != "°C" || cols[1]["decimals"] != float64(1) ||
		cols[1]["align"] != "right" || cols[2]["key"] != "okno" || cols[2]["label"] != "okno" {
		t.Errorf("columns = %v", cols)
	}
	// in the order rows gives; text in a column with decimals is a number; only the columns' keys
	rows := list(w["rows"])
	if len(rows) != 5 {
		t.Fatalf("rows = %v", rows)
	}
	want := []map[string]interface{}{
		{"pokoj": "Salon", "temp": 21.46, "okno": false},
		{"pokoj": "Kuchnia", "temp": 19.04, "okno": true},
		{"pokoj": "Łazienka", "okno": false},
		{"pokoj": "Biuro", "temp": 19.04, "okno": false},
		{"pokoj": "Strych"},
	}
	for i, wr := range want {
		got := tableCells(rows[i])
		if len(got) != len(wr) {
			t.Errorf("row %d = %v, want %v", i, got, wr)
			continue
		}
		for k, v := range wr {
			if got[k] != v {
				t.Errorf("row %d %s = %v, want %v", i, k, got[k], v)
			}
		}
		if _, ok := rows[i]["c"]; ok {
			t.Errorf("row %d has colours without opts.color: %v", i, rows[i])
		}
	}
}

// sort is ascending and stable, empty cells last; limit cuts after the sort
func TestDashboardTableSortLimitColor(t *testing.T) {
	engine := newTableEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("tb")`, &def)
	w := def.widget("sorted")
	if w["sort"] != "temp" || w["span"] != float64(3) || w["icon"] != "thermometer" || w["total"] != float64(5) {
		t.Errorf("sorted = %v", w)
	}
	rows := list(w["rows"])
	var names []string
	for _, r := range rows {
		names = append(names, fmt.Sprint(tableCells(r)["pokoj"]))
	}
	if got := strings.Join(names, ","); got != "Kuchnia,Biuro,Salon" {
		t.Errorf("sorted rows = %s", got)
	}
	// a colour the page can use; one that is not a colour is dropped
	if c, _ := rows[2]["c"].(map[string]interface{}); len(c) != 1 || c["temp"] != "danger" {
		t.Errorf("Salon colours = %v", rows[2]["c"])
	}
	if _, ok := rows[0]["c"]; ok {
		t.Errorf("Kuchnia colours = %v", rows[0]["c"])
	}

	// a poll reads the rows again
	mustExec(t, engine, `T.rows = { { pokoj = 'A', temp = 30 }, { pokoj = 'B', temp = 25 } }; T.off = true`)
	v := tableValues(t, engine, "tb")["sorted"]
	rows = list(v["rows"])
	if len(rows) != 2 || tableCells(rows[0])["pokoj"] != "B" || v["total"] != float64(2) || v["disabled"] != true {
		t.Errorf("sorted after a change = %v", v)
	}
}

// Mixed types sort numbers, text, booleans, then empty; NaN counts as empty
func TestDashboardTableSortMixed(t *testing.T) {
	engine := newTableEngine(t)
	mustExec(t, engine, `
		Dashboard.new('mix'):section('S', 'grid')
			:table('m', 'M', function()
				return { { n = 'nil' }, { n = 'true', x = true }, { n = 'b', x = 'b' }, { n = 'two', x = 2 },
					{ n = 'false', x = false }, { n = 'a', x = 'a' }, { n = 'nan', x = 0/0 }, { n = 'one', x = 1 } }
			end, { sort = 'x', columns = { { key = 'n' }, { key = 'x' } } })
		:build()`)
	rows := list(tableValues(t, engine, "mix")["m"]["rows"])
	var names []string
	for _, r := range rows {
		names = append(names, fmt.Sprint(tableCells(r)["n"]))
	}
	if got := strings.Join(names, ","); got != "one,two,a,b,false,true,nil,nan" {
		t.Errorf("mixed sort = %s", got)
	}
}

// desc with limit keeps the highest rows; empty cells stay last, equal cells in their order
func TestDashboardTableSortDesc(t *testing.T) {
	engine := newTableEngine(t)
	mustExec(t, engine, `
		Dashboard.new('desc'):section('S', 'grid')
			:table('top', 'Top', function()
				return { { n = 'none' }, { n = 'p5', p = 5 }, { n = 'p9', p = 9 }, { n = 'p1', p = 1 },
					{ n = 'p9b', p = 9 }, { n = 'nan', p = 0/0 }, { n = 'p7', p = '7' } }
			end, { sort = 'p', desc = true, limit = 3, columns = { { key = 'n' }, { key = 'p', decimals = 0 } } })
			:table('all', 'All', function()
				return { { n = 'none' }, { n = 'p5', p = 5 }, { n = 'p9', p = 9 } }
			end, { sort = 'p', desc = true, columns = { { key = 'n' }, { key = 'p' } } })
		:build()`)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("desc")`, &def)
	for id, want := range map[string]string{"top": "p9,p9b,p7", "all": "p9,p5,none"} {
		w := def.widget(id)
		var names []string
		for _, r := range list(w["rows"]) {
			names = append(names, fmt.Sprint(tableCells(r)["n"]))
		}
		if got := strings.Join(names, ","); got != want || w["sort"] != "p" || w["desc"] != true {
			t.Errorf("%s: rows %s (want %s), sort %v, desc %v", id, got, want, w["sort"], w["desc"])
		}
	}
}

// nil and {} are no data; at most 200 rows go to the page whatever limit says
func TestDashboardTableEmptyAndCap(t *testing.T) {
	engine := newTableEngine(t)
	mustExec(t, engine, `
		local many = {}
		for i = 1, 250 do many[i] = { n = i } end
		Dashboard.new('cap'):section('S', 'grid')
			:table('none', 'N', function() return nil end, { columns = { { key = 'n' } } })
			:table('empty', 'E', function() return {} end, { columns = { { key = 'n' } } })
			:table('many', 'M', function() return many end, { columns = { { key = 'n' } } })
			:table('limited', 'L', function() return many end, { limit = 200, sort = 'n', columns = { { key = 'n' } } })
		:build()`)
	vals := tableValues(t, engine, "cap")
	for _, id := range []string{"none", "empty"} {
		v := vals[id]
		if rows, ok := v["rows"].([]interface{}); !ok || len(rows) != 0 || v["total"] != float64(0) || v["error"] != nil {
			t.Errorf("%s = %v", id, v)
		}
	}
	for _, id := range []string{"many", "limited"} {
		v := vals[id]
		rows := list(v["rows"])
		if len(rows) != 200 || v["total"] != float64(250) || tableCells(rows[199])["n"] != float64(200) {
			t.Errorf("%s: %d rows, total %v", id, len(rows), v["total"])
		}
	}
}

// An error in rows, a result that is not a list of tables or a cell that is not a value is the tile's error
func TestDashboardTableErrors(t *testing.T) {
	engine := newTableEngine(t)
	mustExec(t, engine, `T.fail = true`)
	v := tableValues(t, engine, "tb")["rooms"]
	if e, _ := v["error"].(string); !strings.Contains(e, "rows gone") || v["rows"] != nil {
		t.Errorf("rooms after an error = %v", v)
	}
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("tb")`, &def)
	if e, _ := def.widget("rooms")["error"].(string); !strings.Contains(e, "rows gone") {
		t.Errorf("definition after an error = %v", def.widget("rooms"))
	}

	cases := map[string]string{
		`'many'`:                              "rows must return a list of tables, got string",
		`{ 'Salon' }`:                         "row 1 must be a table, got string",
		`{ { pokoj = 'A' }, { pokoj = {} } }`: `row 2 "pokoj" must be a number, text or boolean, got table`,
		`{ { okno = print } }`:                `row 1 "okno" must be a number, text or boolean, got function`,
	}
	for rows, want := range cases {
		mustExec(t, engine, `T.fail = false; T.rows = `+rows)
		e, _ := tableValues(t, engine, "tb")["rooms"]["error"].(string)
		if !strings.Contains(e, `section:table("rooms")`) || !strings.Contains(e, want) {
			t.Errorf("rows %s: error %q, want %q", rows, e, want)
		}
	}
}

// A row that cannot be read is the tile's error; the dashboard's other widgets still get values
func TestDashboardTableRowReadError(t *testing.T) {
	engine := newTableEngine(t)
	mustExec(t, engine, `
		local proxy = setmetatable({}, { __index = function(_, k) error('no field ' .. k) end })
		Dashboard.new('px'):section('S', 'grid')
			:table('t', 'T', function() return { { a = 1 }, proxy } end, { columns = { { key = 'a' } } })
			:status('s', 'S', 'info', function() return 42 end)
		:build()`)
	vals := tableValues(t, engine, "px")
	if e, _ := vals["t"]["error"].(string); !strings.Contains(e, "no field a") || vals["t"]["rows"] != nil {
		t.Errorf("t = %v", vals["t"])
	}
	if vals["s"]["value"] != float64(42) {
		t.Errorf("s = %v", vals["s"])
	}
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("px")`, &def)
	if e, _ := def.widget("t")["error"].(string); !strings.Contains(e, "no field a") || def.widget("s")["value"] != float64(42) {
		t.Errorf("definition: t = %v, s = %v", def.widget("t"), def.widget("s"))
	}
}

// A map or a list with holes is the tile's error, not "no data" or rows cut without a word
func TestDashboardTableNotAList(t *testing.T) {
	engine := newTableEngine(t)
	for rows, want := range map[string]string{
		`{ kuchnia = { pokoj = 'Kuchnia' }, salon = { pokoj = 'Salon' } }`: "got 2 keys and 0 in a row",
		`{ { pokoj = 'A' }, nil, { pokoj = 'C' } }`:                        "got 2 keys and 1 in a row",
		`{ { pokoj = 'A' }, extra = { pokoj = 'B' } }`:                     "got 2 keys and 1 in a row",
	} {
		mustExec(t, engine, `T.rows = `+rows)
		v := tableValues(t, engine, "tb")["rooms"]
		if e, _ := v["error"].(string); !strings.Contains(e, `section:table("rooms"): rows must return a list`) || !strings.Contains(e, want) {
			t.Errorf("rows %s: %v", rows, v)
		}
	}
}

// Text sorts as on the page: case does not matter, digit runs as numbers; hex text is not a number
func TestDashboardTableTextSortAndHex(t *testing.T) {
	engine := newTableEngine(t)
	mustExec(t, engine, `
		Dashboard.new('txt'):section('S', 'grid')
			:table('t', 'T', function()
				return { { n = 'room 10' }, { n = 'Room 2' }, { n = 'attic' }, { n = 'Bath' }, { n = 'room 1' } }
			end, { sort = 'n', columns = { { key = 'n' } } })
			:table('hex', 'H', function() return { { x = '0x1F' }, { x = ' 21.5 ' }, { x = '-3' }, { x = '1e3' }, { x = 'inf' } } end,
				{ columns = { { key = 'x', decimals = 1 } } })
		:build()`)
	vals := tableValues(t, engine, "txt")
	var names []string
	for _, r := range list(vals["t"]["rows"]) {
		names = append(names, fmt.Sprint(tableCells(r)["n"]))
	}
	if got := strings.Join(names, ","); got != "attic,Bath,room 1,Room 2,room 10" {
		t.Errorf("text sort = %s", got)
	}
	want := []interface{}{"0x1F", 21.5, float64(-3), "1e3", "inf"}
	for i, r := range list(vals["hex"]["rows"]) {
		if got := tableCells(r)["x"]; got != want[i] {
			t.Errorf("hex row %d = %#v, want %#v", i, got, want[i])
		}
	}
}

// At most 1000 rows are read and sorted; total still counts them all
func TestDashboardTableInputCap(t *testing.T) {
	engine := newTableEngine(t)
	mustExec(t, engine, `
		local many = {}
		for i = 1, 1500 do many[i] = { n = i } end
		Dashboard.new('in'):section('S', 'grid')
			:table('t', 'T', function() return many end, { sort = 'n', desc = true, limit = 2, columns = { { key = 'n' } } })
		:build()`)
	v := tableValues(t, engine, "in")["t"]
	rows := list(v["rows"])
	if len(rows) != 2 || tableCells(rows[0])["n"] != float64(1000) || v["total"] != float64(1500) {
		t.Errorf("t = %d rows from %v, total %v", len(rows), rows, v["total"])
	}
}

// A failing colour function leaves the cells without colour and logs once
func TestDashboardTableColorError(t *testing.T) {
	engine := newTableEngine(t)
	mustExec(t, engine, `
		C = { calls = 0 }
		Dashboard.new('ce'):section('S', 'grid')
			:table('t', 'T', function() return { { a = 1 }, { a = 2 } } end, { columns = { { key = 'a' } },
				color = function(row) C.calls = C.calls + 1; if row.a == 1 then error('no colour') end; return 'success' end })
		:build()`)
	rows := list(tableValues(t, engine, "ce")["t"]["rows"])
	if len(rows) != 2 || rows[0]["c"] != nil {
		t.Fatalf("rows = %v", rows)
	}
	if c, _ := rows[1]["c"].(map[string]interface{}); c["a"] != "success" {
		t.Errorf("row 2 colours = %v", rows[1]["c"])
	}
	if got := mustExec(t, engine, `return tostring(C.calls)`); got != "2" {
		t.Errorf("colour calls = %s", got)
	}
}

// Wrong arguments fail at the module's line and name section:table
func TestDashboardTableArgumentErrors(t *testing.T) {
	fn := `function() return {} end`
	cols := `columns = { { key = 'a' } }`
	cases := []struct{ call, want string }{
		{`:section('S', 'g'):table('', 'L', ` + fn + `, { ` + cols + ` })`, "id must be"},
		{`:section('S', 'g'):table('a', nil, ` + fn + `, { ` + cols + ` })`, "label must be"},
		{`:section('S', 'g'):table('a', 'L', {}, { ` + cols + ` })`, "rows must be a function"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `)`, "opts with columns is required"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, {})`, "opts.columns must be a list"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { columns = {} })`, "opts.columns must be a list"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { ` + cols + `, limits = 3 })`, `unknown option "limits"`},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { columns = { 'a' } })`, "column 1 must be a table"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { columns = { { key = 'a', title = 'A' } } })`, `column 1: unknown key "title"`},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { columns = { { label = 'A' } } })`, "column 1: key must be"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { columns = { { key = 'a' }, { key = 'a' } } })`, `column 2: key "a" is used twice`},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { columns = { { key = 'a', label = 1 } } })`, "label must be a string"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { columns = { { key = 'a', unit = 3 } } })`, "unit must be"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { columns = { { key = 'a', decimals = 7 } } })`, "decimals must be a whole number 0-6"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { columns = { { key = 'a', decimals = 1.5 } } })`, "decimals must be"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { columns = { { key = 'a', align = 'center' } } })`, `align must be "left" or "right"`},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { ` + cols + `, sort = 'b' })`, "opts.sort must be the key of a column"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { ` + cols + `, sort = 'a', desc = 'yes' })`, "opts.desc must be a boolean"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { ` + cols + `, desc = true })`, "opts.desc needs opts.sort"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { ` + cols + `, limit = 0 })`, "opts.limit must be a whole number 1-200"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { ` + cols + `, limit = 201 })`, "opts.limit"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { ` + cols + `, color = 'danger' })`, "opts.color must be a function"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { ` + cols + `, icon = 'x y' })`, "opts.icon"},
		{`:section('S', 'g'):table('a', 'L', ` + fn + `, { ` + cols + `, disabled = true })`, "opts.disabled"},
		{`:header():table('a', 'L', ` + fn + `, { ` + cols + ` })`, "not the header"},
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
		if !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, "section:table") || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %s", c.call, msg)
		}
	}
	// the builder's own method too
	engine := NewLuaEngine()
	defer engine.Close()
	_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  local b = Dashboard.new('f'); b:section('S', 'g')\n  b:table('a', 'L', " + fn + ", { columns = { { key = 'a', decimals = 9 } } })\nend)\nerror(err, 0)")
	if err == nil || !strings.Contains(err.Error(), "<string>:4: ") || !strings.Contains(err.Error(), "section:table") {
		t.Errorf("builder table: %v", err)
	}
	// and it builds
	mustExec(t, engine, "_dashboards = {}\nlocal b = Dashboard.new('g'); b:section('S', 'g')\nb:table('a', 'L', "+fn+", { "+cols+" }):build()")
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("g")`, &def)
	if w := def.widget("a"); w == nil || w["type"] != "table" {
		t.Errorf("builder table definition = %v", w)
	}
}
