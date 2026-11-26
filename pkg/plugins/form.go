package plugins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DevicesKey: where the confirmed device rows sit in a plugin's config.
const DevicesKey = "devices"

const (
	maxFormFields = 64
	maxDeviceRows = 500
	maxFormValue  = 4096
	maxFormFile   = 256 << 10
)

// Form is a plugin's configuration wizard: settings (step 1) and devices (step 2).
type Form struct {
	Settings map[string]FormField `json:"settings"`
	Devices  *DeviceList          `json:"devices,omitempty"`
	// Code: the plugin writes a module (plugin:moduleCode), shown as the last step
	Code bool `json:"code,omitempty"`
}

// DeviceList: the rows of step 2, filled by hand or by the plugin's discover.
type DeviceList struct {
	Discover bool                 `json:"discover,omitempty"`
	Row      string               `json:"row,omitempty"`
	Max      int                  `json:"max,omitempty"`
	Fields   map[string]FormField `json:"fields"`
}

// FormField is one field of the form.
type FormField struct {
	Type        string          `json:"type"`
	Label       json.RawMessage `json:"label,omitempty"`
	Description json.RawMessage `json:"description,omitempty"`
	Required    bool            `json:"required,omitempty"`
	Default     any             `json:"default,omitempty"`
	Min         *float64        `json:"min,omitempty"`
	Max         *float64        `json:"max,omitempty"`
	Step        *float64        `json:"step,omitempty"`
	Unit        string          `json:"unit,omitempty"`
	Options     []string        `json:"options,omitempty"`
	Enum        []string        `json:"enum,omitempty"` // the older config: "type": "string" with "enum"
	// OptionLabels: what the panel shows for an option, text or {"pl", "en"}, by its value
	OptionLabels map[string]json.RawMessage `json:"optionLabels,omitempty"`
	ShowIf       map[string]any             `json:"showIf,omitempty"`
	ObjectTypes  []string                   `json:"objectTypes,omitempty"`
	Readonly     bool                       `json:"readonly,omitempty"`
}

var formFieldName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

var formTypes = map[string]bool{
	"string": true, "number": true, "boolean": true, "enum": true,
	"secret": true, "host": true, "object": true,
}

func (f FormField) options() []string {
	if len(f.Options) > 0 {
		return f.Options
	}
	return f.Enum
}

func (f FormField) kind() string {
	if f.Type == "string" && len(f.Enum) > 0 {
		return "enum"
	}
	return f.Type
}

