package omproject

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Domktorymysli/vclu/pkg/dashspec"
	"github.com/Domktorymysli/vclu/pkg/omparser"
)

// The myGrenton interface the user designed in Object Manager: nodes/<id>/data.json.
// This is the only place that knows its format: it is translated into vclu's own
// model (dashspec), from which the dashboards are generated (#219, #220). Every
// value is checked here, and what names an object is looked up in the project.

const (
	maxInterface  = 4 << 20 // one data.json; a house of 12 pages has 135 KB
	maxInterfaces = 8
	maxText       = 200 // runes of a label or unit
)

var (
	reIndex = regexp.MustCompile(`^\d{1,4}$`)
	// a script's arguments as OM writes them: numbers and "strings", comma separated
	reArg = regexp.MustCompile(`^\s*(?:(-?\d+(?:\.\d+)?)|"([^"\\\x00-\x1f]*)")\s*(?:,|$)`)
)

// gCall is a call as the interface writes it.
type gCall struct {
	CallType   string
	CluID      string
	ObjectName string
	Index      string // a number as text, or a user feature's name
	Type       string
	Value      string // a number as text, a string, "$value$" or ""
}

// gPart is a part of a widget: a button, a value, a thermostat row.
type gPart struct {
	Label         string          `json:"label"`
	Unit          string          `json:"unit"`
	Min           *float64        `json:"min"`
	Max           *float64        `json:"max"`
	OnIndication  string          `json:"onIndication"`
	OffIndication string          `json:"offIndication"`
	State         json.RawMessage `json:"state"`
	Value         json.RawMessage `json:"value"`
	ActionOn      json.RawMessage `json:"actionOn"`
	ActionOff     json.RawMessage `json:"actionOff"`
	ActionClick   json.RawMessage `json:"actionClick"`
	ClickAction   json.RawMessage `json:"clickAction"`
	// DIMMER_V2's object
	OnAction       json.RawMessage `json:"onAction"`
	OffAction      json.RawMessage `json:"offAction"`
	SetValueAction json.RawMessage `json:"setValueAction"`
}

// gWidget is a widget as the interface writes it; parts by their keys.
type gWidget struct {
	ID            json.Number `json:"id"`
	Type          string      `json:"type"`
	Label         string      `json:"label"`
	Unit          string      `json:"unit"`
	Precision     *int        `json:"precision"`
	Min           *float64    `json:"min"`
	Max           *float64    `json:"max"`
	ValueType     string      `json:"valueType"`
	OnIndication  string      `json:"onIndication"`
	OffIndication string      `json:"offIndication"`

	Text                  json.RawMessage `json:"text"`
	Value                 json.RawMessage `json:"value"`
	ButtonBistable        *gPart          `json:"buttonBistable"`
	ButtonMonostable      *gPart          `json:"buttonMonostable"`
	ButtonMonostableFirst *gPart          `json:"buttonMonostableFirst"`
	ButtonMonostableSec   *gPart          `json:"buttonMonostableSecond"`
	Object                *gPart          `json:"object"`
	ThermostatCurrentTemp *gPart          `json:"thermostatCurrentTemp"`
	ThermostatTargetTemp  *gPart          `json:"thermostatTargetTemp"`
	ThermostatState       *gPart          `json:"thermostatState"`
	ThermostatMode        *gPart          `json:"thermostatMode"`
	ThermostatSchedule    *gPart          `json:"thermostatSchedule"`

	// dimmer, LED and roller shutter
	Status                *gPart `json:"status"`
	Slider                *gPart `json:"slider"`
	SliderBrightness      *gPart `json:"sliderBrightness"`
	SliderHue             *gPart `json:"sliderHue"`
	SliderSaturation      *gPart `json:"sliderSaturation"`
	ButtonBistableClick   *gPart `json:"buttonBistableClick"`
	ButtonBistableShorter *gPart `json:"buttonBistableShorter"`
	UpButton              *gPart `json:"upButton"`
	DownButton            *gPart `json:"downButton"`
	SliderOpenLevel       *gPart `json:"sliderOpenLevel"`
	SliderSlatsLevel      *gPart `json:"sliderSlatsLevel"`
}

