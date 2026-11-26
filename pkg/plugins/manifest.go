package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// plugin.json on plugin API 2: every permission is an object with its parameters and, for a
// wide one, why the plugin needs it:
//
//	"permissions": {
//	  "internet": {"hosts": ["api.example.com"], "why": "..."},
//	  "lan":      {"ports": [80]},                 // without ports: any port, and a why
//	  "udp":      {"ports": [7000], "broadcast": [7000]},
//	  "tcp":      {"ports": [23]},
//	  "mqtt":     {"subscribe": ["zigbee2mqtt/#"], "publish": ["zigbee2mqtt/+/set"]},
//	  "modbus":   true,
//	  "clu":      {"why": "..."}, "tftp": {"why": "..."}, "serial": {"why": "..."}
//	}
//
// A key or a field this vclu does not know refuses the plugin: it was written for a newer one.

const (
	maxManifestHosts  = 32
	maxManifestPorts  = 8
	maxManifestTopics = 32
	maxWhy            = 200
	maxManifestName   = 80
	maxManifestDesc   = 500
)

// cluPorts: the CLU's own ports, reached only with clu (1234) and tftp (69).
var cluPorts = []int{1234, 69}

// tcpReservedPorts: ports of protocols with a permission of their own.
var tcpReservedPorts = map[int]string{1234: "clu", 69: "tftp", 502: "modbus", 1883: "mqtt", 8883: "mqtt"}

// ReservedPermissions: names kept for later versions of plugin API 2; today they refuse a
// plugin as any unknown permission does.
var ReservedPermissions = []string{"webhooks", "files", "notify", "gpio", "i2c", "onewire"}

// knownManifestKeys: the top level of plugin.json this vclu reads or keeps for people and
// agents; another key is logged, an "x-" one is the author's own.
var knownManifestKeys = map[string]bool{
	"id": true, "name": true, "version": true, "description": true, "author": true, "homepage": true,
	"license": true, "tags": true, "dependencies": true, "configSchema": true, "docs": true, "files": true,
	"api": true, "permissions": true, "requires": true, "form": true, "config": true, "dashboards": true,
	"actions": true, "exports": true, "factory": true, "profiles": true, "icon": true,
}

// movedManifestKeys: top-level keys of earlier drafts of plugin API 2, now inside permissions.
var movedManifestKeys = map[string]string{
	"udp":            `"permissions": {"udp": {"ports": [...], "broadcast": [...]}}`,
	"tcp":            `"permissions": {"tcp": {"ports": [...]}}`,
	"websocket":      `"permissions": {"lan": {"ports": [...]}} (HTTP and WebSocket)`,
	"internetWhy":    `"permissions": {"internet": {"hosts": [...], "why": "..."}}`,
	"minVcluVersion": `"requires": {"vclu": ">=1.13.0"}`,
}

// ErrNewerVclu marks a plugin.json this vclu cannot read because it was written for a newer one.
var ErrNewerVclu = errors.New("the plugin needs a newer vclu")

func newerVclu(what string) error {
	return fmt.Errorf("%s: unknown to this vclu: %w", what, ErrNewerVclu)
}

// decodeStrict decodes v into dst refusing unknown fields as written for a newer vclu, a
// field in another case ("Ports") among them, and a field written twice.
func decodeStrict(where string, v json.RawMessage, dst any) error {
	if err := strictKeys(where, v, reflect.TypeOf(dst)); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(v))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		msg := err.Error()
		if i := strings.Index(msg, "unknown field "); i >= 0 {
			return newerVclu(where + "." + strings.Trim(msg[i+len("unknown field "):], `"`))
		}
		return fmt.Errorf("%s: %w", where, err)
	}
	return nil
}

type jsonEntry struct {
	key string
	val json.RawMessage
}

