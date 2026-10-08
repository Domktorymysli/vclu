package lua

import (
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// A cyclic table used to overflow the Go stack in json.encode / KV set,
// which kills the whole process; now it is an ordinary error.
func TestLuaToGo_Limits(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	cases := map[string]string{
		`local t = {} t.self = t return t`:                               "cyclic",
		`local t = {} t[1] = { t } return t`:                             "cyclic",
		`local t = {} for i = 1, 100 do t = { t } end return t`:          "nested deeper",
		`local t = {} for i = 1, 1000001 do t[i] = i end return t`:       "more than",
		`local a = {} for i = 1, 600000 do a[i] = 1 end return { a, a }`: "more than",
	}
	for src, want := range cases {
		if err := L.DoString(src); err != nil {
			t.Fatal(err)
		}
		v := L.Get(-1)
		L.Pop(1)
		if _, err := LuaToGo(v); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", src, err, want)
		}
	}
}

func TestLuaToGo_Shapes(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	if err := L.DoString(`
		local shared = { x = 1 }
		return { list = { "a", "b" }, holes = { [1] = 1, [3] = 3 }, sparse = { [1e9] = true },
		         twice = { shared, shared }, n = 2.5, i = 7 }
	`); err != nil {
		t.Fatal(err)
	}
	v, err := LuaToGo(L.Get(-1))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]interface{})
	if l := m["list"].([]interface{}); len(l) != 2 || l[0] != "a" {
		t.Errorf("list = %v", m["list"])
	}
	if h := m["holes"].([]interface{}); len(h) != 3 || h[1] != nil {
		t.Errorf("holes = %v", m["holes"])
	}
	if _, ok := m["sparse"].(map[string]interface{}); !ok {
		t.Errorf("sparse must stay a map, got %T", m["sparse"])
	}
	if tw := m["twice"].([]interface{}); len(tw) != 2 {
		t.Errorf("a table shared twice is not a cycle: %v", m["twice"])
	}
	if m["n"] != 2.5 || m["i"] != int64(7) {
		t.Errorf("numbers: n=%v i=%v", m["n"], m["i"])
	}
}

func TestJSONEncode_CyclicReturnsError(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	RegisterParserFunctions(L)
	if err := L.DoString(`
		local t = {} t.self = t
		_s, _err = json.encode(t)
	`); err != nil {
		t.Fatal(err)
	}
	if L.GetGlobal("_s") != lua.LNil || !strings.Contains(L.GetGlobal("_err").String(), "cyclic") {
		t.Fatalf("json.encode(cyclic) = %v, %v", L.GetGlobal("_s"), L.GetGlobal("_err"))
	}
}

// A whole number stored in KV came back as a string ("5") until restart.
func TestGoValueToLua_RoundTrip(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	if err := L.DoString(`return { n = 5, f = 2.5, list = { 1, 2 }, s = "x", b = true }`); err != nil {
		t.Fatal(err)
	}
	v, err := LuaToGo(L.Get(-1))
	if err != nil {
		t.Fatal(err)
	}
	L.SetGlobal("_v", goValueToLua(L, v))
	if err := L.DoString(`
		assert(type(_v.n) == "number" and _v.n == 5, "n=" .. type(_v.n))
		assert(_v.f == 2.5 and _v.list[2] == 2 and _v.s == "x" and _v.b == true)
	`); err != nil {
		t.Fatal(err)
	}
}
