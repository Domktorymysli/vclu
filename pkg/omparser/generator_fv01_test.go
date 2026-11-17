package omparser

import (
	"strings"
	"testing"
)

// A ROLLER_SHUTTER (class 24) on fv01 firmware has no feature 7 Position: om.lua tells it apart
// by the settable feature 1 MaxTime, which fv02 and later do not have (#186).
const fv01OM = `CLU212345678 = OBJECT:new(0, 0xC0A80003)
-- NAME_CLU DOM20=CLU212345678
mm_400000001 = OBJECT:new(2, 0x17d78401, 0x28, "17d78401") -- 400000001 02 01
mm_330000001 = OBJECT:new(2, 0x13ab6681, 0x1e, "13ab6681") -- 330000001 02 01
ROL1005:set( 1 , 45000 ) -- before its object, with spaces
 ROL1001 = OBJECT:new(24, mm_400000001, 0)
-- NAME_IO STARA=ROL1001
 ROL1002 = OBJECT:new(24, mm_400000001, 1)
-- NAME_IO NOWA=ROL1002
 ROL1003 = OBJECT:new(24, mm_400000001, 2)
-- NAME_IO ZALUZJA=ROL1003
 ROL1004 = OBJECT:new(24, mm_400000001, 3)
-- NAME_IO RECZNA=ROL1004
 ROL1005 = OBJECT:new(24, mm_400000001, 4)
-- NAME_IO STRYCH=ROL1005
 DOU1001 = OBJECT:new(4, mm_330000001, 0)
-- NAME_IO LAMPA=DOU1001
ROL1001:set(1, 30000)
ROL1001:set(5, 0)
ROL1002:set(9, 30000)
ROL1002:set(10, 0)
ROL1003:set(9, 30000)
ROL1003:set(10, 1500)
DOU1001:set(1, 0)
`

func TestParseRollerFv01(t *testing.T) {
	parsed, err := Parse(fv01OM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	want := map[string]bool{"ROL1001": true, "ROL1002": false, "ROL1003": false, "ROL1004": false, "ROL1005": true, "DOU1001": false}
	for _, obj := range parsed.Objects {
		if obj.Fv01 != want[obj.ID] || parsed.ObjectMap[obj.ID].Fv01 != want[obj.ID] {
			t.Errorf("%s Fv01 = %v (map %v), want %v", obj.ID, obj.Fv01, parsed.ObjectMap[obj.ID].Fv01, want[obj.ID])
		}
	}
}

func TestGenerateProxyRollerFv01(t *testing.T) {
	parsed, err := Parse(fv01OM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	config := DefaultConfig()
	config.Namespace = "dom"
	out := GenerateProxy(parsed, config)
	for _, name := range []string{"STARA", "STRYCH"} {
		if strings.Contains(out, "dom."+name+"._hasPosition") || strings.Contains(out, "dom."+name+"._hasTilt") {
			t.Errorf("fv01 roller %s has a position or slats:\n%s", name, out)
		}
	}
	// fv02+ and a roller without set lines keep their position, the blind its slats
	for _, line := range []string{
		"dom.NOWA._hasPosition = true\n",
		"dom.ZALUZJA._hasPosition = true\n",
		"dom.ZALUZJA._hasTilt = true\n",
		"dom.RECZNA._hasPosition = true\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("missing %q:\n%s", line, out)
		}
	}
	if strings.Contains(out, "dom.NOWA._hasTilt") || strings.Contains(out, "dom.LAMPA._has") {
		t.Errorf("unexpected marks:\n%s", out)
	}
}
