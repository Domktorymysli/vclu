package web

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

// The om.lua of a GATE Alarm as Object Manager writes it (#214), anonymised: serials,
// addresses, names and settings made up, the panel's secrets a marker.
const gateMarker = "MARKER-PASSWORD-123"

const gateAlarmOMLua = `-- FwType 00000002
-- FwVersion 00000456
-- HwType 00000012
-- HwVersion 00000002
CLU510000001 = GATE:new(5000, 0xC0A80104, 0xFFFFFF00, 0xC0A80101, "", "")
-- NAME_CLU Alarm=CLU510000001
CLU220000002 = OBJECT:new(1, 0xC0A80105, "CLU220000002")
SAT0001 = GATE:new(103, "SAT0001")
-- NAME_PERIPHERY satel_panel=SAT0001
SAT0002 = GATE:new(100, "SAT0002")
-- NAME_PERIPHERY alarm_ground=SAT0002
SAT0003 = GATE:new(101, "SAT0003")
-- NAME_PERIPHERY pir_hall=SAT0003
SAT0004 = GATE:new(102, "SAT0004")
-- NAME_PERIPHERY siren=SAT0004
JAB0001 = GATE:new(150, "JAB0001")
-- NAME_PERIPHERY jablotron=JAB0001
JAB0002 = GATE:new(151, "JAB0002")
-- NAME_PERIPHERY section_house=JAB0002
SAT0001:set(2, "192.0.2.77")
SAT0001:set(3, 7094)
SAT0001:set(4, "MARKER-PASSWORD-123")
SAT0001:set(7, "MARKER-PASSWORD-123-KEY")
SAT0002:set(1, 1)
SAT0002:set(2, "MARKER-PASSWORD-123-USER")
SAT0004:set(2, "MARKER-PASSWORD-123-OUT")
JAB0001:set(0, "MARKER-PASSWORD-123-ADMIN")
JAB0002:set(2, "MARKER-PASSWORD-123-SECTION")
CLU510000001:add_event(0, OnInit)
`

