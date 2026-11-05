package omparser

import (
	"strings"
	"testing"
)

// A ROLLER_SHUTTER (class 24) has slats when om.lua sets its LamelMoveTimeout (feature 10)
// above 0; Object Manager writes 0 for a roller without them (#175).
const tiltOM = `CLU212345678 = OBJECT:new(0, 0xC0A80003)
-- NAME_CLU DOM20=CLU212345678
mm_400000001 = OBJECT:new(2, 0x17d78401, 0x28, "17d78401") -- 400000001 02 01
mm_330000001 = OBJECT:new(2, 0x13ab6681, 0x1e, "13ab6681") -- 330000001 02 01
ROL1001:set(10, 1500)
 ROL1001 = OBJECT:new(24, mm_400000001, 0)
-- NAME_IO SALON_ZALUZJA=ROL1001
 ROL1002 = OBJECT:new(24, mm_400000001, 1)
-- NAME_IO GARAZ=ROL1002
 ROL1003 = OBJECT:new(24, mm_400000001, 2)
-- NAME_IO KUCHNIA=ROL1003
 ROL2001 = OBJECT:new(11, mm_400000001, 3)
-- NAME_IO STRYCH=ROL2001
 DOU1001 = OBJECT:new(4, mm_330000001, 0)
-- NAME_IO LAMPA=DOU1001
ROL1002:set(10, 0)
ROL2001:set(10, 1500)
DOU1001:set(10, 1500)
`

func TestParseRollerTilt(t *testing.T) {
	parsed, err := Parse(tiltOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	want := map[string]bool{"ROL1001": true, "ROL1002": false, "ROL1003": false, "ROL2001": false, "DOU1001": false}
	for _, obj := range parsed.Objects {
		if obj.HasTilt != want[obj.ID] {
			t.Errorf("%s HasTilt = %v, want %v", obj.ID, obj.HasTilt, want[obj.ID])
		}
		if parsed.ObjectMap[obj.ID].HasTilt != want[obj.ID] {
			t.Errorf("ObjectMap %s HasTilt = %v", obj.ID, parsed.ObjectMap[obj.ID].HasTilt)
		}
	}
}

func TestGenerateProxyRollerTilt(t *testing.T) {
	parsed, err := Parse(tiltOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	config := DefaultConfig()
	config.Namespace = "dom"
	out := GenerateProxy(parsed, config)
	if !strings.Contains(out, "dom.SALON_ZALUZJA._hasTilt = true\n") {
		t.Errorf("blind not marked with a tilt:\n%s", out)
	}
	for _, name := range []string{"GARAZ", "KUCHNIA", "STRYCH", "LAMPA"} {
		if strings.Contains(out, "dom."+name+"._hasTilt") {
			t.Errorf("%s marked with a tilt:\n%s", name, out)
		}
	}
}

func TestHADiscoveryRollerTilt(t *testing.T) {
	parsed, err := Parse(tiltOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	payloads := map[string]map[string]interface{}{}
	for _, m := range GenerateHADiscovery(parsed, DefaultHAConfig()) {
		payloads[m.ObjectPath] = m.Payload
	}

	blind := payloads["CLU212345678.ROL1001"]
	for k, v := range map[string]interface{}{
		"tilt_status_topic":  "vclu/clu212345678/cover/rol1001/tilt",
		"tilt_command_topic": "vclu/clu212345678/cover/rol1001/tilt/set",
		"tilt_min":           0, "tilt_max": 100, "tilt_closed_value": 0, "tilt_opened_value": 100,
		"unique_id": "vclu_clu212345678_rol1001",
	} {
		if blind[k] != v {
			t.Errorf("blind %s = %v, want %v", k, blind[k], v)
		}
	}
	for _, path := range []string{"CLU212345678.ROL1002", "CLU212345678.ROL1003", "CLU212345678.ROL2001"} {
		p, ok := payloads[path]
		if !ok {
			t.Fatalf("no discovery for %s", path)
		}
		for k := range p {
			if strings.HasPrefix(k, "tilt") {
				t.Errorf("%s has %s", path, k)
			}
		}
	}
}