// objectEntries: the keys of the JSON object v in order with their values; a key twice is
// an error. Not an object: nil (the decoder says what it is).
func objectEntries(where string, v json.RawMessage) ([]jsonEntry, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(v), []byte("{")) {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(v))
	if _, err := dec.Token(); err != nil {
		return nil, nil
	}
	var out []jsonEntry
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil
		}
		key, _ := tok.(string)
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, nil
		}
		if slices.ContainsFunc(out, func(e jsonEntry) bool { return e.key == key }) {
			return nil, fmt.Errorf("%s.%s: written twice", where, key)
		}
		out = append(out, jsonEntry{key, val})
	}
	return out, nil
}

var jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()

// strictKeys: every key of the object v is a field of the struct t, written as its tag spells
// it and once; nested structs, maps and lists of them likewise. A type that reads itself
// (LocalText, RawMessage) is its own.
func strictKeys(where string, v json.RawMessage, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if reflect.PointerTo(t).Implements(jsonUnmarshaler) {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		entries, err := objectEntries(where, v)
		if err != nil {
			return err
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" {
				name = f.Name
			}
			if name != "-" && f.IsExported() {
				fields[name] = f.Type
			}
		}
		for _, e := range entries {
			ft, ok := fields[e.key]
			if !ok {
				return newerVclu(where + "." + e.key)
			}
			if err := strictKeys(where+"."+e.key, e.val, ft); err != nil {
				return err
			}
		}
	case reflect.Map:
		entries, err := objectEntries(where, v)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := strictKeys(where+"."+e.key, e.val, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		var items []json.RawMessage
		if json.Unmarshal(v, &items) != nil {
			return nil
		}
		for i, item := range items {
			if err := strictKeys(fmt.Sprintf("%s[%d]", where, i), item, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func isJSON(v json.RawMessage, lit string) bool { return string(bytes.TrimSpace(v)) == lit }

// checkWhy: a text for the install dialog, one line, no markup.
func checkWhy(where string, w *LocalText) error {
	if w == nil {
		return nil
	}
	if w.In("pl") == "" {
		return fmt.Errorf("%s.why: empty", where)
	}
	for _, s := range w.each() {
		if !plainText(s, maxWhy) || strings.ContainsAny(s, "<>") {
			return fmt.Errorf("%s.why: up to %d characters on one line, without < or >", where, maxWhy)
		}
	}
	return nil
}

// checkPorts: whole ports 1-65535, none twice, at most maxManifestPorts, none of reserved.
func checkPorts(where string, ports []int, reserved func(int) string) error {
	if len(ports) > maxManifestPorts {
		return fmt.Errorf("%s: at most %d ports", where, maxManifestPorts)
	}
	for i, p := range ports {
		if p < 1 || p > 65535 {
			return fmt.Errorf("%s: %d is not a port 1-65535", where, p)
		}
		if owner := reserved(p); owner != "" {
			return fmt.Errorf("%s: port %d belongs to the %s permission", where, p, owner)
		}
		if slices.Contains(ports[:i], p) {
			return fmt.Errorf("%s: %d twice", where, p)
		}
	}
	return nil
}

func tcpReserved(p int) string { return tcpReservedPorts[p] }

func udpReserved(p int) string {
	if p == 1234 {
		return "clu"
	}
	if p == 69 {
		return "tftp"
	}
	return ""
}

// checkTopics: MQTT topic filters, at most maxManifestTopics.
func checkTopics(where string, filters []string) error {
	if len(filters) > maxManifestTopics {
		return fmt.Errorf("%s: at most %d topic filters", where, maxManifestTopics)
	}
	for _, f := range filters {
		if !validTopicFilter(f) {
			return fmt.Errorf("%s: %q is not a topic filter (+ for a whole level, # only at the end)", where, f)
		}
	}
	return nil
}

// UnmarshalJSON reads plugin API 2's permissions; see the top of this file.
func (p *Permissions) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return fmt.Errorf(`permissions: an object such as {"lan": {"ports": [80]}}, {} for none`)
	}
	if _, err := objectEntries("permissions", data); err != nil {
		return err
	}
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	out := Permissions{}
	for _, name := range names {
		v := raw[name]
		if isJSON(v, "false") || isJSON(v, "null") {
			continue // not asked for
		}
		where := "permissions." + name
		var err error
		switch name {
		case "internet":
			err = out.readInternet(where, v)
		case "lan":
			err = out.readLAN(where, v)
		case "udp":
			err = out.readUDP(where, v)
		case "tcp":
			err = out.readTCP(where, v)
		case "mqtt":
			err = out.readMQTT(where, v)
		case "modbus", "clu", "tftp", "serial":
			err = out.readPlain(name, where, v)
		case "grenton_raw":
			err = fmt.Errorf(`%s: on plugin API 2 it is "clu": {"why": "..."}`, where)
		default:
			err = newerVclu(where)
		}
		if err != nil {
			return err
		}
	}
	*p = out
	return nil
}

func (p *Permissions) setWhy(name string, w *LocalText) {
	if w == nil {
		return
	}
	if p.Why == nil {
		p.Why = map[string]LocalText{}
	}
	p.Why[name] = *w
}

func (p *Permissions) readInternet(where string, v json.RawMessage) error {
	var o struct {
		Hosts []string   `json:"hosts"`
		Why   *LocalText `json:"why"`
	}
	if !bytes.HasPrefix(bytes.TrimSpace(v), []byte("{")) {
		return fmt.Errorf(`%s: {"hosts": ["api.example.com"]}, or {"hosts": ["*"], "why": "..."} for any host`, where)
	}
	if err := decodeStrict(where, v, &o); err != nil {
		return err
	}
	if err := checkWhy(where, o.Why); err != nil {
		return err
	}
	if len(o.Hosts) == 0 {
		return fmt.Errorf("%s.hosts: at least one host", where)
	}
	if len(o.Hosts) > maxManifestHosts {
		return fmt.Errorf("%s.hosts: at most %d hosts", where, maxManifestHosts)
	}
	hosts, err := NormalizeInternetHosts(o.Hosts)
	if err != nil {
		return err
	}
	if hosts.Any() && o.Why == nil {
		return fmt.Errorf(`%s: any host ("*") needs "why"`, where)
	}
	p.Internet = hosts
	p.setWhy("internet", o.Why)
	return nil
}

func (p *Permissions) readLAN(where string, v json.RawMessage) error {
	var o struct {
		Ports *[]int     `json:"ports"`
		Why   *LocalText `json:"why"`
	}
	if !isJSON(v, "true") {
		if err := decodeStrict(where, v, &o); err != nil {
			return err
		}
	}
	if err := checkWhy(where, o.Why); err != nil {
		return err
	}
	if o.Ports == nil {
		if o.Why == nil {
			return fmt.Errorf(`%s: the ports it reaches ({"ports": [80]}), or "why" it needs any port`, where)
		}
	} else {
		if len(*o.Ports) == 0 {
			return fmt.Errorf(`%s.ports: at least one port; without ports leave the list out and say "why"`, where)
		}
		if err := checkPorts(where+".ports", *o.Ports, tcpReserved); err != nil {
			return err
		}
		p.LANPorts = slices.Clone(*o.Ports)
	}
	p.LAN = true
	p.setWhy("lan", o.Why)
	return nil
}

func (p *Permissions) readUDP(where string, v json.RawMessage) error {
	var o struct {
		Ports     []int      `json:"ports"`
		Broadcast []int      `json:"broadcast"`
		Why       *LocalText `json:"why"`
	}
	if isJSON(v, "true") {
		return fmt.Errorf(`%s: {"ports": [7000]}, with "broadcast": [7000] to broadcast`, where)
	}
	if err := decodeStrict(where, v, &o); err != nil {
		return err
	}
	if err := checkWhy(where, o.Why); err != nil {
		return err
	}
	if len(o.Ports) == 0 {
		return fmt.Errorf("%s.ports: at least one port", where)
	}
	if err := checkPorts(where+".ports", o.Ports, udpReserved); err != nil {
		return err
	}
	if err := checkPorts(where+".broadcast", o.Broadcast, udpReserved); err != nil {
		return err
	}
	for _, b := range o.Broadcast {
		if !slices.Contains(o.Ports, b) {
			return fmt.Errorf("%s.broadcast: %d is not among its ports", where, b)
		}
	}
	p.UDP, p.UDPPorts, p.BroadcastPorts = true, slices.Clone(o.Ports), slices.Clone(o.Broadcast)
	if p.BroadcastPorts == nil {
		p.BroadcastPorts = []int{}
	}
	p.setWhy("udp", o.Why)
	return nil
}

func (p *Permissions) readTCP(where string, v json.RawMessage) error {
	var o struct {
		Ports []int      `json:"ports"`
		Why   *LocalText `json:"why"`
	}
	if isJSON(v, "true") {
		return fmt.Errorf(`%s: {"ports": [23]}`, where)
	}
	if err := decodeStrict(where, v, &o); err != nil {
		return err
	}
	if err := checkWhy(where, o.Why); err != nil {
		return err
	}
	if len(o.Ports) == 0 {
		return fmt.Errorf("%s.ports: at least one port", where)
	}
	if err := checkPorts(where+".ports", o.Ports, tcpReserved); err != nil {
		return err
	}
	p.TCP, p.TCPPorts = true, slices.Clone(o.Ports)
	p.setWhy("tcp", o.Why)
	return nil
}

func (p *Permissions) readMQTT(where string, v json.RawMessage) error {
	var o struct {
		Subscribe *[]string  `json:"subscribe"`
		Publish   *[]string  `json:"publish"`
		Why       *LocalText `json:"why"`
	}
	if !isJSON(v, "true") {
		if err := decodeStrict(where, v, &o); err != nil {
			return err
		}
	}
	if err := checkWhy(where, o.Why); err != nil {
		return err
	}
	if o.Subscribe == nil && o.Publish == nil {
		if o.Why == nil {
			return fmt.Errorf(`%s: the topic filters it uses ({"subscribe": [...], "publish": [...]}), or "why" it needs any topic`, where)
		}
	} else {
		// one list given: the other direction is not asked for
		p.MQTTSubscribe, p.MQTTPublish = []string{}, []string{}
		if o.Subscribe != nil {
			if err := checkTopics(where+".subscribe", *o.Subscribe); err != nil {
				return err
			}
			p.MQTTSubscribe = slices.Clone(*o.Subscribe)
		}
		if o.Publish != nil {
			if err := checkTopics(where+".publish", *o.Publish); err != nil {
				return err
			}
			p.MQTTPublish = slices.Clone(*o.Publish)
		}
	}
	p.MQTT = true
	p.setWhy("mqtt", o.Why)
	return nil
}

// readPlain: modbus (true or {why}), clu, tftp and serial ({why}, required).
func (p *Permissions) readPlain(name, where string, v json.RawMessage) error {
	var o struct {
		Why *LocalText `json:"why"`
	}
	if !isJSON(v, "true") {
		if err := decodeStrict(where, v, &o); err != nil {
			return err
		}
	}
	if err := checkWhy(where, o.Why); err != nil {
		return err
	}
	if name != "modbus" && o.Why == nil {
		return fmt.Errorf(`%s: {"why": "..."}: the user allows it in the panel and reads why`, where)
	}
	switch name {
	case "modbus":
		p.Modbus = true
	case "clu":
		p.CLU = true
	case "tftp":
		p.TFTP = true
	case "serial":
		p.Serial = true
	}
	p.setWhy(name, o.Why)
	return nil
}

// MarshalJSON writes the permissions as plugin.json declares them.
func (p Permissions) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	obj := func(name string) map[string]any {
		m := map[string]any{}
		if w, ok := p.Why[name]; ok {
			m["why"] = w
		}
		return m
	}
	if len(p.Internet) > 0 {
		m := obj("internet")
		m["hosts"] = []string(p.Internet)
		out["internet"] = m
	}
	if p.LAN {
		m := obj("lan")
		if p.LANPorts != nil {
			m["ports"] = p.LANPorts
		}
		out["lan"] = m
	}
	if p.UDP {
		m := obj("udp")
		m["ports"], m["broadcast"] = nonNilInts(p.UDPPorts), nonNilInts(p.BroadcastPorts)
		out["udp"] = m
	}
	if p.TCP {
		m := obj("tcp")
		m["ports"] = nonNilInts(p.TCPPorts)
		out["tcp"] = m
	}
	if p.MQTT {
		m := obj("mqtt")
		if p.MQTTSubscribe != nil || p.MQTTPublish != nil {
			m["subscribe"], m["publish"] = nonNilStrings(p.MQTTSubscribe), nonNilStrings(p.MQTTPublish)
		}
		out["mqtt"] = m
	}
	for name, on := range map[string]bool{"modbus": p.Modbus, "clu": p.CLU, "tftp": p.TFTP, "serial": p.Serial} {
		if on {
			out[name] = obj(name)
		}
	}
	return json.Marshal(out)
}

func nonNilInts(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// ManifestRequires is "requires": the vclu version the plugin needs, ">=X.Y.Z".
type ManifestRequires struct {
	Vclu string `json:"vclu,omitempty"`
}

var requiresVcluRe = regexp.MustCompile(`^>=\s*(\d+)\.(\d+)\.(\d+)$`)

// minVclu reads ">=X.Y.Z"; ok false when it is not written so.
func (r *ManifestRequires) minVclu() (v [3]int, ok bool) {
	if r == nil || r.Vclu == "" {
		return v, true
	}
	m := requiresVcluRe.FindStringSubmatch(strings.TrimSpace(r.Vclu))
	if m == nil {
		return v, false
	}
	for i := range v {
		var err error
		if v[i], err = strconv.Atoi(m[i+1]); err != nil {
			return v, false
		}
	}
	return v, true
}

// ManifestAction declares an action agents and voice may call (plugin:action names it).
type ManifestAction struct {
	Name        string                 `json:"name"`
	Description LocalText              `json:"description"`
	Params      map[string]ActionParam `json:"params,omitempty"`
	// Confirm: the caller asks the user before running it; plugin code cannot turn it off
	Confirm bool `json:"confirm,omitempty"`
}

// ManifestActions refuses a malformed list, at download and at load.
type ManifestActions []ManifestAction

const (
	maxManifestActions = 32
	maxActionParams    = 32
	maxActionText      = 500
)

var actionParamName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// UnmarshalJSON checks names, texts, params and the count; an unknown field refuses it.
func (a *ManifestActions) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return fmt.Errorf("actions: a list of {name, description, params, confirm}")
	}
	if len(raws) > maxManifestActions {
		return fmt.Errorf("actions: at most %d", maxManifestActions)
	}
	out := make(ManifestActions, 0, len(raws))
	for i, raw := range raws {
		where := fmt.Sprintf("actions[%d]", i)
		var m struct {
			Name        string                     `json:"name"`
			Description *LocalText                 `json:"description"`
			Params      map[string]json.RawMessage `json:"params"`
			Confirm     bool                       `json:"confirm"`
		}
		if err := decodeStrict(where, raw, &m); err != nil {
			return err
		}
		if !ValidActionName(m.Name) {
			return fmt.Errorf("%s: name %q is a lower-case letter, then up to 63 letters, digits and _", where, m.Name)
		}
		if slices.ContainsFunc(out, func(x ManifestAction) bool { return x.Name == m.Name }) {
			return fmt.Errorf("%s: %q twice", where, m.Name)
		}
		where = "actions." + m.Name
		if m.Description == nil || m.Description.In("pl") == "" {
			return fmt.Errorf("%s: a description is required", where)
		}
		if err := checkText(where+".description", *m.Description, maxActionText); err != nil {
			return err
		}
		if len(m.Params) > maxActionParams {
			return fmt.Errorf("%s.params: at most %d", where, maxActionParams)
		}
		act := ManifestAction{Name: m.Name, Description: *m.Description, Confirm: m.Confirm, Params: map[string]ActionParam{}}
		for pname, praw := range m.Params {
			pw := where + ".params." + pname
			if !actionParamName.MatchString(pname) {
				return fmt.Errorf("%s: a name is a letter, then letters, digits and _", pw)
			}
			var pv struct {
				Type        string     `json:"type"`
				Description *LocalText `json:"description"`
				Required    bool       `json:"required"`
			}
			if err := decodeStrict(pw, praw, &pv); err != nil {
				return err
			}
			switch pv.Type {
			case "string", "number", "boolean":
			default:
				return fmt.Errorf("%s.type: \"string\", \"number\" or \"boolean\"", pw)
			}
			ap := ActionParam{Type: pv.Type, Required: pv.Required}
			if pv.Description != nil {
				if err := checkText(pw+".description", *pv.Description, maxActionText); err != nil {
					return err
				}
				ap.Description = *pv.Description
			}
			act.Params[pname] = ap
		}
		out = append(out, act)
	}
	*a = out
	return nil
}

