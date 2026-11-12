package omproject

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/omparser"
)

func projectZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A CLU spelled out inside a peripheral and referenced from its tree entry,
// a module defined inside the first object and referenced by the second,
// events numbered by <vector> (OM 1.3) and by <index> (later).
const twoCLUs = `<object-stream>
  <TreeObject id="1"><children id="2">
    <TreeObject id="3"><specificObject class="HardwarePeripheryCLU" id="4">
      <clu id="10">
        <name>Gate</name>
        <ipAddress>192.0.2.4</ipAddress>
        <nameOnCLU>CLU511000001</nameOnCLU>
        <eventsList id="11"/>
        <iosList id="12"/>
        <scripts id="13"/>
        <peripheryList id="14">
          <HardwarePeripheryCLU id="17"><name>alarm_zone</name><nameOnCLU>SAT0001</nameOnCLU><typeName>SatelZone</typeName><isRemoved>false</isRemoved></HardwarePeripheryCLU>
        </peripheryList>
        <modulesList class="linked-list" id="15"/>
        <firmwareType>00000002</firmwareType>
        <firmwareVersion>00000456</firmwareVersion>
        <hardwareType>00000012</hardwareType>
        <hardwareVersion>00000002</hardwareVersion>
        <privateKey id="16">c2VjcmV0</privateKey>
      </clu>
    </specificObject></TreeObject>
    <TreeObject id="5"><specificObject class="CLU" reference="10"/></TreeObject>
    <TreeObject id="6"><specificObject class="CLU" id="20">
      <name>Home</name>
      <ipAddress>192.0.2.3</ipAddress>
      <nameOnCLU>CLU221000002</nameOnCLU>
      <eventsList id="21">
        <Event id="22"><name>OnInit</name><commands class="linked-list" id="23"><string>Home-&gt;setup()</string></commands><index>0</index></Event>
      </eventsList>
      <iosList id="24">
        <Input id="30">
          <name>hall_button</name>
          <nameOnCLU>DIN0001</nameOnCLU>
          <type>DIN</type>
          <events class="linked-list" id="31">
            <Event id="32"><name>OnSwitchOn</name><commands class="linked-list" id="33"><string>Home-&gt;hall_light-&gt;SwitchOn(0)</string></commands><vector>1</vector></Event>
            <Event id="34"><name>OnSwitchOff</name><commands class="linked-list" id="35"/><vector>2</vector></Event>
          </events>
          <module id="36"><moduleID>330000001</moduleID><innerName>13ab6681</innerName><moduleClass>1e</moduleClass><active>true</active></module>
          <classTypeId>3</classTypeId>
          <isRemoved>false</isRemoved>
          <number>0</number>
          <active>true</active>
        </Input>
        <Output id="40">
          <name>hall_light</name>
          <nameOnCLU>DOU0001</nameOnCLU>
          <type>DOUT</type>
          <module reference="36"/>
          <classTypeId>4</classTypeId>
          <number>1</number>
        </Output>
        <Output id="41">
          <name>gone</name>
          <nameOnCLU>DOU0002</nameOnCLU>
          <module reference="36"/>
          <classTypeId>4</classTypeId>
          <isRemoved>true</isRemoved>
          <number>2</number>
        </Output>
      </iosList>
      <scripts id="50">
        <specificObject class="Script" id="51"><name>setup</name><parameters id="52"><ScriptParameter id="53"><name>level</name></ScriptParameter></parameters><isRemoved>false</isRemoved></specificObject>
      </scripts>
      <peripheryList id="60">
        <HardwarePeripheryCLU id="61"><clu reference="20"/><name>living</name><nameOnCLU>THE0001</nameOnCLU><typeName>Thermostat</typeName><isRemoved>false</isRemoved></HardwarePeripheryCLU>
      </peripheryList>
      <modulesList class="linked-list" id="70"><InputOutputDefinition reference="36"/></modulesList>
      <firmwareType>00000003</firmwareType>
      <firmwareVersion>00000203</firmwareVersion>
      <hardwareType>00000013</hardwareType>
      <hardwareVersion>00000001</hardwareVersion>
    </specificObject></TreeObject>
  </children></TreeObject>
</object-stream>`

const properties = `<object-stream>
  <ProjectProperties id="1">
    <version>230100</version>
    <versionExt>1.8.0_2301</versionExt>
    <projectCipherKey id="2"><keyBytes id="3">S3CR3T</keyBytes></projectCipherKey>
    <wifiPassword>S3CR3T</wifiPassword>
  </ProjectProperties>
</object-stream>`

