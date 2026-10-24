package web

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// The colour widget sends {r, g, b}; it reaches Lua as a table, not nil.
func TestActionValueLua(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	eval := func(v interface{}) lua.LValue {
		t.Helper()
		if err := L.DoString("return " + actionValueLua(v)); err != nil {
			t.Fatalf("%v: %v", v, err)
		}
		ret := L.Get(-1)
		L.Pop(1)
		return ret
	}

	rgb, ok := eval(map[string]interface{}{"r": float64(0), "g": float64(128), "b": float64(255)}).(*lua.LTable)
	if !ok {
		t.Fatal("{r, g, b} is not a table")
	}
	if rgb.RawGetString("r") != lua.LNumber(0) || rgb.RawGetString("g") != lua.LNumber(128) || rgb.RawGetString("b") != lua.LNumber(255) {
		t.Errorf("rgb r=%v g=%v b=%v", rgb.RawGetString("r"), rgb.RawGetString("g"), rgb.RawGetString("b"))
	}

	odd := eval(map[string]interface{}{`k"]=1--`: `v"\`, "nested": map[string]interface{}{"x": 1.0}, "list": []interface{}{1.0}}).(*lua.LTable)
	if odd.RawGetString(`k"]=1--`) != lua.LString(`v"\`) || odd.RawGetString("nested") != lua.LNil || odd.RawGetString("list") != lua.LNil {
		t.Errorf("quoted keys and values, nested left out: %v %v %v", odd.RawGetString(`k"]=1--`), odd.RawGetString("nested"), odd.RawGetString("list"))
	}

	for _, tc := range []struct {
		in   interface{}
		want lua.LValue
	}{
		{true, lua.LTrue}, {false, lua.LFalse}, {21.5, lua.LNumber(21.5)}, {"a\"b", lua.LString("a\"b")},
		{nil, lua.LNil}, {[]interface{}{1.0}, lua.LNil},
	} {
		if got := eval(tc.in); got != tc.want {
			t.Errorf("%#v -> %v, want %v", tc.in, got, tc.want)
		}
	}
}