// gateObjects are the objects the API lists for the GATE.
func gateObjects(t *testing.T, s *Server) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "serial", Value: "510000001"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/om/510000001", nil)
	s.handleAPIOMDetails(c)
	var ans struct {
		Objects json.RawMessage `json:"objects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body, err)
	}
	return string(ans.Objects)
}

// noMarker checks that no file vCLU keeps of an import holds a secret of the om.lua.
func noMarker(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(gateMarker)) || bytes.Contains(data, []byte("192.0.2.77")) {
			t.Errorf("%s holds a secret of the GATE's om.lua", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// captureLog sends the standard logger to a buffer for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(prev)
		log.SetFlags(flags)
	})
	return &buf
}

// A GATE Alarm's objects come in from a diagnostic pack and from its om.lua alone, the
// same; the secrets its om.lua sets reach no file vCLU keeps, no answer and no log line.
func TestGateAlarmUpload(t *testing.T) {
	logged := captureLog(t)

	fromPack := &Server{serverDir: t.TempDir()}
	pack := zipBytes(t, map[string][]byte{"CLU510000001/om.lua": []byte(gateAlarmOMLua)})
	code, packBody := uploadFile(t, fromPack, "pack.zip", pack)
	if code != http.StatusOK {
		t.Fatalf("pack: %d %s", code, packBody)
	}

	// the runtime runs the proxy: the objects are in the registry
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	alone := &Server{serverDir: t.TempDir(), luaExecutor: engine}
	code, aloneBody := uploadFile(t, alone, "om.lua", []byte(gateAlarmOMLua))
	if code != http.StatusOK {
		t.Fatalf("om.lua: %d %s", code, aloneBody)
	}
	var ans struct {
		ObjectCount int      `json:"objectCount"`
		LuaError    string   `json:"luaError"`
		Warnings    []string `json:"warnings"`
	}
	if err := json.Unmarshal(aloneBody, &ans); err != nil {
		t.Fatal(err)
	}
	if ans.ObjectCount != 6 || ans.LuaError != "" || len(ans.Warnings) != 0 {
		t.Errorf("answer %s", aloneBody)
	}
	got, err := engine.Execute(`local n = {}
		for _, id in ipairs({"SAT0001", "SAT0002", "SAT0003", "SAT0004", "JAB0001", "JAB0002"}) do
			local o = _registry["CLU510000001." .. id]
			n[#n + 1] = o and o._name or "missing"
		end
		return table.concat(n, ",")`)
	if err != nil || got != "satel_panel,alarm_ground,pir_hall,siren,jablotron,section_house" {
		t.Errorf("registry %q %v", got, err)
	}

	if a, b := gateObjects(t, fromPack), gateObjects(t, alone); a != b || !strings.Contains(a, `"SAT0002"`) {
		t.Errorf("objects from the pack:\n%s\nfrom om.lua:\n%s", a, b)
	}
	want := registered(t, fromPack.serverDir)
	if got := registered(t, alone.serverDir); got[510000001] != want[510000001] || want[510000001] == "" {
		t.Errorf("from om.lua:\n%s\nfrom the pack:\n%s", got[510000001], want[510000001])
	}

	for _, s := range []*Server{fromPack, alone} {
		noMarker(t, config.GetOMFilesDir(s.serverDir))
		stored, err := os.ReadFile(filepath.Join(config.GetOMFilesDir(s.serverDir), "om_510000001.lua"))
		if err != nil || strings.Contains(string(stored), ":set(") || !strings.Contains(string(stored), `SAT0002 = GATE:new(100, "SAT0002")`) {
			t.Errorf("stored copy %v:\n%s", err, stored)
		}
	}
	for name, body := range map[string][]byte{"pack": packBody, "om.lua": aloneBody, "log": logged.Bytes()} {
		if bytes.Contains(body, []byte(gateMarker)) {
			t.Errorf("the %s holds a secret: %s", name, body)
		}
	}
	if !strings.Contains(logged.String(), "settings of the GATE's objects left out") {
		t.Errorf("log: %s", logged)
	}
}

// An HTTP GATE (same hardware, FwType 00000003) has other objects under the alarm's
// classes: left out, the answer says which.
func TestGateAlarmClassesOnHTTPGate(t *testing.T) {
	om := strings.Replace(gateAlarmOMLua, "-- FwType 00000002", "-- FwType 00000003", 1)
	s := &Server{serverDir: t.TempDir()}
	code, body := uploadFile(t, s, "om.lua", []byte(om))
	var ans struct {
		ObjectCount int      `json:"objectCount"`
		Warnings    []string `json:"warnings"`
	}
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK || ans.ObjectCount != 0 || len(ans.Warnings) != 6 ||
		!strings.HasPrefix(ans.Warnings[0], "CLU510000001: SAT0001: class 103") {
		t.Errorf("%d %s", code, body)
	}
	noMarker(t, config.GetOMFilesDir(s.serverDir))

	pack := zipBytes(t, map[string][]byte{"CLU510000001/om.lua": []byte(om)})
	_, body = uploadFile(t, s, "pack.zip", pack)
	var packAns projectAnswer
	if err := json.Unmarshal(body, &packAns); err != nil {
		t.Fatal(err)
	}
	if len(packAns.Warnings) != 6 {
		t.Errorf("pack warnings %q", packAns.Warnings)
	}
}

// A copy kept before vCLU removed the settings loses them when the proxies are regenerated.
func TestGateAlarmRegenerateStripsOldCopy(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	if code, body := uploadFile(t, s, "om.lua", []byte(gateAlarmOMLua)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	// as an import before #214 left it, with the GATE's Wi-Fi too
	stored := filepath.Join(config.GetOMFilesDir(s.serverDir), "om_510000001.lua")
	old := strings.Replace(gateAlarmOMLua, `0xC0A80101, "", "")`, `0xC0A80101, "home-net", "MARKER-PASSWORD-123-WIFI")`, 1)
	if old == gateAlarmOMLua {
		t.Fatal("no GATE definition in the fixture")
	}
	if err := os.WriteFile(stored, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	s.handleAPIOMRegenerate(c)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"regenerated":1`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	noMarker(t, config.GetOMFilesDir(s.serverDir))
	if data, _ := os.ReadFile(stored); strings.Contains(string(data), "home-net") ||
		!strings.Contains(string(data), `CLU510000001 = GATE:new(5000, 0xC0A80104, 0xFFFFFF00, 0xC0A80101, "", "")`) {
		t.Errorf("Wi-Fi kept:\n%s", data)
	}
}
