package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

func modbusGateOM(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../omparser/testdata/om-modbus-gate.lua")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func modbusMap(t *testing.T, s *Server) string {
	t.Helper()
	b, err := os.ReadFile(modbusMapPath(s.serverDir, 500000001))
	if err != nil {
		return ""
	}
	return string(b)
}

const modbusRow = "| MODBAS_WILGOTNOSC_SCHODY | MOD9012 | ModbusRTU | sensor | 9600 8N1 | 1 | 2 | readInput |"

// A GATE MODBUS's om.lua, alone or in a diagnostic pack, gives the agent its Modbus map and
// registers its objects; the kept copy keeps their settings (not the GATE's Wi-Fi password).
func TestOMUploadModbusMap(t *testing.T) {
	for _, tc := range []struct {
		name string
		file func(t *testing.T) (string, []byte)
	}{
		{"om.lua", func(t *testing.T) (string, []byte) { return "om.lua", modbusGateOM(t) }},
		{"pack", func(t *testing.T) (string, []byte) {
			return "pack.zip", zipBytes(t, map[string][]byte{
				"CLU500000001/om.lua": modbusGateOM(t), "CLU212345678/om.lua": []byte(namesOMLua)})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{serverDir: t.TempDir()}
			name, data := tc.file(t)
			code, body := uploadFile(t, s, name, data)
			if code != http.StatusOK || !strings.Contains(string(body), `"modbus":16`) {
				t.Fatalf("%d %s", code, body)
			}
			if doc := modbusMap(t, s); !strings.Contains(doc, modbusRow) {
				t.Errorf("map:\n%s", doc)
			}
			proxy := registered(t, s.serverDir)[500000001]
			for _, want := range []string{
				`RemoteGateModbusSensor:new(_clu, "MOD9012", "MODBAS_WILGOTNOSC_SCHODY", 203)`,
				`registerObject("CLU500000001.MOD9012"`,
				"ObjectCount:16",
			} {
				if !strings.Contains(proxy, want) {
					t.Errorf("registry lacks %q:\n%s", want, proxy)
				}
			}
			kept, err := os.ReadFile(filepath.Join(config.GetOMFilesDir(s.serverDir), "om_500000001.lua"))
			if err != nil || !strings.Contains(string(kept), "MOD9012:set(8, 2)") || strings.Contains(string(kept), "TIM9001:set(") {
				t.Errorf("kept copy (%v):\n%s", err, kept)
			}
		})
	}
}

// An om.lua without settings (a project's) keeps the map; the CLU's own without Modbus
// objects, and deleting the CLU, remove it.
func TestOMModbusMapLifecycle(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	src := string(modbusGateOM(t))
	if code, body := uploadFile(t, s, "om.lua", []byte(src)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}

	declarations := src[:strings.Index(src, "function OnInit")]
	if code, body := uploadFile(t, s, "om.lua", []byte(declarations)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if !strings.Contains(modbusMap(t, s), modbusRow) {
		t.Error("an om.lua without settings replaced the map")
	}

	var noModbus []string
	for _, l := range strings.Split(src, "\n") {
		if !strings.Contains(l, "MOD90") {
			noModbus = append(noModbus, l)
		}
	}
	if code, body := uploadFile(t, s, "om.lua", []byte(strings.Join(noModbus, "\n"))); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if modbusMap(t, s) != "" {
		t.Error("map kept after the GATE lost its Modbus objects")
	}

	if code, body := uploadFile(t, s, "om.lua", []byte(src)); code != http.StatusOK || modbusMap(t, s) == "" {
		t.Fatalf("%d %s", code, body)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/api/om/:serial", s.handleAPIOMDelete)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/om/500000001", nil))
	var del map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &del); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if modbusMap(t, s) != "" {
		t.Error("map kept after the CLU was deleted")
	}
}