func TestReadProject(t *testing.T) {
	p, err := Read(projectZip(t, map[string]string{
		"system.xml":             twoCLUs,
		"properties.xml":         properties,
		"scripts/Home/setup.lua": "Home->hall_light->SwitchOn(0)\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != "1.8.0_2301" || len(p.Warnings) != 0 || len(p.CLUs) != 2 {
		t.Fatalf("version %q warnings %q CLUs %d", p.Version, p.Warnings, len(p.CLUs))
	}
	gate, home := p.CLUs[0], p.CLUs[1]
	if gate.ID != "CLU511000001" || gate.Name != "Gate" || !gate.Gate || home.Gate {
		t.Errorf("CLUs %+v %+v", gate, home)
	}
	if len(home.Objects) != 2 || home.Objects[1].Module != 330000001 || len(home.Modules) != 1 {
		t.Errorf("objects %+v modules %+v", home.Objects, home.Modules)
	}
	if ev := home.Objects[0].Events; len(ev) != 1 || ev[0].Index != 1 || ev[0].Commands[0] != "Home->hall_light->SwitchOn(0)" {
		t.Errorf("events by <vector>: %+v", ev)
	}
	if ev := home.Events; len(ev) != 1 || ev[0].Event != "OnInit" || ev[0].Index != 0 {
		t.Errorf("CLU events by <index>: %+v", ev)
	}
	if sc := home.Scripts; len(sc) != 1 || sc[0].Params[0] != "level" || !strings.Contains(sc[0].Source, "SwitchOn") {
		t.Errorf("scripts %+v", sc)
	}
}

// What OMLua writes is what omparser reads from the om.lua OM writes.
func TestOMLuaParses(t *testing.T) {
	p, err := Read(projectZip(t, map[string]string{"system.xml": twoCLUs, "properties.xml": properties}))
	if err != nil {
		t.Fatal(err)
	}
	om := p.OMLua(p.CLUs[1])
	parsed, err := omparser.Parse(om)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.CLU.ID != "CLU221000002" || parsed.CLU.Name != "Home" || omparser.IPToString(parsed.CLU.IP) != "192.0.2.3" || parsed.CLU.HwType != "00000013" {
		t.Errorf("CLU %+v", parsed.CLU)
	}
	if len(parsed.References) != 1 || parsed.References[0].ID != "CLU511000001" {
		t.Errorf("references %+v", parsed.References)
	}
	if len(parsed.Modules) != 1 || parsed.Modules[0].Address != 330000001 || parsed.Modules[0].ModuleType != 0x1e {
		t.Errorf("modules %+v", parsed.Modules)
	}
	the := parsed.ObjectMap["THE0001"]
	light := parsed.ObjectMap["DOU0001"]
	if the.Class != omparser.ClassThermostat || the.Name != "living" || light.Name != "hall_light" || light.Index != 1 || light.ModuleID != "mm_330000001" {
		t.Errorf("objects %+v", parsed.Objects)
	}
	if strings.Contains(om, "S3CR3T") {
		t.Error("om.lua has a secret of properties.xml")
	}

	gate := p.OMLua(p.CLUs[0])
	if !strings.Contains(gate, `CLU511000001 = GATE:new(5000, 0xC0000204, 0x0, 0x0, "", "")`) {
		t.Errorf("a GATE without its network:\n%s", gate)
	}
	if !strings.Contains(gate, `SAT0001 = GATE:new(100, "SAT0001")`+"\n-- NAME_PERIPHERY alarm_zone=SAT0001") {
		t.Errorf("a GATE's peripheral not as OM writes it:\n%s", gate)
	}
}

// A name cannot add a line to om.lua, and an ID that is no Lua name is left out.
func TestOMLuaKeepsValuesOnTheirLine(t *testing.T) {
	sys := strings.Replace(twoCLUs, "<name>hall_light</name>", "<name>hall\nCLU1 = OBJECT:new(0, 0x01)</name>", 1)
	sys = strings.Replace(sys, "<nameOnCLU>DIN0001</nameOnCLU>", "<nameOnCLU>DIN0001\nX</nameOnCLU>", 1)
	p, err := Read(projectZip(t, map[string]string{"system.xml": sys}))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := omparser.Parse(p.OMLua(p.CLUs[1]))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.CLU.ID != "CLU221000002" || len(parsed.Objects) != 2 {
		t.Errorf("CLU %+v objects %+v", parsed.CLU, parsed.Objects)
	}
	if _, ok := parsed.ObjectMap["DIN0001"]; ok {
		t.Error("an object with an invalid ID is registered")
	}
	if !strings.Contains(strings.Join(p.Warnings, "\n"), "no valid name on the CLU") {
		t.Errorf("warnings %q", p.Warnings)
	}
}

// Values om.lua and the proxies would carry as code are left out with a
// warning: firmware values that are no hex numbers, names that are no Lua
// names, a version that is no version.
func TestReadLeavesOutWhatIsNoValue(t *testing.T) {
	sys := strings.Replace(twoCLUs, "<firmwareVersion>00000203</firmwareVersion>", "<firmwareVersion>1\nos.exit(1)</firmwareVersion>", 1)
	sys = strings.Replace(sys, "<name>hall_light</name>", "<name>a=1;os.exit(1)</name>", 1)
	sys = strings.Replace(sys, "<name>living</name>", "<name>living room</name>", 1)
	props := strings.Replace(properties, "1.8.0_2301", "1.8\nos.exit(1)", 1)
	p, err := Read(projectZip(t, map[string]string{"system.xml": sys, "properties.xml": props}))
	if err != nil {
		t.Fatal(err)
	}
	om := p.OMLua(p.CLUs[1])
	if strings.Contains(om, "os.exit") || strings.Contains(om, "living room") {
		t.Errorf("om.lua carries a value from the project:\n%s", om)
	}
	parsed, err := omparser.Parse(om)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ObjectMap["DOU0001"].Name != "DOU0001" || parsed.ObjectMap["THE0001"].Name != "THE0001" {
		t.Errorf("objects with no Lua names: %+v", parsed.Objects)
	}
	w := strings.Join(p.Warnings, "\n")
	for _, want := range []string{"is not a hex number", `name "a=1;os.exit(1)"`, `name "living room"`, "is not a version"} {
		if !strings.Contains(w, want) {
			t.Errorf("no warning %q in %q", want, p.Warnings)
		}
	}
	if p.Version != "" || p.CLUs[1].FwVersion != "" {
		t.Errorf("version %q firmware %q", p.Version, p.CLUs[1].FwVersion)
	}
}

// A CLU without its lists is reported, not dropped in silence.
func TestReadWarnsAboutCLUWithoutLists(t *testing.T) {
	sys := strings.Replace(twoCLUs, `<iosList id="12"/>`, "", 1)
	p, err := Read(projectZip(t, map[string]string{"system.xml": sys, "properties.xml": properties}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.CLUs) != 1 || !strings.Contains(strings.Join(p.Warnings, "\n"), "CLU511000001: a CLU without") {
		t.Errorf("CLUs %d warnings %q", len(p.CLUs), p.Warnings)
	}
}

func TestReadRefusesTooLarge(t *testing.T) {
	deep := strings.Repeat("<a>", maxDepth+1) + strings.Repeat("</a>", maxDepth+1)
	big := "<object-stream>" + strings.Repeat("<!-- padding -->", maxSystemXML/16+1) + "</object-stream>"
	for name, sys := range map[string]string{"deep": deep, "big": big} {
		_, err := Read(projectZip(t, map[string]string{"system.xml": sys}))
		if !errors.Is(err, errTooLarge) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReadRejectsWhatIsNoProject(t *testing.T) {
	if _, err := Read([]byte("no zip")); err == nil {
		t.Error("not a zip: no error")
	}
	if _, err := Read(projectZip(t, map[string]string{"CLU1/om.lua": ""})); err == nil || !strings.Contains(err.Error(), "system.xml") {
		t.Errorf("a pack: %v", err)
	}
	if _, err := Read(projectZip(t, map[string]string{"system.xml": "<object-stream/>"})); err == nil {
		t.Error("no CLU: no error")
	}
}

// index.md turns the project's own commands into vCLU's Lua and lists the
// objects OM already commands.
func TestDocsTranslateAndListDriven(t *testing.T) {
	p, err := Read(projectZip(t, map[string]string{
		"system.xml":             twoCLUs,
		"scripts/Home/setup.lua": "if x then Home->hall_light->SwitchOff(0) end\nlocal v = Home->hall_button->Value()\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	idx := p.Docs()["index.md"]
	for _, want := range []string{
		"| `Home->hall_light->SwitchOn(0)` | `_:get(\"CLU221000002.DOU0001\"):switchOn(0)` |",
		"| CLU221000002.DOU0001 | hall_light | ",
		"OnSwitchOn, script setup |",
	} {
		if !strings.Contains(idx, want) {
			t.Errorf("index.md has no %q:\n%s", want, idx)
		}
	}
	// reading a value commands nothing
	if strings.Contains(idx, "| CLU221000002.DIN0001 |") {
		t.Errorf("hall_button is read, not commanded:\n%s", idx)
	}
}

func TestDocsSplitLongScripts(t *testing.T) {
	long := strings.Repeat("x = 1 -- ```\n", maxDoc/13/2+10)
	c := CLU{ID: "CLU1", Name: "Home", Scripts: []Script{{Name: "a", Source: long}, {Name: "b", Source: long}, {Name: "c", Source: long}}}
	docs := (&Project{CLUs: []CLU{c}}).Docs()
	for _, name := range []string{"index.md", "CLU1.md", "CLU1-scripts.md", "CLU1-scripts-2.md"} {
		d, ok := docs[name]
		if !ok {
			t.Fatalf("no %s in %v", name, keys(docs))
		}
		if len(d) > maxDoc+maxDoc/2 {
			t.Errorf("%s has %d bytes", name, len(d))
		}
	}
	if !strings.Contains(docs["CLU1-scripts.md"], "````lua\n") {
		t.Error("a script with ``` is not fenced with more backticks")
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