// Names lists the declared actions in order.
func (a ManifestActions) Names() []string {
	out := []string{}
	for _, x := range a {
		out = append(out, x.Name)
	}
	return out
}

func checkText(where string, t LocalText, max int) error {
	for _, s := range t.each() {
		if !plainText(s, max) {
			return fmt.Errorf("%s: up to %d characters on one line", where, max)
		}
	}
	return nil
}

// UnmarshalJSON reads plugin.json: on plugin API 2 its permissions (required, {} for none),
// requires and actions strictly, unknown top-level keys noted for the log (see LoadPlugin).
func (m *PluginManifest) UnmarshalJSON(data []byte) error {
	type plain PluginManifest
	var aux struct {
		*plain
		Permissions json.RawMessage `json:"permissions"`
		Requires    json.RawMessage `json:"requires"`
	}
	aux.plain = (*plain)(m)
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(data, &keys)
	// the API decides how the rest is read: written once, as "api", on any API
	top := topKeys(data)
	if n := countFunc(top, func(k string) bool { return strings.EqualFold(k, "api") }); n > 1 ||
		n == 1 && !slices.Contains(top, "api") {
		return fmt.Errorf(`plugin.json: "api" is written once, in lower case`)
	}
	api := manifestAPI(*m)
	m.Permissions, m.Requires, m.unknownKeys = nil, nil, nil
	for k := range keys {
		if hint, moved := movedManifestKeys[k]; moved && api == 2 {
			return fmt.Errorf("%q moved on plugin API 2: %s", k, hint)
		}
		if !knownManifestKeys[k] && !strings.HasPrefix(k, "x-") {
			m.unknownKeys = append(m.unknownKeys, k)
		}
	}
	sort.Strings(m.unknownKeys)
	if len(aux.Requires) > 0 && !isJSON(aux.Requires, "null") {
		var r ManifestRequires
		if api == 2 {
			if err := decodeStrict("requires", aux.Requires, &r); err != nil {
				return err
			}
			if _, ok := r.minVclu(); !ok {
				return fmt.Errorf(`requires.vclu: %q is written ">=X.Y.Z"`, r.Vclu)
			}
		} else {
			_ = json.Unmarshal(aux.Requires, &r) // plugin API 1: as before, only what it can read
		}
		m.Requires = &r
	}
	if api < 2 || api > PluginAPIMax {
		return nil // API 1 asks by its version; a newer one is refused by its version (checkAPI)
	}
	if _, err := objectEntries("plugin.json", data); err != nil {
		return err
	}
	if len(aux.Permissions) == 0 || isJSON(aux.Permissions, "null") {
		return fmt.Errorf(`plugin API 2 needs "permissions" in plugin.json ({} for none)`)
	}
	var p Permissions
	if err := json.Unmarshal(aux.Permissions, &p); err != nil {
		return err
	}
	m.Permissions = &p
	return m.checkText()
}

