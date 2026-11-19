package omchange

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/omparser"
)

const omLua = `CLU221000001 = OBJECT:new(0, 0x0A00000C, "CLU221000001")
-- NAME_CLU Parter=CLU221000001
mm_1 = OBJECT:new(2, 0x13ab66e6, 0x1e, "13ab66e6")
 DOU0001 = OBJECT:new(4, mm_1, 0, "DOU0001")
-- NAME_IO Salon=DOU0001
 DOU0002 = OBJECT:new(4, mm_1, 1, "DOU0002")
 DIN0001 = OBJECT:new(3, mm_1, 0, "DIN0001")
`

func house(t *testing.T, texts ...string) House {
	t.Helper()
	h := House{}
	for _, text := range texts {
		p, err := omparser.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		h.Add(p)
	}
	return h
}

func TestHas(t *testing.T) {
	h := house(t, omLua)
	for path, want := range map[string]bool{
		"CLU221000001.DOU0001": true, "CLU221000001.Salon": true, "parter.Salon": true, "parter.DOU0002": true,
		"parter.DOU0001": false, "CLU221000001.DOU0009": false, "dom.Salon": false,
	} {
		if h.Has(path) != want {
			t.Errorf("%s: %v", path, !want)
		}
	}
}

// Objects that go, come and change type, and a CLU renamed (its table with it).
func TestDiff(t *testing.T) {
	before := house(t, omLua, `CLU221000002 = OBJECT:new(0, 0x0A00000D, "CLU221000002")
`)
	after := house(t, `CLU221000001 = OBJECT:new(0, 0x0A00000C, "CLU221000001")
-- NAME_CLU Pietro=CLU221000001
mm_1 = OBJECT:new(2, 0x13ab66e6, 0x1e, "13ab66e6")
 DOU0001 = OBJECT:new(4, mm_1, 0, "DOU0001")
-- NAME_IO Salon=DOU0001
 DIN0001 = OBJECT:new(4, mm_1, 3, "DIN0001")
 DOU0003 = OBJECT:new(4, mm_1, 2, "DOU0003")
`, `CLU221000003 = OBJECT:new(0, 0x0A00000E, "CLU221000003")
`)
	got := Diff(before, after)
	if len(got) != 3 || got[0].ID != "CLU221000001" || got[1].Kind != "removed" || got[2].Kind != "new" {
		t.Fatalf("%+v", got)
	}
	ch := got[0]
	if ch.Kind != "changed" || ch.Renamed != "Parter" || ch.Added != 1 ||
		!reflect.DeepEqual(ch.Removed, []Object{{ID: "DOU0002", Type: "DOUT"}}) ||
		len(ch.Retyped) != 1 || ch.Retyped[0].ID != "DIN0001" || ch.Retyped[0].From != "DIN" || ch.Retyped[0].To != "DOUT" {
		t.Errorf("%+v", ch)
	}
	if d := Diff(before, before); d[0].Kind != "same" || d[1].Kind != "same" {
		t.Errorf("same: %+v", d)
	}
}

// Refs in code and strings, by ID, by name and through the CLU's table; not in comments, not
// as part of another name; the lines stay where they are after a block comment.
func TestRefs(t *testing.T) {
	dir := t.TempDir()
	code := `local r = _registry
--[[ r:get("CLU221000001.DOU0002")
]]
r:get("CLU221000001.DOU0001"):switchOn() -- parter.DOU0002
parter.Salon:switchOff(); x.parter.DOU0002 = 1; my_parter.DOU0002 = 1
local s = "--" .. "CLU221000001.Gone" print(CLU221000001._omFirmware, CLU999.DOU0001)
`
	if err := os.MkdirAll(filepath.Join(dir, "dashboards"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dashboards", "a.lua"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("CLU221000001.DOU0002"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := house(t, omLua)
	refs, err := Refs(dir, h)
	if err != nil {
		t.Fatal(err)
	}
	want := []Ref{
		{"dashboards/a.lua", 4, "CLU221000001.DOU0001"},
		{"dashboards/a.lua", 5, "parter.Salon"},
		{"dashboards/a.lua", 6, "CLU221000001.Gone"},
	}
	if !reflect.DeepEqual(refs, want) {
		t.Errorf("refs\n%v\nwant\n%v", refs, want)
	}
	if m := Missing(refs, h); len(m) != 1 || m[0].Path != "CLU221000001.Gone" {
		t.Errorf("missing %v", m)
	}
	if refs, err := Refs(filepath.Join(dir, "none"), h); err != nil || len(refs) != 0 {
		t.Errorf("no directory: %v %v", refs, err)
	}
}

// A table after "..", and a table's name the file declares itself (local, parameter, loop).
func TestRefsConcatAndLocals(t *testing.T) {
	dir := t.TempDir()
	write := func(name, code string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(code), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.lua", `print("x"..parter.Salon:get(0), "CLU"..CLU221000001.DOU0001)
`)
	write("b.lua", `local parter = {}
parter.Gone = 1
`)
	write("c.lua", `function f(a, parter) return parter.Gone end
for parter in pairs(t) do print(parter.Gone) end
`)
	refs, err := Refs(dir, house(t, omLua))
	if err != nil {
		t.Fatal(err)
	}
	want := []Ref{{"a.lua", 1, "CLU221000001.DOU0001"}, {"a.lua", 1, "parter.Salon"}}
	if !reflect.DeepEqual(refs, want) {
		t.Errorf("refs\n%v\nwant\n%v", refs, want)
	}
}

// Comments go, strings and long strings stay, and every byte keeps its place, however odd the
// code: escaped quotes, levels of long brackets, "--" in strings, things left open.
func TestWithoutComments(t *testing.T) {
	for in, want := range map[string]string{
		`a = "x\"--y" -- c`:             `a = "x\"--y"     `,
		`a = 'it''s' --c`:               `a = 'it''s'    `,
		"a = [==[ ]] -- ]==] b -- c":    "a = [==[ ]] -- ]==] b     ",
		"--[==[ x\n]] y ]==] z":         "        \n          z",
		"--[[ open\nstill":              "         \n     ",
		"s = [[ open\n-- in the string": "s = [[ open\n-- in the string",
		`s = "open -- not closed`:       `s = "open -- not closed`,
		"s = \"a\\\n-- b\" -- c":        "s = \"a\\\n-- b\"     ",
		`s = "\\" -- c`:                 `s = "\\"     `,
		"x = 1 --[ not long\ny":         "x = 1             \ny",
		`ends with \`:                   `ends with \`,
		`"\`:                            `"\`,
		`--`:                            `  `,
		`t[ [[x]] ] = a[b[1]] -- c`:     `t[ [[x]] ] = a[b[1]]     `,
	} {
		got := withoutComments(in)
		if got != want {
			t.Errorf("%q:\n got %q\nwant %q", in, got, want)
		}
		if len(got) != len(in) || strings.Count(got, "\n") != strings.Count(in, "\n") {
			t.Errorf("%q: length or lines changed", in)
		}
	}
}

func FuzzWithoutComments(f *testing.F) {
	for _, s := range []string{`a = "x" -- c`, "--[==[\n]==]", `[[`, `"\`, "--[", "[=[ ]=]"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := withoutComments(in)
		if len(got) != len(in) {
			t.Fatalf("%q: length %d", in, len(got))
		}
		for i := range in {
			if (in[i] == '\n') != (got[i] == '\n') {
				t.Fatalf("%q: line moved at %d", in, i)
			}
		}
	})
}