// A GATE Alarm's om.lua, with its passwords in GATE objects' settings, gives no Modbus map.
func TestOMModbusMapNotFromGateAlarm(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	if code, body := uploadFile(t, s, "om.lua", []byte(gateAlarmOMLua)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(s.serverDir, "ai", "modbus")); !os.IsNotExist(err) {
		t.Errorf("ai/modbus written for a GATE Alarm (%v)", err)
	}
}

// An om.lua without settings, with no map before it, still names the objects and says why
// their settings are missing.
func TestOMModbusMapWithoutSettings(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	src := string(modbusGateOM(t))
	code, body := uploadFile(t, s, "om.lua", []byte(src[:strings.Index(src, "function OnInit")]))
	if code != http.StatusOK || !strings.Contains(string(body), `"modbus":16`) {
		t.Fatalf("%d %s", code, body)
	}
	if doc := modbusMap(t, s); !strings.Contains(doc, "| MOD9012 | ModbusRTU | number | — |") || !strings.Contains(doc, "no settings") {
		t.Errorf("map:\n%s", doc)
	}
}

// modbusGateCLU is a GATE MODBUS of a project with the given peripherals.
func modbusGateCLU(peripherals string) string {
	return `<specificObject class="CLU"><name>modbus</name><ipAddress>192.0.2.10</ipAddress><nameOnCLU>CLU500000001</nameOnCLU>` +
		`<iosList/><peripheryList>` + peripherals + `</peripheryList><modulesList/>` +
		`<firmwareType>00000001</firmwareType><hardwareType>00000012</hardwareType></specificObject>`
}

// A project gives the map as the GATE's om.lua does; a project without the Modbus objects
// removes it.
func TestOMProjectModbusMap(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	feature := func(i int, v string) string {
		return fmt.Sprintf(`<EmbeddedFeature><initValue>%s</initValue><index>%d</index></EmbeddedFeature>`, v, i)
	}
	rtu := `<ProgrammablePeripheryCLU><name>MODBAS_WILGOTNOSC_SCHODY</name><nameOnCLU>MOD9012</nameOnCLU><typeName>ModbusRTU</typeName><features>` +
		feature(0, "9600") + feature(1, "0") + feature(2, "0") + feature(5, "1") + feature(8, "2") + feature(10, "3") +
		`</features><isRemoved>false</isRemoved></ProgrammablePeripheryCLU>`
	omp := zipBytes(t, map[string][]byte{"system.xml": projectXML(modbusGateCLU(rtu))})
	code, body := uploadFile(t, s, "house.omp", omp)
	if code != http.StatusOK || !strings.Contains(string(body), `"modbus":1`) {
		t.Fatalf("%d %s", code, body)
	}
	if doc := modbusMap(t, s); !strings.Contains(doc, modbusRow) {
		t.Errorf("map:\n%s", doc)
	}
	if strings.Contains(string(body), "does not know") {
		t.Errorf("Modbus objects reported as unknown: %s", body)
	}

	omp = zipBytes(t, map[string][]byte{"system.xml": projectXML(modbusGateCLU(""))})
	if code, body := uploadFile(t, s, "house.omp", omp); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if modbusMap(t, s) != "" {
		t.Error("map kept after the project lost its Modbus objects")
	}
}

// The proxy runs: the objects are in the runtime's registry with their kinds.
func TestOMUploadModbusRuns(t *testing.T) {
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	s := &Server{serverDir: t.TempDir(), luaExecutor: engine}
	code, body := uploadFile(t, s, "om.lua", modbusGateOM(t))
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var ans struct {
		LuaError string `json:"luaError"`
	}
	if err := json.Unmarshal(body, &ans); err != nil || ans.LuaError != "" {
		t.Fatalf("%s (%v)", body, err)
	}
	got, err := engine.Execute(`local a, b = _:get("CLU500000001.MOD9012"), _:get("CLU500000001.MOD9001")
		return a._type .. " " .. a:subscribeFeatures()[1] .. " " .. b._type .. " " .. b:subscribeFeatures()[1]`)
	if err != nil || got != "GATE_MODBUS_SENSOR 22 GATE_MODBUS_NUMBER 14" {
		t.Errorf("%q (%v)", got, err)
	}
}