// ReadForm reads the wizard of an installed plugin: the file plugin.json points to with
// "form", else its older "config" map as the settings. raw is what the panel draws (the
// file as written, so the fields keep their order); nil when the plugin has no form.
func ReadForm(localDir string) (form *Form, raw json.RawMessage, err error) {
	data, err := os.ReadFile(filepath.Join(localDir, "plugin.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("plugin.json: %w", err)
	}
	var m struct {
		Form   string          `json:"form"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil, fmt.Errorf("plugin.json: %w", err)
	}
	switch {
	case m.Form != "":
		if !validPathName(m.Form) || strings.Contains(m.Form, "/") {
			return nil, nil, fmt.Errorf("plugin.json: form %q is not a file name", m.Form)
		}
		raw, err = os.ReadFile(filepath.Join(localDir, m.Form))
		if err != nil {
			return nil, nil, fmt.Errorf("form: %w", err)
		}
		if len(raw) > maxFormFile {
			return nil, nil, fmt.Errorf("form: %s is larger than %d KiB", m.Form, maxFormFile>>10)
		}
	case len(bytes.TrimSpace(m.Config)) > 0 && !bytes.Equal(bytes.TrimSpace(m.Config), []byte("null")):
		raw = json.RawMessage(`{"settings":` + string(m.Config) + `}`)
	default:
		return nil, nil, nil
	}
	form, err = ParseForm(raw)
	if err != nil {
		return nil, nil, err
	}
	return form, raw, nil
}

// ParseForm reads and checks a form file.
func ParseForm(raw []byte) (*Form, error) {
	var f Form
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("form: %w", err)
	}
	if err := checkFields("settings", f.Settings, false); err != nil {
		return nil, err
	}
	if _, taken := f.Settings[DevicesKey]; taken && f.Devices != nil {
		return nil, fmt.Errorf("form: settings.%s: the name is taken by the device list", DevicesKey)
	}
	if f.Devices != nil {
		if len(f.Devices.Fields) == 0 {
			return nil, fmt.Errorf("form: devices: no fields")
		}
		if err := checkFields("devices", f.Devices.Fields, true); err != nil {
			return nil, err
		}
		if f.Devices.Max < 0 || f.Devices.Max > maxDeviceRows {
			return nil, fmt.Errorf("form: devices.max: 0 to %d", maxDeviceRows)
		}
	}
	return &f, nil
}

func checkFields(where string, fields map[string]FormField, rows bool) error {
	if len(fields) > maxFormFields {
		return fmt.Errorf("form: %s: more than %d fields", where, maxFormFields)
	}
	for name, f := range fields {
		at := where + "." + name
		if !formFieldName.MatchString(name) {
			return fmt.Errorf("form: %s: a name is a letter, then letters, digits or _", at)
		}
		if !formTypes[f.Type] {
			return fmt.Errorf("form: %s: type %q unknown to this vclu (string, number, boolean, enum, secret, host, object): %w", at, f.Type, ErrNewerVclu)
		}
		if f.kind() == "enum" && len(f.options()) == 0 {
			return fmt.Errorf("form: %s: an enum needs options", at)
		}
		for opt := range f.OptionLabels {
			if !slices.Contains(f.options(), opt) {
				return fmt.Errorf("form: %s: optionLabels names %q, not an option", at, opt)
			}
		}
		if rows && f.Type == "secret" {
			return fmt.Errorf("form: %s: a device field cannot be a secret", at)
		}
		for other := range f.ShowIf {
			if _, ok := fields[other]; !ok || other == name {
				return fmt.Errorf("form: %s: showIf names %q, not another field here", at, other)
			}
		}
	}
	return nil
}

// FormValues: what a submitted form leaves after checking.
type FormValues struct {
	Config  map[string]any    // settings without secrets, and the device rows
	Secrets map[string]string // the secrets the user set or changed; "" clears one
	Hosts   []string          // addresses from host fields, for the lan grant
}

// Check validates a submitted form against the plugin's declaration. A secret left out
// keeps its stored value.
func (f *Form) Check(settings map[string]any, devices []map[string]any) (FormValues, error) {
	v := FormValues{Config: map[string]any{}, Secrets: map[string]string{}}
	hosts := map[string]bool{}
	for name := range settings {
		if _, ok := f.Settings[name]; !ok {
			return v, fmt.Errorf("%s: not in the form", name)
		}
	}
	shown := visibleFields(f.Settings, settings)
	for name, field := range f.Settings {
		val, given := settings[name]
		if field.Type == "secret" {
			if !given || val == nil {
				continue
			}
			s, ok := val.(string)
			if !ok || len(s) > maxFormValue {
				return v, fmt.Errorf("%s: a secret is text", name)
			}
			v.Secrets[name] = s
			continue
		}
		if !shown[name] {
			continue
		}
		out, err := checkValue(field, val, given)
		if err != nil {
			return v, fmt.Errorf("%s: %w", name, err)
		}
		if out != nil {
			v.Config[name] = out
			if field.Type == "host" {
				hosts[out.(string)] = true
			}
		}
	}
	if f.Devices != nil {
		limit := f.Devices.Max
		if limit == 0 {
			limit = maxDeviceRows
		}
		if len(devices) > limit {
			return v, fmt.Errorf("devices: more than %d", limit)
		}
		rows := make([]any, 0, len(devices))
		for i, row := range devices {
			clean, err := f.Devices.checkRow(row)
			if err != nil {
				return v, fmt.Errorf("device %d: %w", i+1, err)
			}
			for name, field := range f.Devices.Fields {
				if s, ok := clean[name].(string); ok && field.Type == "host" {
					hosts[s] = true
				}
			}
			rows = append(rows, clean)
		}
		v.Config[DevicesKey] = rows
	} else if len(devices) > 0 {
		return v, fmt.Errorf("devices: the plugin has no device list")
	}
	for h := range hosts {
		v.Hosts = append(v.Hosts, h)
	}
	sort.Strings(v.Hosts)
	return v, nil
}

func (d *DeviceList) checkRow(row map[string]any) (map[string]any, error) {
	clean := map[string]any{}
	for name := range row {
		if _, ok := d.Fields[name]; !ok {
			return nil, fmt.Errorf("%s: not a device field", name)
		}
	}
	shown := visibleFields(d.Fields, row)
	for name, field := range d.Fields {
		if !shown[name] {
			continue
		}
		val, given := row[name]
		out, err := checkValue(field, val, given)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if out != nil {
			clean[name] = out
		}
	}
	return clean, nil
}

// DiscoveredRows keeps of what a plugin's discover returned only rows that pass the
// device fields; a bad row is dropped with the reason.
func (d *DeviceList) DiscoveredRows(rows []map[string]any) (ok []map[string]any, dropped []string) {
	for i, row := range rows {
		if len(ok) >= maxDeviceRows {
			dropped = append(dropped, fmt.Sprintf("more than %d rows", maxDeviceRows))
			break
		}
		known := map[string]any{}
		for k, v := range row {
			if _, declared := d.Fields[k]; declared {
				known[k] = v
			}
		}
		clean, err := d.checkRow(known)
		if err != nil {
			dropped = append(dropped, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		ok = append(ok, clean)
	}
	return ok, dropped
}

// hiddenRune: control, line separator and bidi characters, which a field or a diff in the
// panel does not show as they act; a tab is allowed.
func hiddenRune(r rune) bool {
	switch {
	case r == '\t':
		return false
	case unicode.IsControl(r), r == '\u2028', r == '\u2029', r == '\u061c', r == '\u200e', r == '\u200f',
		r >= '\u202a' && r <= '\u202e', r >= '\u2066' && r <= '\u2069':
		return true
	}
	return false
}

// visibleFields: the fields whose showIf holds, a field hidden by a hidden one hidden too (the
// panel counts a hidden field as unset), so neither required nor kept.
func visibleFields(fields map[string]FormField, values map[string]any) map[string]bool {
	on := map[string]bool{}
	for name := range fields {
		on[name] = true
	}
	for changed := true; changed; {
		changed = false
		for name, f := range fields {
			if on[name] && !showIfHolds(f, values, on) {
				on[name], changed = false, true
			}
		}
	}
	return on
}

func showIfHolds(f FormField, values map[string]any, on map[string]bool) bool {
	for other, want := range f.ShowIf {
		var v any
		if on[other] {
			v = values[other]
		}
		if fmt.Sprint(v) != fmt.Sprint(want) {
			return false
		}
	}
	return true
}

func checkValue(f FormField, val any, given bool) (any, error) {
	if !given || val == nil || val == "" {
		if f.Default != nil {
			val = f.Default
		} else if f.Required {
			return nil, fmt.Errorf("required")
		} else {
			return nil, nil
		}
	}
	switch f.kind() {
	case "number":
		n, ok := toNumber(val)
		if !ok {
			return nil, fmt.Errorf("not a number")
		}
		if f.Min != nil && n < *f.Min {
			return nil, fmt.Errorf("less than %v", *f.Min)
		}
		if f.Max != nil && n > *f.Max {
			return nil, fmt.Errorf("more than %v", *f.Max)
		}
		return n, nil
	case "boolean":
		switch b := val.(type) {
		case bool:
			return b, nil
		case string:
			if b == "true" || b == "false" {
				return b == "true", nil
			}
		}
		return nil, fmt.Errorf("not true or false")
	}
	s, ok := val.(string)
	if !ok {
		if n, isNum := val.(float64); isNum && f.kind() != "enum" {
			s = fmt.Sprint(n)
		} else {
			return nil, fmt.Errorf("not text")
		}
	}
	s = strings.TrimSpace(s)
	if len(s) > maxFormValue {
		return nil, fmt.Errorf("longer than %d characters", maxFormValue)
	}
	// one line, as the panel's inputs: plugins put these values into module code
	if !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool { return r == '\n' || hiddenRune(r) }) {
		return nil, fmt.Errorf("control characters")
	}
	switch f.kind() {
	case "enum":
		for _, o := range f.options() {
			if o == s {
				return s, nil
			}
		}
		return nil, fmt.Errorf("%q is not one of %s", s, strings.Join(f.options(), ", "))
	case "host":
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address", s)
		}
		return a.Unmap().String(), nil
	}
	return s, nil
}

func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case int:
		return float64(n), true
	case string:
		var f float64
		if _, err := fmt.Sscan(strings.TrimSpace(n), &f); err == nil {
			return f, !math.IsNaN(f) && !math.IsInf(f, 0)
		}
	}
	return 0, false
}

// RowKey: the readonly field that tells discovered rows apart, "" when there is none.
func (d *DeviceList) RowKey() string {
	names := make([]string, 0, len(d.Fields))
	for name, f := range d.Fields {
		if f.Readonly {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
