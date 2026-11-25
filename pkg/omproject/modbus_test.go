package omproject

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/omparser"
)

// modbusFeature is a feature of a peripheral as Object Manager keeps it in system.xml.
func modbusFeature(index int, value string) string {
	return fmt.Sprintf(`<EmbeddedFeature><name>f%d</name><accessType>ALL</accessType><initValue>%s</initValue><index>%d</index></EmbeddedFeature>`, index, value, index)
}

// modbusProject is twoCLUs with its GATE a GATE MODBUS holding the given peripherals.
func modbusProject(peripherals string) string {
	sys := strings.Replace(twoCLUs,
		`<HardwarePeripheryCLU id="17"><name>alarm_zone</name><nameOnCLU>SAT0001</nameOnCLU><typeName>SatelZone</typeName><isRemoved>false</isRemoved></HardwarePeripheryCLU>`,
		peripherals, 1)
	return strings.Replace(sys, "<firmwareType>00000002</firmwareType>", "<firmwareType>00000001</firmwareType>", 1)
}

// A GATE MODBUS's Modbus objects in a project reach its om.lua with their settings, so the
// Modbus map comes out of a project as out of the CLU's om.lua.
func TestOMLuaModbusObjects(t *testing.T) {
	rtu := `<ProgrammablePeripheryCLU><name>wilgotnosc</name><nameOnCLU>MOD0001</nameOnCLU><typeName>ModbusRTU</typeName><features class="linked-list">` +
		modbusFeature(0, "9600") + modbusFeature(1, "0") + modbusFeature(2, "0") + modbusFeature(5, "1") +
		modbusFeature(8, "2") + modbusFeature(10, "3") + modbusFeature(14, "1") + modbusFeature(15, "16") +
		modbusFeature(19, "10") + modbusFeature(21, `\z`) +
		`</features><isRemoved>false</isRemoved></ProgrammablePeripheryCLU>`
	client := `<ProgrammablePeripheryCLU><name>licznik</name><nameOnCLU>MOD0002</nameOnCLU><typeName>ModbusClient</typeName><features class="linked-list">` +
		modbusFeature(1, "192.0.2.5:502") + modbusFeature(2, "3") + modbusFeature(5, "12") + modbusFeature(7, "3") +
		modbusFeature(16, "1\")\nos.exit(1)--") +
		`</features><isRemoved>false</isRemoved></ProgrammablePeripheryCLU>`
	removed := `<ProgrammablePeripheryCLU><name>stary</name><nameOnCLU>MOD0003</nameOnCLU><typeName>Modbus</typeName><isRemoved>true</isRemoved></ProgrammablePeripheryCLU>`
	p, err := Read(projectZip(t, map[string]string{"system.xml": modbusProject(rtu + client + removed), "properties.xml": properties,
		"scripts/Home/setup.lua": "\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings) != 0 {
		t.Errorf("warnings %q", p.Warnings)
	}
	om := p.OMLua(p.CLUs[0])
	if strings.Contains(om, "os.exit") || strings.Contains(om, `\z`) {
		t.Errorf("a value that is no plain setting reached om.lua:\n%s", om)
	}
	m := omparser.ReadModbus(om)
	if m == nil || !m.HasSettings || len(m.Objects) != 2 {
		t.Fatalf("%+v\n%s", m, om)
	}
	doc := m.Doc()
	for _, want := range []string{
		"| wilgotnosc | MOD0001 | ModbusRTU | 9600 8N1 | 1 | 2 | readInput | — | int16 |",
		"| licznik | MOD0002 | ModbusClient | TCP 192.0.2.5:502 | 3 | 12 | readInput |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc lacks %q:\n%s", want, doc)
		}
	}
	// the objects are no devices of the registry
	parsed, err := omparser.Parse(om)
	if err != nil || len(parsed.Objects) != 0 {
		t.Errorf("objects %+v (%v)", parsed.Objects, err)
	}
}