// topKeys: every key of the JSON object, twice if written twice; nil for anything else.
func topKeys(v []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(v))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var out []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		key, _ := tok.(string)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return out
		}
		out = append(out, key)
	}
	return out
}

func countFunc[T any](s []T, f func(T) bool) int {
	n := 0
	for _, v := range s {
		if f(v) {
			n++
		}
	}
	return n
}

// checkText: the manifest's name and description as the panel shows them.
func (m PluginManifest) checkText() error {
	if err := checkText("name", m.Name, maxManifestName); err != nil {
		return err
	}
	return checkText("description", m.Description, maxManifestDesc)
}

// UnknownKeys: top-level keys of plugin.json this vclu does not read ("x-" ones left out).
func (m PluginManifest) UnknownKeys() []string { return m.unknownKeys }

var (
	vcluVersionMu sync.RWMutex
	vcluVersion   = [3]int{}
	vcluKnown     bool
)

var vcluVersionRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// SetVcluVersion: the version requires.vclu is checked against ("1.13.0-dev" counts as
// 1.13.0); one that is not X.Y.Z (a "dev" build) checks nothing.
func SetVcluVersion(v string) {
	vcluVersionMu.Lock()
	defer vcluVersionMu.Unlock()
	m := vcluVersionRe.FindStringSubmatch(strings.TrimSpace(v))
	vcluKnown = m != nil
	if m == nil {
		return
	}
	for i := range vcluVersion {
		vcluVersion[i], _ = strconv.Atoi(m[i+1])
	}
}

