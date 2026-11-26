package plugins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Domktorymysli/vclu/pkg/luastr"
)

// DashboardsKey: where the wizard keeps its dashboards step in a plugin's config, only to
// fill the step again; what runs is the code it writes (Plugin:dashboardAccess).
const DashboardsKey = "_dashboards"

const (
	maxManifestDashboards = 8
	maxDashboardName      = 60
	maxDashboardDesc      = 300
	maxDashboardRoles     = 30
)

var (
	dashboardID = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	dashRole    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	dashPIN     = regexp.MustCompile(`^[0-9]{4}$`)
	luaKeywords = map[string]bool{"and": true, "break": true, "do": true, "else": true, "elseif": true, "end": true,
		"false": true, "for": true, "function": true, "goto": true, "if": true, "in": true, "local": true, "nil": true,
		"not": true, "or": true, "repeat": true, "return": true, "then": true, "true": true, "until": true, "while": true}
)

// LocalText is a text or {"pl", "en"}.
type LocalText struct {
	Text string
	PL   string
	EN   string
}

// UnmarshalJSON reads a text or {"pl", "en"} without other keys.
func (t *LocalText) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &t.Text)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("a text or {\"pl\", \"en\"}")
	}
	for k := range m {
		if k != "pl" && k != "en" {
			return fmt.Errorf("language %q (pl, en)", k)
		}
	}
	t.PL, t.EN = m["pl"], m["en"]
	return nil
}

// MarshalJSON writes it back as it came.
func (t LocalText) MarshalJSON() ([]byte, error) {
	if t.PL == "" && t.EN == "" {
		return json.Marshal(t.Text)
	}
	m := map[string]string{}
	if t.PL != "" {
		m["pl"] = t.PL
	}
	if t.EN != "" {
		m["en"] = t.EN
	}
	return json.Marshal(m)
}

// In: the text in lang, else the other one.
func (t LocalText) In(lang string) string {
	if t.Text != "" {
		return t.Text
	}
	if lang == "en" && t.EN != "" || t.PL == "" {
		return t.EN
	}
	return t.PL
}

func (t LocalText) each() []string { return []string{t.Text, t.PL, t.EN} }

// ManifestDashboard: a dashboard the plugin declares in plugin.json; who may open it is the
// user's to set (Plugin:dashboardAccess in a module, written by the wizard).
type ManifestDashboard struct {
	ID          string     `json:"id"`
	Name        LocalText  `json:"name"`
	Description *LocalText `json:"description,omitempty"`
}

// ManifestDashboards refuses a manifest with a malformed list, at download and at load.
type ManifestDashboards []ManifestDashboard

// UnmarshalJSON checks ids, names and the count; a field this vclu does not know refuses it.
func (d *ManifestDashboards) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return fmt.Errorf("dashboards: %w", err)
	}
	if len(raws) > maxManifestDashboards {
		return fmt.Errorf("dashboards: at most %d", maxManifestDashboards)
	}
	list := make([]ManifestDashboard, len(raws))
	seen := map[string]bool{}
	for i, raw := range raws {
		at := fmt.Sprintf("dashboards[%d]", i)
		if err := decodeStrict(at, raw, &list[i]); err != nil {
			return err
		}
		m := list[i]
		if !dashboardID.MatchString(m.ID) {
			return fmt.Errorf("%s: id %q is 1 to 64 lower-case letters, digits and _", at, m.ID)
		}
		if seen[m.ID] {
			return fmt.Errorf("%s: id %q twice", at, m.ID)
		}
		seen[m.ID] = true
		if m.Name.In("pl") == "" {
			return fmt.Errorf("%s: a name is required", at)
		}
		for _, s := range m.Name.each() {
			if !plainText(s, maxDashboardName) {
				return fmt.Errorf("%s: a name is up to %d characters on one line", at, maxDashboardName)
			}
		}
		if m.Description != nil {
			for _, s := range m.Description.each() {
				if !plainText(s, maxDashboardDesc) {
					return fmt.Errorf("%s: a description is up to %d characters on one line", at, maxDashboardDesc)
				}
			}
		}
	}
	*d = list
	return nil
}