// unit is a unit as OM writes it (DEGREE, PERCENT, UNKNOWN) or as text.
func unit(u string) string {
	switch u {
	case "DEGREE":
		return "°C"
	case "PERCENT":
		return "%"
	case "UNKNOWN":
		return ""
	}
	return text(u)
}

// readInterfaces reads nodes/<id>/data.json of the project.
func (p *Project) readInterfaces(files map[string]*zip.File) {
	var names []string
	for name := range files {
		if strings.HasPrefix(name, "nodes/") && strings.HasSuffix(name, "/data.json") && strings.Count(name, "/") == 2 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if len(p.Interfaces) == maxInterfaces {
			p.warn(fmt.Sprintf("%s: more than %d myGrenton interfaces, left out", name, maxInterfaces))
			continue
		}
		rc, err := files[name].Open()
		if err != nil {
			p.warn(fmt.Sprintf("%s: %v", name, err))
			continue
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxInterface+1))
		_ = rc.Close()
		if err != nil || len(b) > maxInterface {
			p.warn(fmt.Sprintf("%s: unreadable or over %d MB, left out", name, maxInterface>>20))
			continue
		}
		in, err := p.ReadInterface(b)
		if err != nil {
			p.warn(fmt.Sprintf("%s: %v", name, err))
			continue
		}
		p.Interfaces = append(p.Interfaces, *in)
	}
}