// checkRequires: this vclu is as new as requires.vclu asks.
func (m PluginManifest) checkRequires() error {
	need, ok := m.Requires.minVclu()
	if !ok || m.Requires == nil || m.Requires.Vclu == "" {
		return nil // plugin API 1 keeps what it could not be read as
	}
	vcluVersionMu.RLock()
	have, known := vcluVersion, vcluKnown
	vcluVersionMu.RUnlock()
	if !known {
		return nil
	}
	for i := range need {
		if have[i] != need[i] {
			if have[i] < need[i] {
				return fmt.Errorf("needs vclu %s, this one is %d.%d.%d: %w", strings.TrimSpace(m.Requires.Vclu), have[0], have[1], have[2], ErrNewerVclu)
			}
			return nil
		}
	}
	return nil
}

// checkFormFile: the wizard's form next to plugin.json reads with this vclu's field types.
func checkFormFile(dir string, m PluginManifest) error {
	if m.Form == "" {
		return nil
	}
	_, _, err := ReadForm(dir)
	return err
}

// Warnings name combinations the install dialog warns about: reading the house and reaching
// the internet ("lan_internet", "mqtt_internet"), full control of a CLU ("clu_tftp"), and a CLU
// with the internet ("clu_internet").
func (p Permissions) Warnings() []string {
	out := []string{}
	internet := len(p.Internet) > 0
	if internet && (p.LAN || p.UDP || p.TCP || p.Modbus) {
		out = append(out, "lan_internet")
	}
	if internet && p.MQTT {
		out = append(out, "mqtt_internet")
	}
	if p.CLU && p.TFTP {
		out = append(out, "clu_tftp")
	}
	if p.CLU && internet {
		out = append(out, "clu_internet")
	}
	return out
}

// Risky lists the permissions only the user allows in the panel.
func (p Permissions) Risky() []string {
	out := []string{}
	for _, n := range []struct {
		name string
		on   bool
	}{{"clu", p.CLU}, {"tftp", p.TFTP}, {"serial", p.Serial}} {
		if n.on {
			out = append(out, n.name)
		}
	}
	return out
}