// plainText: valid UTF-8, no control characters, at most n characters.
func plainText(s string, n int) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > n {
		return false
	}
	// format characters (right-to-left override, zero width) could hide what the text says
	return !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
}

// RealDashboardID: the dashboard's id at /d/, "<shortId with - as _>_<id>" (PluginSandbox).
func RealDashboardID(pluginID, id string) string {
	_, short := parseFullId(pluginID)
	return strings.ReplaceAll(short, "-", "_") + "_" + id
}

// Dashboards: the dashboards the installed plugin declares.
func (l *Loader) Dashboards(pluginID string) ([]ManifestDashboard, error) {
	m, err := l.diskManifest(pluginID)
	if err != nil {
		return nil, err
	}
	return m.Dashboards, nil
}

// DashboardAccess is the wizard's dashboards step for one dashboard.
type DashboardAccess struct {
	Name      string   `json:"name"`
	Auth      string   `json:"auth"` // session, pin, public, off
	PIN       string   `json:"pin,omitempty"`
	Remote    bool     `json:"remote"`
	RemotePIN bool     `json:"remotePin"`
	Roles     []string `json:"roles"`
}

// DefaultDashboardAccess: what the wizard offers before the user chose: the plugin's own.
func DefaultDashboardAccess(m ManifestDashboard) DashboardAccess {
	return DashboardAccess{Name: m.Name.In("pl"), Auth: "session", Roles: []string{}}
}

// SavedDashboardAccess: the step as saved in the config, the defaults for the rest.
func SavedDashboardAccess(declared []ManifestDashboard, cfg map[string]any) map[string]DashboardAccess {
	out := map[string]DashboardAccess{}
	var saved map[string]json.RawMessage
	if raw, err := json.Marshal(cfg[DashboardsKey]); err == nil {
		_ = json.Unmarshal(raw, &saved)
	}
	for _, m := range declared {
		a := DefaultDashboardAccess(m)
		if raw, ok := saved[m.ID]; ok {
			var s DashboardAccess
			if json.Unmarshal(raw, &s) == nil {
				a = s
				if a.Roles == nil {
					a.Roles = []string{}
				}
			}
		}
		out[m.ID] = a
	}
	return out
}

// CheckDashboardAccess checks the step against the declared dashboards; errors by field
// ("lista.pin"), nil when it is fine.
func CheckDashboardAccess(declared []ManifestDashboard, in map[string]json.RawMessage) (map[string]DashboardAccess, map[string]string) {
	out := map[string]DashboardAccess{}
	bad := map[string]string{}
	for id := range in {
		if !slices.ContainsFunc(declared, func(m ManifestDashboard) bool { return m.ID == id }) {
			bad[id] = "not a dashboard of this plugin"
		}
	}
	for _, m := range declared {
		raw, given := in[m.ID]
		if !given {
			continue
		}
		var req struct {
			Name      *string `json:"name"`
			Auth      string  `json:"auth"`
			PIN       string  `json:"pin"`
			Remote    bool    `json:"remote"`
			RemotePIN bool    `json:"remotePin"`
			Roles     any     `json:"roles"`
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			bad[m.ID] = "invalid: " + err.Error()
			continue
		}
		a := DashboardAccess{Auth: req.Auth, Remote: req.Remote, RemotePIN: req.RemotePIN, Roles: []string{}}
		field := func(name, msg string) { bad[m.ID+"."+name] = msg }
		a.Name = m.Name.In("pl")
		if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
			a.Name = strings.TrimSpace(*req.Name)
		}
		if !plainText(a.Name, maxDashboardName) {
			field("name", fmt.Sprintf("up to %d characters on one line", maxDashboardName))
		}
		switch a.Auth {
		case "session", "pin", "public", "off":
		default:
			field("auth", "session, pin, public or off")
		}
		roles, err := roleList(req.Roles)
		if err != "" {
			field("roles", err)
		}
		if a.Auth == "off" { // not served at all: nothing else applies
			out[m.ID] = DashboardAccess{Name: a.Name, Auth: "off", Roles: []string{}}
			continue
		}
		if a.Remote {
			a.Roles = roles
		} else {
			a.RemotePIN = false
		}
		if a.Auth == "pin" || a.RemotePIN {
			a.PIN = strings.TrimSpace(req.PIN)
			if !dashPIN.MatchString(a.PIN) {
				field("pin", "4 digits")
			}
		}
		out[m.ID] = a
	}
	if len(bad) > 0 {
		return nil, bad
	}
	return out, nil
}