// ReadInterface translates one interface's data.json. Interface and page IDs
// already taken by an earlier interface of the project are left out.
func (p *Project) ReadInterface(data []byte) (*dashspec.Interface, error) {
	var raw struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Pages []struct {
			ID      json.Number       `json:"id"`
			Label   string            `json:"label"`
			Widgets []json.RawMessage `json:"widgets"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("not a myGrenton interface: %w", err)
	}
	if !dashspec.ValidID(raw.ID) {
		return nil, fmt.Errorf("interface id %q is no id, left out", text(raw.ID))
	}
	if p.taken == nil {
		p.taken = map[string]bool{}
	}
	// file names: one case-insensitive file system holds them all
	if key := "if:" + strings.ToLower(raw.ID); p.taken[key] {
		return nil, fmt.Errorf("interface id %s is used twice, left out the second time", raw.ID)
	} else {
		p.taken[key] = true
	}
	in := &dashspec.Interface{ID: raw.ID, Label: text(raw.Label), Source: "myGrenton", Pages: []dashspec.Page{}}
	for i, rp := range raw.Pages {
		id := rp.ID.String()
		if !dashspec.ValidPageID(id) || p.taken["page:"+id] {
			p.warn(fmt.Sprintf("interface %s: page %d has no id or one already used, left out", in.Label, i+1))
			continue
		}
		p.taken["page:"+id] = true
		pg := dashspec.Page{ID: id, Label: text(rp.Label), Widgets: []dashspec.Widget{}}
		t := &translation{p: p, ids: map[string]bool{}}
		for n, rw := range rp.Widgets {
			var gw gWidget
			if err := json.Unmarshal(rw, &gw); err != nil {
				t.left = append(t.left, fmt.Sprintf("widget %d: not a widget", n+1))
				continue
			}
			t.widget(gw, n)
		}
		pg.Widgets, pg.LeftOut = t.out, t.left
		if pg.Widgets == nil {
			pg.Widgets = []dashspec.Widget{}
		}
		in.Pages = append(in.Pages, pg)
	}
	return in, nil
}

// translation collects one page's widgets.
type translation struct {
	p    *Project
	out  []dashspec.Widget
	left []string
	ids  map[string]bool
	name string // the widget being translated, for messages
}

func (t *translation) leave(why string) { t.left = append(t.left, t.name+": "+why) }

// id is a unique widget id on the page, from the interface's id.
func (t *translation) id(g gWidget, n int, suffix string) string {
	base := "w" + strconv.Itoa(n+1)
	if v, err := g.ID.Int64(); err == nil && v >= 0 && v < 1e12 {
		base = "w" + strconv.FormatInt(v, 10)
	}
	id := base + suffix
	for i := 2; t.ids[id]; i++ {
		id = base + suffix + "_" + strconv.Itoa(i)
	}
	t.ids[id] = true
	return id
}

// add keeps a widget that passes the model's check.
func (t *translation) add(w dashspec.Widget) bool {
	if err := w.Check(); err != nil {
		t.leave(err.Error())
		return false
	}
	t.out = append(t.out, w)
	return true
}

func (t *translation) widget(g gWidget, n int) {
	label := text(g.Label)
	if label == "" {
		for _, x := range []*gPart{g.ButtonBistable, g.ButtonMonostable, g.ButtonMonostableFirst, g.ThermostatState,
			g.ButtonBistableClick, g.ButtonBistableShorter, g.Slider} {
			if x != nil && x.Label != "" {
				label = text(x.Label)
				break
			}
		}
	}
	t.name = strings.TrimSpace(g.Type + " " + strconv.Quote(label))
	switch g.Type {
	case "HEADER":
		var tx gPart
		_ = json.Unmarshal(g.Text, &tx)
		if label == "" {
			label = text(tx.Label)
		}
		t.add(dashspec.Widget{Kind: dashspec.Group, Label: label})
	case "ON_OFF":
		t.onOff(g, n, label, orEmpty(g.ButtonBistable))
	case "DIMMER":
		// on and off, and the level
		if g.ButtonBistable != nil {
			t.onOff(g, n, label, g.ButtonBistable)
		}
		if g.Slider != nil {
			t.level(g, n, "_level", label, g.Slider)
		} else if g.Status != nil {
			t.level(g, n, "_level", label, g.Status)
		}
	case "LED":
		if g.ButtonBistable != nil {
			t.onOff(g, n, label, g.ButtonBistable)
		}
		for _, s := range []struct {
			part   *gPart
			suffix string
		}{{g.SliderBrightness, "brightness"}, {g.SliderHue, "hue"}, {g.SliderSaturation, "saturation"}} {
			if s.part != nil {
				part := *s.part
				if part.Unit == "DEGREE" {
					part.Unit = "°" // a hue's angle, not a temperature
				}
				t.level(g, n, "_"+s.suffix, label+" ("+s.suffix+")", &part)
			}
		}
	case "ROLLER_SHUTTER", "ROLLER_SHUTTER_V2":
		// the click (one button that moves or stops), up and down, the position and the slats
		for _, b := range []struct {
			part   *gPart
			suffix string
			label  string
		}{{g.ButtonBistableClick, "", label}, {g.ButtonBistableShorter, "", label},
			{g.UpButton, "_up", label + " ▲"}, {g.DownButton, "_down", label + " ▼"}} {
			if b.part != nil && len(b.part.ActionClick) > 0 {
				t.add(dashspec.Widget{Kind: dashspec.Button, ID: t.id(g, n, b.suffix), Label: b.label,
					Do: t.action(b.part.ActionClick, "actionClick")})
			}
		}
		if g.SliderOpenLevel != nil {
			t.level(g, n, "_position", label, g.SliderOpenLevel)
		} else if g.Status != nil {
			t.level(g, n, "_position", label, g.Status)
		}
		if g.SliderSlatsLevel != nil {
			t.level(g, n, "_slats", label+" (slats)", g.SliderSlatsLevel)
		}
	case "VALUE":
		var v gPart
		_ = json.Unmarshal(g.Value, &v)
		if label == "" {
			label = text(v.Label)
		}
		read := t.read(v.State, "state")
		set := t.action(v.ActionClick, "actionClick")
		if set != nil && set.Input && v.Min != nil && v.Max != nil && *v.Min < *v.Max {
			t.add(dashspec.Widget{Kind: dashspec.Slider, ID: t.id(g, n, ""), Label: label, Read: read, Set: set,
				Min: v.Min, Max: v.Max, Unit: unit(v.Unit)})
			return
		}
		if set != nil {
			t.leave("actionClick: only a value to enter, with min and max, is shown")
		}
		t.add(dashspec.Widget{Kind: dashspec.Status, ID: t.id(g, n, ""), Label: label, Read: read, Unit: unit(v.Unit)})
	case "VALUE_V2":
		w := dashspec.Widget{Kind: dashspec.Status, ID: t.id(g, n, ""), Label: label, Read: t.read(g.Value, "value"), Unit: unit(g.Unit)}
		if g.Precision != nil && g.ValueType != "STRING" {
			d := min(max(*g.Precision, 0), 6)
			w.Decimals = &d
		}
		t.add(w)
	case "TEXT":
		t.add(dashspec.Widget{Kind: dashspec.Status, ID: t.id(g, n, ""), Label: label, Read: t.read(g.Text, "text")})
	case "CONTACT_SENSOR":
		o := orEmpty(g.Object)
		if t.add(dashspec.Widget{Kind: dashspec.Status, ID: t.id(g, n, ""), Label: label, Read: t.read(o.Value, "value"),
			OnText: text(g.OnIndication), OffText: text(g.OffIndication)}) {
			if do := t.action(o.ClickAction, "clickAction"); do != nil {
				t.add(dashspec.Widget{Kind: dashspec.Button, ID: t.id(g, n, "_click"), Label: label, Do: do})
			}
		}
	case "SCENE":
		b := orEmpty(g.ButtonMonostable)
		t.add(dashspec.Widget{Kind: dashspec.Button, ID: t.id(g, n, ""), Label: label, Do: t.action(b.ActionClick, "actionClick")})
	case "SCENE_DOUBLE":
		for i, b := range []*gPart{g.ButtonMonostableFirst, g.ButtonMonostableSec} {
			b = orEmpty(b)
			l := text(b.Label)
			if l == "" {
				l = label
			}
			t.add(dashspec.Widget{Kind: dashspec.Button, ID: t.id(g, n, "_"+strconv.Itoa(i+1)), Label: l,
				Do: t.action(b.ActionClick, "actionClick")})
		}
	case "THERMOSTAT":
		cur, target, state := orEmpty(g.ThermostatCurrentTemp), orEmpty(g.ThermostatTargetTemp), orEmpty(g.ThermostatState)
		if label == "" {
			label = "Thermostat"
		}
		w := dashspec.Widget{Kind: dashspec.Thermostat, ID: t.id(g, n, ""), Label: label,
			Current: t.read(cur.State, "current"), Target: t.read(target.State, "target"),
			Set: t.action(target.ActionClick, "set")}
		if len(state.State) > 0 {
			w.State = t.read(state.State, "state")
		}
		if target.Min != nil && target.Max != nil && *target.Min < *target.Max {
			w.Min, w.Max = target.Min, target.Max
		}
		if t.add(w) && (g.ThermostatMode != nil || g.ThermostatSchedule != nil) {
			t.leave("mode and schedule are not shown")
		}
	case "SCHEDULER":
		t.leave("schedulers have no vclu widget yet")
	case "DIMMER_V2":
		// one object: on, off, the level to set and the value; the range is the widget's
		o := orEmpty(g.Object)
		read := t.read(o.Value, "value")
		on, off := t.action(o.OnAction, "onAction"), t.action(o.OffAction, "offAction")
		if on != nil || off != nil {
			t.add(dashspec.Widget{Kind: dashspec.Toggle, ID: t.id(g, n, ""), Label: label, Read: read, On: on, Off: off})
		}
		t.level(g, n, "_level", label, &gPart{Unit: "PERCENT", Min: g.Min, Max: g.Max, State: o.Value, ActionClick: o.SetValueAction})
	case "LED_CCT", "ROLLER_SHUTTER_V3":
		t.leave("this version of the widget is not supported yet")
	default:
		t.leave("widget type not supported")
	}
}

// onOff is a bistable button: a toggle, or a status when it has no actions.
func (t *translation) onOff(g gWidget, n int, label string, b *gPart) {
	read := t.read(b.State, "state")
	on, off := t.action(b.ActionOn, "actionOn"), t.action(b.ActionOff, "actionOff")
	if on == nil && off == nil {
		t.add(dashspec.Widget{Kind: dashspec.Status, ID: t.id(g, n, ""), Label: label, Read: read,
			OnText: text(b.OnIndication), OffText: text(b.OffIndication)})
		return
	}
	t.add(dashspec.Widget{Kind: dashspec.Toggle, ID: t.id(g, n, ""), Label: label, Read: read, On: on, Off: off})
}

// level is a slider when its action takes the entered value within a range, else a status.
func (t *translation) level(g gWidget, n int, suffix, label string, v *gPart) {
	if l := text(v.Label); l != "" {
		label = l
	}
	read := t.read(v.State, "state")
	set := t.action(v.ActionClick, "actionClick")
	// a level of 0-1 in % (a dimmer's, an LED's brightness and saturation) is shown as 0-100,
	// as myGrenton shows it: a slider of 0-1 would only take 0 and 1
	percent := v.Unit == "PERCENT" && v.Min != nil && v.Max != nil && *v.Min == 0 && *v.Max == 1
	if set != nil && set.Input && v.Min != nil && v.Max != nil && *v.Min < *v.Max {
		w := dashspec.Widget{Kind: dashspec.Slider, ID: t.id(g, n, suffix), Label: label, Read: read, Set: set,
			Min: v.Min, Max: v.Max, Unit: unit(v.Unit)}
		if percent {
			lo, hi := 0.0, 100.0
			w.Min, w.Max, w.Percent = &lo, &hi, true
		}
		t.add(w)
		return
	}
	if set != nil {
		t.leave("actionClick: only a value to enter, with min and max, is shown")
	}
	t.add(dashspec.Widget{Kind: dashspec.Status, ID: t.id(g, n, suffix), Label: label, Read: read, Unit: unit(v.Unit), Percent: percent})
}

func orEmpty(p *gPart) *gPart {
	if p == nil {
		return &gPart{}
	}
	return p
}

// call reads a call; nil when there is none or its CLU is no CLU ID. Index and
// value may be JSON strings or numbers.
func (t *translation) call(raw json.RawMessage, key string) *gCall {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.leave(key + ": not a call")
		return nil
	}
	str := func(k string) string { s, _ := m[k].(string); return s }
	c := &gCall{CallType: str("callType"), CluID: str("cluId"), ObjectName: str("objectName"), Type: str("type"),
		Index: scalar(m["index"]), Value: scalar(m["value"])}
	if !reCLUID.MatchString(c.CluID) {
		t.leave(fmt.Sprintf("%s: CLU %q is no CLU ID", key, text(c.CluID)))
		return nil
	}
	return c
}

// scalar is a JSON string or number as text, "" for anything else.
func scalar(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// read translates an ATTRIBUTE or VARIABLE call.
func (t *translation) read(raw json.RawMessage, key string) *dashspec.Read {
	c := t.call(raw, key)
	if c == nil {
		return nil
	}
	r := &dashspec.Read{CLU: c.CluID}
	switch c.CallType {
	case "ATTRIBUTE":
		if !reIndex.MatchString(c.Index) {
			t.leave(fmt.Sprintf("%s: index %q is no number", key, text(c.Index)))
			return nil
		}
		n, _ := strconv.Atoi(c.Index)
		r.Feature = &n
		if c.ObjectName != c.CluID {
			r.Object = c.ObjectName
		}
	case "VARIABLE":
		if c.ObjectName != c.CluID {
			t.leave(fmt.Sprintf("%s: user feature of %q, not of its CLU", key, text(c.ObjectName)))
			return nil
		}
		r.Variable = c.Index
	default:
		t.leave(fmt.Sprintf("%s: %q is no read", key, text(c.CallType)))
		return nil
	}
	if err := r.Check(); err != nil {
		t.leave(key + ": " + err.Error())
		return nil
	}
	if err := t.p.inProject(c.CluID, r.Object, ""); err != nil {
		t.leave(key + ": " + err.Error())
		return nil
	}
	return r
}

// action translates a PSEUDO_METHOD, METHOD or SCRIPT call.
func (t *translation) action(raw json.RawMessage, key string) *dashspec.Action {
	c := t.call(raw, key)
	if c == nil {
		return nil
	}
	a := &dashspec.Action{CLU: c.CluID}
	switch c.CallType {
	case "PSEUDO_METHOD", "METHOD":
		a.Op, a.Object = dashspec.SetFeature, c.ObjectName
		if c.CallType == "METHOD" {
			a.Op = dashspec.Execute
		}
		if c.ObjectName == c.CluID {
			t.leave(key + ": commands to the CLU itself are left out")
			return nil
		}
		if !reIndex.MatchString(c.Index) {
			t.leave(fmt.Sprintf("%s: index %q is no number", key, text(c.Index)))
			return nil
		}
		a.Index, _ = strconv.Atoi(c.Index)
		switch v := c.Value; {
		case v == "$value$":
			a.Input = true
		case v == "":
		default:
			if f, err := strconv.ParseFloat(v, 64); err == nil && !strings.ContainsAny(v, "xXnN") {
				a.Value = f
			} else {
				a.Value = text(v)
			}
		}
	case "SCRIPT":
		a.Op, a.Script = dashspec.Script, c.ObjectName
		args, err := scriptArgs(c.Value)
		if err != nil {
			t.leave(fmt.Sprintf("%s: script %s: %v", key, text(c.ObjectName), err))
			return nil
		}
		a.Args = args
	default:
		t.leave(fmt.Sprintf("%s: %q is no command", key, text(c.CallType)))
		return nil
	}
	if err := a.Check(); err != nil {
		t.leave(key + ": " + err.Error())
		return nil
	}
	if err := t.p.inProject(c.CluID, a.Object, a.Script); err != nil {
		t.leave(key + ": " + err.Error())
		return nil
	}
	return a
}

// scriptArgs reads a script's arguments, which OM keeps as Lua source: `1,-1`,
// `"on","2"`. Only numbers and plain strings are taken.
func scriptArgs(v string) ([]any, error) {
	args := []any{}
	rest := strings.TrimSpace(v)
	for rest != "" {
		m := reArg.FindStringSubmatchIndex(rest)
		if m == nil {
			return nil, fmt.Errorf("arguments %q are not numbers and plain strings", text(v))
		}
		if m[2] >= 0 {
			f, err := strconv.ParseFloat(rest[m[2]:m[3]], 64)
			if err != nil {
				return nil, fmt.Errorf("argument %s is no number vclu can send", text(rest[m[2]:m[3]]))
			}
			args = append(args, f)
		} else {
			args = append(args, rest[m[4]:m[5]])
		}
		rest = strings.TrimSpace(rest[m[1]:])
		if len(args) > 16 {
			return nil, fmt.Errorf("more than 16 arguments")
		}
	}
	if len(args) > 0 && strings.HasSuffix(strings.TrimSpace(v), ",") {
		return nil, fmt.Errorf("arguments %q end with a comma", text(v))
	}
	return args, nil
}

// text keeps free text as valid UTF-8 of at most maxText runes.
func text(s string) string {
	s = strings.ToValidUTF8(s, "?")
	if utf8.RuneCountInString(s) > maxText {
		s = string([]rune(s)[:maxText])
	}
	return s
}

// inProject says whether what a call names is in the project, as the proxies
// register it: the CLU, its object or peripheral, its script.
func (p *Project) inProject(clu, object, script string) error {
	if p.known == nil {
		p.known = map[string]bool{}
		for _, c := range p.CLUs {
			p.known[c.ID] = true
			for _, o := range c.Objects {
				p.known[c.ID+"."+o.ID] = true
			}
			// a GATE Alarm's objects only on a GATE Alarm, as om.lua's parser takes them: on
			// another CLU their features are not guarded (the panel's passwords)
			alarm := omparser.CLUInfo{FwType: c.FwType, HwType: c.HwType}.IsGateAlarm()
			for _, o := range c.Peripherals {
				if o.Class != 0 && (alarm || !omparser.ObjectClass(o.Class).IsGateAlarm()) {
					p.known[c.ID+"."+o.ID] = true
				}
			}
			for _, sc := range c.Scripts {
				p.known[c.ID+":"+sc.Name] = true
			}
		}
	}
	switch {
	case !p.known[clu]:
		return fmt.Errorf("CLU %s is not in the project", clu)
	case script != "":
		if !p.known[clu+":"+script] {
			return fmt.Errorf("script %s is not on %s", script, clu)
		}
	case object != "" && !p.known[clu+"."+object]:
		return fmt.Errorf("%s.%s is not in the project, or vclu does not know its type", clu, object)
	}
	return nil
}
