package lua

import (
	"encoding/json"
	"strings"
	"testing"
)

// The schedule widget's definition and values as a page or an app gets them: the definition
// names the calendar, the schedule and the week's format; the values carry a text for a client
// that does not know the type.
func TestDashboardScheduleWidget(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		_dashboards = {}
		dom = Calendar.new('dom'):clock({ wait = false })
			:schedule('komfort', { values = { 'eco', 'komfort' }, default = 'eco',
				{ every = 'mo', hours = '06:00-08:00', value = 'komfort' } }):editable()
			:build()
		Dashboard.new('d'):section('S', 'clock'):schedule('k', 'Ogrzewanie', dom, 'komfort', { span = 2 }):build()
	`); err != nil {
		t.Fatal(err)
	}
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("d")`, &def)
	w := def.widget("k")
	got, _ := json.Marshal(w)
	if string(got) != `{"calendar":"dom","disabled":false,"format":1,"id":"k","label":"Ogrzewanie","schedule":"komfort","section":0,"span":2,"step":30,"type":"schedule"}` {
		t.Errorf("definition %s", got)
	}
	var values map[string]map[string]any
	dashJSON(t, engine, `Dashboard.API.getValues("d")`, &values)
	// the calendar waits for its first tick: no value yet, the version already
	if v := values["k"]; v["value"] != "…" || v["version"] != 0.0 {
		t.Errorf("values %v", v)
	}
}

func TestDashboardScheduleWidgetErrors(t *testing.T) {
	for module, want := range map[string]string{
		`Dashboard.new('d'):section('S', 'clock'):schedule('k', 'K', dom, 'taryfa')`:                       `schedule widget "k": Calendar dom:week: schedule "taryfa" is not editable`,
		`Dashboard.new('d'):section('S', 'clock'):schedule('k', 'K', dom, 'nie')`:                          `unknown schedule "nie"`,
		`Dashboard.new('d'):section('S', 'clock'):schedule('k', 'K', dom, 'komfort', { view = 'month' })`:  `schedule widget "k": unknown option "view" ("disabled", "remote", "span", "step", "visible")`,
		`Dashboard.new('d'):section('S', 'clock'):schedule('k', 'K', dom)`:                                 `expected the id of an editable schedule`,
		`Dashboard.new('d'):section('S', 'clock'):schedule('k', 'K', dom, 'komfort', { disabled = true })`: `opts.disabled must be a function`,
		`Dashboard.new('d'):section('S', 'clock'):schedule('k', 'K', dom, 'komfort', { step = 7 })`:        `opts.step must be 5, 10, 15, 30 or 60`,
	} {
		engine := NewLuaEngine()
		_, err := engine.Execute(`
			dom = Calendar.new('dom'):clock({ wait = false })
				:schedule('komfort', { values = { 'eco', 'komfort' }, default = 'eco', { every = 'mo', hours = '06:00-08:00', value = 'komfort' } }):editable()
				:schedule('taryfa', { values = { 'a', 'b' }, default = 'a', { every = 'mo', value = 'b' } })
				:build()
		` + module)
		engine.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s:\nwant %q, got %v", module, want, err)
		}
	}
}