// roleList: roles as a list or a comma-separated text, checked as the cloud takes them.
func roleList(v any) ([]string, string) {
	var parts []string
	switch r := v.(type) {
	case nil:
	case string:
		parts = strings.Split(r, ",")
	case []any:
		for _, x := range r {
			s, ok := x.(string)
			if !ok {
				return nil, "roles are text"
			}
			parts = append(parts, s)
		}
	default:
		return nil, "roles are a list"
	}
	out := []string{}
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || slices.Contains(out, p) {
			continue
		}
		if !dashRole.MatchString(p) {
			return nil, fmt.Sprintf("%q: a-z, 0-9, - and _, up to 32", p)
		}
		out = append(out, p)
	}
	if len(out) > maxDashboardRoles {
		return nil, fmt.Sprintf("at most %d roles", maxDashboardRoles)
	}
	return out, ""
}

// DashboardAccessCode: the module lines that grant the step's access, "" with nothing saved.
// Every value goes in quoted; names and roles were checked on save and are checked again here.
func DashboardAccessCode(pluginID string, declared []ManifestDashboard, access map[string]DashboardAccess) string {
	var lines []string
	ids := make([]string, 0, len(access)) // in the manifest's order
	for _, m := range declared {
		if _, ok := access[m.ID]; ok {
			ids = append(ids, m.ID)
		}
	}
	for _, id := range ids {
		a := access[id]
		parts := []string{"name = " + luastr.Quote(a.Name), "auth = " + luastr.Quote(a.Auth)}
		if a.PIN != "" && dashPIN.MatchString(a.PIN) {
			parts = append(parts, "pin = "+luastr.Quote(a.PIN))
		}
		if a.Auth != "off" && a.Remote {
			roles := make([]string, 0, len(a.Roles))
			for _, r := range a.Roles {
				if dashRole.MatchString(r) {
					roles = append(roles, luastr.Quote(r))
				}
			}
			rl := "{}"
			if len(roles) > 0 {
				rl = "{ " + strings.Join(roles, ", ") + " }"
			}
			parts = append(parts, fmt.Sprintf("remote = { pin = %t, roles = %s }", a.RemotePIN, rl))
		}
		// one option per line: the user and agents read and change this code
		lines = append(lines, fmt.Sprintf("    %s:dashboardAccess(%s, {\n        %s,\n    })",
			accessVar(pluginID), luastr.Quote(id), strings.Join(parts, ",\n        ")))
	}
	if len(lines) == 0 {
		return ""
	}
	v := accessVar(pluginID)
	return "-- Dostęp do dashboardów (kreator pluginu): auth \"session\", \"pin\", \"public\" albo \"off\";\n" +
		"-- remote: w aplikacji vclu, roles z chmury (pusto: wszyscy domownicy). Zmieniaj śmiało.\n" +
		"local " + v + " = Plugin.getPlugin(" + luastr.Quote(pluginID) + ")\n" +
		"if " + v + " then\n" + strings.Join(lines, "\n") + "\nend\n"
}

// accessVar: a Lua name for the plugin in the generated block.
func accessVar(pluginID string) string {
	_, short := parseFullId(pluginID)
	v := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, short)
	if v == "" || v[0] >= '0' && v[0] <= '9' || luaKeywords[v] {
		v = "plugin_" + v
	}
	return v
}
