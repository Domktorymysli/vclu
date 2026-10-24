package omparser

import (
	"strings"
	"testing"
)

// om.lua snippet with both thermometer flavours:
//   - ONE_WIRE (class 23): module-based, same shape as regular IO
//   - THE (class 18): peripheral, attached directly to the CLU (no module/index)
const thermometerOM = `-- FwType 00000003
-- FwVersion 000001f6
-- HwType 00000013
-- HwVersion 00000001

CLU220000592 = OBJECT:new(0, 0xC0A80004)
-- NAME_CLU DOM=CLU220000592

mm_175250704 = OBJECT:new(2, 0x0a73a890, 0x1e, "0a73a890")

 ONE3544 = OBJECT:new(23, mm_175250704, 0, "ONE3544")
-- NAME_IO Kuchnia_Temp=ONE3544
 DOU5048 = OBJECT:new(4, mm_175250704, 1, "DOU5048")

THE0855 = OBJECT:new(18, "THE0855")
-- NAME_PERIPHERY aSalon=THE0855
THE4346 = OBJECT:new(18, "THE4346")
-- NAME_PERIPHERY aSypialnia=THE4346
`

func TestParseThermometers(t *testing.T) {
	parsed, err := Parse(thermometerOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	oneWire := parsed.GetObjectsByClass(ClassONE_WIRE)
	if len(oneWire) != 1 {
		t.Fatalf("expected 1 ONE_WIRE object, got %d", len(oneWire))
	}
	if oneWire[0].ID != "ONE3544" {
		t.Errorf("ONE_WIRE id = %q, want ONE3544", oneWire[0].ID)
	}
	if oneWire[0].ModuleID != "mm_175250704" {
		t.Errorf("ONE_WIRE module = %q, want mm_175250704", oneWire[0].ModuleID)
	}
	if oneWire[0].Name != "Kuchnia_Temp" {
		t.Errorf("ONE_WIRE name = %q, want Kuchnia_Temp", oneWire[0].Name)
	}

	// THE objects (class 18) are thermostats, not plain thermometers. They become
	// RemoteThermostat objects, not auto-exposed (a climate, exposed by expose() only).
	thermostats := parsed.GetObjectsByClass(ClassThermostat)
	if len(thermostats) != 2 {
		t.Fatalf("expected 2 thermostats, got %d", len(thermostats))
	}
	if thermostats[0].ModuleID != "" {
		t.Errorf("thermostat should have no module, got %q", thermostats[0].ModuleID)
	}
	if thermostats[0].Name != "aSalon" {
		t.Errorf("thermostat name = %q, want aSalon", thermostats[0].Name)
	}

	// ONE_WIRE is a real temperature sensor and must reach the expose layer.
	if !ClassONE_WIRE.IsExposable() {
		t.Error("ClassONE_WIRE should be exposable")
	}
	// Thermostat must NOT be exposed as a plain sensor (would misread index 0).
	if ClassThermostat.IsExposable() {
		t.Error("ClassThermostat should not be exposable yet")
	}
}

func TestGenerateThermometerProxy(t *testing.T) {
	parsed, err := Parse(thermometerOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	code := GenerateProxy(parsed, DefaultConfig())

	// ONE_WIRE sensor must produce a RemoteThermometer proxy and be registered.
	if !strings.Contains(code, `RemoteThermometer:new(_clu, "ONE3544"`) {
		t.Error("generated proxy missing RemoteThermometer for ONE3544")
	}
	if !strings.Contains(code, `registerObject("CLU220000592.ONE3544"`) {
		t.Error("generated proxy missing registerObject for ONE3544")
	}

	// Thermostats (THE) must NOT be emitted as thermometers (avoid misreading index 0).
	for _, id := range []string{"THE0855", "THE4346"} {
		if strings.Contains(code, `RemoteThermometer:new(_clu, "`+id+`"`) {
			t.Errorf("thermostat %s should not be generated as RemoteThermometer", id)
		}
	}
	// They are RemoteThermostat objects, in the registry for the Sync list and subscriptions
	for _, want := range []string{
		`.aSalon = RemoteThermostat:new(_clu, "THE0855", "aSalon")`,
		`registerObject("CLU220000592.THE0855", `,
		`registerObject("CLU220000592.THE4346", `,
	} {
		if !strings.Contains(code, want) {
			t.Errorf("generated proxy missing %s", want)
		}
	}
}

// Thermostat v1 (CLU firmware 197, 1f5) has no CurrentTemp: the import marks it.
func TestGenerateThermostatV1(t *testing.T) {
	for fw, want := range map[string]bool{"00000197": true, "000001f5": true, "000001f6": false, "00000198": false} {
		om := strings.Replace(thermometerOM, "-- FwVersion 000001f6", "-- FwVersion "+fw, 1)
		om = strings.Replace(om, "-- FwType ", "-- FwType 00000003\n-- was ", 1)
		parsed, err := Parse(om)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		parsed.CLU.FwType = "00000003"
		code := GenerateProxy(parsed, DefaultConfig())
		if got := strings.Contains(code, ".aSalon._version = 1"); got != want {
			t.Errorf("fw %s: _version = 1 is %v, want %v", fw, got, want)
		}
	}
}

// Smart Panel sensors: temperature (20), light (21), humidity (22). All module-based,
// Value at feature index 0, NAME_IO comments — same shape as regular IO.
const panelSensorOM = `CLU220000592 = OBJECT:new(0, 0xC0A80004)
mm_230000386 = OBJECT:new(2, 0x0db9e5c2, 0x1e, "0db9e5c2")

 PAN9127 = OBJECT:new(20, mm_230000386, 0, "PAN9127")
-- NAME_IO x230000386_PANELSENSTEMP1_WIATROLAP=PAN9127
 PAN3473 = OBJECT:new(21, mm_230000386, 0, "PAN3473")
-- NAME_IO x230000386_PANELSENSLIGHT1=PAN3473
 HUM1234 = OBJECT:new(22, mm_230000386, 0, "HUM1234")
-- NAME_IO x230000386_HUMIDITY1=HUM1234
 PBT5555 = OBJECT:new(30, mm_230000386, 0, "PBT5555")
-- NAME_IO x230000386_PANEL_BUTTON1=PBT5555
`

func TestParsePanelSensors(t *testing.T) {
	parsed, err := Parse(panelSensorOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	cases := []struct {
		class ObjectClass
		id    string
	}{
		{ClassPanelTemp, "PAN9127"},
		{ClassPanelLight, "PAN3473"},
		{ClassHumidity, "HUM1234"},
	}
	for _, c := range cases {
		objs := parsed.GetObjectsByClass(c.class)
		if len(objs) != 1 || objs[0].ID != c.id {
			t.Errorf("class %d: expected %s, got %+v", c.class, c.id, objs)
		}
		if !c.class.IsExposable() {
			t.Errorf("class %d (%s) should be exposable", c.class, c.id)
		}
	}

	// PANEL_BUTTON (30) is not a value sensor — parsed but not exposable.
	if ClassObjectByID(parsed, "PBT5555").IsExposable() {
		t.Error("PANEL_BUTTON should not be exposable")
	}

	code := GenerateProxy(parsed, DefaultConfig())
	expect := map[string]string{
		"PAN9127": "PANELSENSTEMP",
		"PAN3473": "PANELSENSLIGHT",
		"HUM1234": "HUMIDITY_SENSOR",
	}
	for id, typ := range expect {
		want := `RemoteValueSensor:new(_clu, "` + id + `"`
		if !strings.Contains(code, want) {
			t.Errorf("missing RemoteValueSensor proxy for %s", id)
		}
		if !strings.Contains(code, `, "`+typ+`")`) {
			t.Errorf("proxy for %s missing grenton type %s", id, typ)
		}
	}
}

func ClassObjectByID(p *ParsedOM, id string) ObjectClass {
	if obj, ok := p.ObjectMap[id]; ok {
		return obj.Class
	}
	return ObjectClass(-1)
}
