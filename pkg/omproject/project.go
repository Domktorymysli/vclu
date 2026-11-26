// Package omproject reads an Object Manager project: the .omp file, or the
// project zip a diagnostic pack carries. From system.xml it takes the CLUs,
// modules, objects, peripherals and event bindings; from scripts/ the CLU
// scripts; from properties.xml the OM version and nothing else (the file
// holds the project's keys).
//
// The format changes between OM versions, so the reader picks the elements it
// needs, follows XStream references, and reports what it had to skip in
// Project.Warnings instead of failing.
package omproject

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/dashspec"
)

// Limits on what is unpacked from the archive.
const (
	maxSystemXML = 32 << 20
	maxScript    = 1 << 20
	maxScripts   = 8 << 20
	// maxCommands caps the event commands of a project: XStream lets many
	// objects reference one list, so a small system.xml can name the same
	// commands many times over. A house with six CLUs has 4 KB of them.
	maxCommands = 4 << 20
	maxCLUs     = 256
	// MaxWarnings caps the warnings kept; the rest are only counted.
	MaxWarnings = 100
)

// Project is what vCLU takes from an Object Manager project.
type Project struct {
	Version  string // OM version from properties.xml, e.g. "1.8.0_2301"
	CLUs     []CLU
	Warnings []string
	// WarningsOmitted counts the warnings over MaxWarnings, left out of Warnings.
	WarningsOmitted int
	// Skipped are the IDs of CLUs the project has and vCLU could not read: an
	// import keeps what it stored of them.
	Skipped []string
	// Interfaces are the myGrenton interfaces designed in OM (nodes/<id>/data.json).
	Interfaces []dashspec.Interface

	commands int // bytes of event commands read so far, up to maxCommands
	overCmds bool
	known    map[string]bool // what calls of an interface may name (inProject)
	taken    map[string]bool // interface and page IDs already read
}

// CLU is one CLU of the project.
type CLU struct {
	ID          string // name on the CLU, e.g. "CLU221000001"
	Name        string // project name, e.g. "Home"
	IP          string
	FwType      string
	FwVersion   string
	HwType      string
	HwVersion   string
	Gate        bool
	Modules     []Module
	Objects     []Object
	Peripherals []Peripheral
	Events      []Binding // the CLU's own events (OnInit, ...)
	Scripts     []Script
}

// Module is a module on the CLU's bus.
type Module struct {
	Serial    uint64 // module ID, e.g. 330000001
	InnerName string // its address as OM writes it, e.g. "13ab6681"
	Type      string // module class in hex, e.g. "1e"
}

// Object is an input or output of a module.
type Object struct {
	ID     string // name on the CLU, e.g. "DOU0001"
	Name   string
	Type   string // e.g. "DOUT"
	Class  int    // object class in om.lua
	Module uint64 // serial of its module
	Index  int
	Lamel  string // ROLLER_SHUTTER LamelMoveTimeout (feature 10), "" when not set
	Events []Binding
}

// Peripheral is a virtual object of the CLU (thermostat, scheduler, ...).
type Peripheral struct {
	ID     string
	Name   string
	Type   string // OM type name, e.g. "Thermostat"
	Class  int    // object class in om.lua, 0 when vCLU does not know the type
	Events []Binding
	// Features set in the project (index -> initValue), read for Modbus objects only
	Features map[int]string
}

// Binding is an event with the commands OM runs on it.
type Binding struct {
	Event    string
	Index    int
	Commands []string // in OM's notation: "Home->light->SwitchOn(0)"
}

// Script is a CLU script.
type Script struct {
	Name   string
	Params []string
	Source string
}

// classROLLER is the class of a roller shutter with position in om.lua.
const classROLLER = 24

// peripheralClasses are the om.lua classes of OM's peripheral types, from
// Object Manager's device interfaces (object_*.xml).
var peripheralClasses = map[string]int{
	"Timer":                 6,
	"PIDcontroller":         14,
	"Calendar":              15,
	"Scheduler":             16,
	"Thermostat":            18,
	"Push":                  31,
	"SunriseSunsetCalendar": 39,
	"PresenceSensor":        40,
	"EventScheduler":        43,
	"MultiFanACThermostat":  46,
	"Statistics":            47,
	"StatisticsPulse":       48,
	// A GATE Alarm's alarm objects (clu_GATE_ALARM_ft00000002_*)
	"SatelZone":        100,
	"SatelInput":       101,
	"SatelOutput":      102,
	"Satel":            103,
	"Jablotron":        150,
	"JablotronSection": 151,
	"JablotronOutput":  152,
	"JablotronDevice":  153,
	// an HTTP GATE's
	"HttpListener": 120,
	"HttpRequest":  121,
	// a GATE MODBUS's (object_modbus_*.xml); their features go to om.lua for the Modbus map
	"Modbus":               200,
	"ModbusValue":          201,
	"ModbusSlaveConfigRTU": 202,
	"ModbusRTU":            203,
	"ModbusSlaveRTU":       204,
	"ModbusClient":         205,
	"ModbusServer":         206,
}

// isModbus tells whether an om.lua class is a GATE MODBUS's Modbus object.
func isModbus(class int) bool {
	return class >= 200 && class <= 206
}

// Patterns of what goes into om.lua: anything else is left out, so a value from
// the project cannot add lines or code to it. Names are what Object Manager
// allows; vCLU makes Lua identifiers of them.
var (
	reCLUID = regexp.MustCompile(`^CLU\d+$`)
	reObjID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	reHex   = regexp.MustCompile(`^[0-9A-Fa-f]{1,8}$`)
	reName  = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

	reVersion = regexp.MustCompile(`^[0-9A-Za-z._-]{1,40}$`)
)

// zwaveHardware are the hardware types of CLU Z-Wave; every other CLU is a
// GATE in om.lua.
var zwaveHardware = map[string]bool{"00000000": true, "00000013": true}

// IsProject tells whether a zip archive is an Object Manager project.
func IsProject(z *zip.Reader) bool {
	for _, f := range z.File {
		if f.Name == "system.xml" {
			return true
		}
	}
	return false
}

// Read reads a project from the bytes of its zip archive.
func Read(data []byte) (*Project, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a zip archive: %w", err)
	}
	return ReadZip(z)
}

// ReadZip reads a project from an open zip archive.
func ReadZip(z *zip.Reader) (*Project, error) {
	files := map[string]*zip.File{}
	for _, f := range z.File {
		files[f.Name] = f
	}
	sys := files["system.xml"]
	if sys == nil {
		return nil, errors.New("system.xml is missing: not an Object Manager project")
	}
	p := &Project{}

	if f := files["properties.xml"]; f != nil {
		p.Version = readVersion(f)
	}
	if p.Version != "" && !reVersion.MatchString(p.Version) {
		p.warn(fmt.Sprintf("properties.xml: OM version %q is not a version, left out", p.Version))
		p.Version = ""
	} else if p.Version == "" {
		p.warn("properties.xml has no OM version")
	}

	rc, err := sys.Open()
	if err != nil {
		return nil, fmt.Errorf("system.xml: %w", err)
	}
	lr := &io.LimitedReader{R: rc, N: maxSystemXML + 1}
	s, err := parseStream(lr)
	_ = rc.Close()
	if lr.N == 0 {
		return nil, fmt.Errorf("system.xml: %w: over %d MB", errTooLarge, maxSystemXML>>20)
	}
	if err != nil {
		return nil, fmt.Errorf("system.xml: %w", err)
	}
	p.readCLUs(s)
	if err := s.over(); err != nil {
		return nil, fmt.Errorf("system.xml: %w", err)
	}
	if len(p.CLUs) > maxCLUs {
		return nil, fmt.Errorf("system.xml: %w: over %d CLUs", errTooLarge, maxCLUs)
	}
	if len(p.CLUs) == 0 {
		return nil, errors.New("system.xml has no CLU")
	}
	if s.dangling > 0 {
		p.warn(fmt.Sprintf("system.xml: %d references to objects it does not contain, left out", s.dangling))
	}
	p.readScripts(files)
	p.readInterfaces(files)
	return p, nil
}

func (p *Project) warn(w string) {
	if len(p.Warnings) >= MaxWarnings {
		p.WarningsOmitted++
		return
	}
	p.Warnings = append(p.Warnings, w)
}

// CapWarnings keeps the first MaxWarnings of list and says how many more there
// are, omitted of them counted before.
func CapWarnings(list []string, omitted int) []string {
	if len(list) > MaxWarnings {
		omitted += len(list) - MaxWarnings
		list = list[:MaxWarnings:MaxWarnings]
	}
	if omitted > 0 {
		list = append(list, fmt.Sprintf("... and %d more", omitted))
	}
	return list
}

// atoi reads a count or an index: a number that is not negative.
func atoi(s string) (int, error) {
	i, err := strconv.Atoi(s)
	if err == nil && i < 0 {
		err = fmt.Errorf("%d is negative", i)
	}
	return i, err
}

// checkName warns about a name vCLU cannot use in Lua; om.lua leaves it out
// and the object goes by its ID.
func (p *Project) checkName(what, name string) {
	if name != "" && !reName.MatchString(name) {
		p.warn(fmt.Sprintf("%s: name %q has characters other than letters, digits and _, the ID is used instead", what, name))
	}
}

// luaName is the name om.lua gives an object: its project name, or its ID when
// the name is no Lua name.
func luaName(name, id string) string {
	if reName.MatchString(name) {
		return name
	}
	return id
}

// readVersion takes the OM version from properties.xml and drops the rest:
// the file also holds the project's cipher keys and Wi-Fi password.
func readVersion(f *zip.File) string {
	rc, err := f.Open()
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()
	d := xml.NewDecoder(io.LimitReader(rc, 1<<20))
	var version, ext string
	depth := 0
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth != 3 {
				continue
			}
			var v string
			switch t.Name.Local {
			case "version":
				if d.DecodeElement(&v, &t) == nil {
					version = strings.TrimSpace(v)
				}
				depth--
			case "versionExt":
				if d.DecodeElement(&v, &t) == nil {
					ext = strings.TrimSpace(v)
				}
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}
	if ext != "" {
		return ext
	}
	return version
}

// readCLUs finds every CLU, wherever XStream first spelled it out.
func (p *Project) readCLUs(s *stream) {
	seen := map[*node]bool{}
	ids, skipped := map[string]bool{}, map[string]bool{}
	s.root.walk(func(n *node) {
		if n.ref != "" || seen[n] || s.over() != nil || len(p.CLUs) > maxCLUs {
			return
		}
		if s.child(n, "nameOnCLU") == nil {
			return
		}
		if s.child(n, "iosList") == nil || s.child(n, "modulesList") == nil {
			// Peripherals have a name on the CLU too, never a CLU's.
			if id := s.text(n, "nameOnCLU"); reCLUID.MatchString(id) && s.text(n, "isRemoved") != "true" {
				seen[n] = true
				skipped[id] = true
				p.warn(fmt.Sprintf("%s: a CLU without its list of inputs and outputs or of modules, left out", id))
			}
			return
		}
		seen[n] = true
		if s.text(n, "isRemoved") == "true" {
			return
		}
		id := s.text(n, "nameOnCLU")
		if !reCLUID.MatchString(id) {
			p.warn(fmt.Sprintf("a CLU named %q on the CLU, left out", id))
			return
		}
		if ids[id] {
			p.warn(fmt.Sprintf("%s: the CLU is in the project twice, left out the second time", id))
			return
		}
		ids[id] = true
		p.CLUs = append(p.CLUs, p.readCLU(s, n))
	})
	// a CLU read in full elsewhere is not skipped
	for id := range skipped {
		if !ids[id] {
			p.Skipped = append(p.Skipped, id)
		}
	}
	sort.Strings(p.Skipped)
}

func (p *Project) readCLU(s *stream, n *node) CLU {
	c := CLU{
		ID:        s.text(n, "nameOnCLU"),
		Name:      s.text(n, "name"),
		IP:        s.text(n, "ipAddress"),
		FwType:    s.text(n, "firmwareType"),
		FwVersion: s.text(n, "firmwareVersion"),
		HwType:    s.text(n, "hardwareType"),
		HwVersion: s.text(n, "hardwareVersion"),
	}
	label := c.Name + " (" + c.ID + ")"
	p.checkName(label, c.Name)
	for _, v := range []*string{&c.FwType, &c.FwVersion, &c.HwType, &c.HwVersion} {
		if *v != "" && !reHex.MatchString(*v) {
			p.warn(fmt.Sprintf("%s: firmware or hardware value %q is not a hex number, left out", label, *v))
			*v = ""
		}
	}
	if c.FwType == "" || c.HwType == "" {
		p.warn(label + ": firmware or hardware type missing")
	}
	c.Gate = c.HwType != "" && !zwaveHardware[c.HwType]
	c.Events = p.readEvents(s, n, "eventsList", label)

	used := map[uint64]bool{}
	ids := map[string]bool{}
	for _, io := range s.items(n, "iosList") {
		o, ok := p.readObject(s, io, label)
		if !ok {
			continue
		}
		if ids[o.ID] {
			p.warn(fmt.Sprintf("%s: %s is in the list twice, left out the second time", label, o.ID))
			continue
		}
		ids[o.ID] = true
		used[o.Module] = true
		c.Objects = append(c.Objects, o)
	}
	for _, m := range s.items(n, "modulesList") {
		serial, err := strconv.ParseUint(s.text(m, "moduleID"), 10, 64)
		if err != nil {
			p.warn(label + ": a module without a valid moduleID, left out")
			continue
		}
		if !used[serial] || s.text(m, "active") == "false" {
			continue
		}
		if !reHex.MatchString(s.text(m, "innerName")) || !reHex.MatchString(s.text(m, "moduleClass")) {
			p.warn(fmt.Sprintf("%s: module %d has no valid address or class, left out", label, serial))
			continue
		}
		c.Modules = append(c.Modules, Module{
			Serial:    serial,
			InnerName: s.text(m, "innerName"),
			Type:      s.text(m, "moduleClass"),
		})
	}
	for _, pn := range s.items(n, "peripheryList") {
		if s.text(pn, "isRemoved") == "true" {
			continue
		}
		per := Peripheral{
			ID:   s.text(pn, "nameOnCLU"),
			Name: s.text(pn, "name"),
			Type: s.text(pn, "typeName"),
		}
		if !reObjID.MatchString(per.ID) {
			p.warn(label + ": a peripheral without a name on the CLU, left out")
			continue
		}
		if ids[per.ID] {
			p.warn(fmt.Sprintf("%s: %s is in the list twice, left out the second time", label, per.ID))
			continue
		}
		ids[per.ID] = true
		p.checkName(label+": peripheral "+per.ID, per.Name)
		per.Class = peripheralClasses[per.Type]
		if per.Class == 0 {
			p.warn(fmt.Sprintf("%s: %s is a %q, a type vCLU does not know; listed for the agent, not registered", label, per.Name, per.Type))
		}
		if isModbus(per.Class) {
			per.Features = map[int]string{}
			for _, f := range s.items(pn, "features") {
				if i, err := atoi(s.text(f, "index")); err == nil && s.text(f, "initValue") != "" {
					per.Features[i] = s.text(f, "initValue")
				}
			}
		}
		per.Events = p.readEvents(s, pn, "events", label+" "+per.Name)
		c.Peripherals = append(c.Peripherals, per)
	}
	for _, sn := range s.items(n, "scripts") {
		if s.text(sn, "isRemoved") == "true" {
			continue
		}
		sc := Script{Name: s.text(sn, "name")}
		if ids["script "+sc.Name] {
			continue
		}
		ids["script "+sc.Name] = true
		for _, par := range s.items(sn, "parameters") {
			sc.Params = append(sc.Params, s.text(par, "name"))
		}
		if sc.Name != "" {
			c.Scripts = append(c.Scripts, sc)
		}
	}
	return c
}

// readObject reads one input or output; false for one to leave out.
func (p *Project) readObject(s *stream, n *node, label string) (Object, bool) {
	o := Object{
		ID:   s.text(n, "nameOnCLU"),
		Name: s.text(n, "name"),
		Type: s.text(n, "type"),
	}
	if s.text(n, "isRemoved") == "true" || s.text(n, "active") == "false" {
		return o, false
	}
	what := label + ": " + n.name + " " + o.Name
	if !reObjID.MatchString(o.ID) {
		p.warn(what + " has no valid name on the CLU, left out")
		return o, false
	}
	p.checkName(what+" ("+o.ID+")", o.Name)
	class, err := atoi(s.text(n, "classTypeId"))
	if err != nil {
		p.warn(what + " (" + o.ID + ") has no object class, left out")
		return o, false
	}
	o.Class = class
	o.Index, err = atoi(s.text(n, "number"))
	if err != nil {
		p.warn(what + " (" + o.ID + ") has no index on its module, left out")
		return o, false
	}
	m := s.child(n, "module")
	o.Module, err = strconv.ParseUint(s.text(m, "moduleID"), 10, 64)
	if err != nil {
		p.warn(what + " (" + o.ID + ") has no module, left out")
		return o, false
	}
	if o.Class == classROLLER {
		for _, f := range s.items(n, "features") {
			if s.text(f, "index") == "10" {
				o.Lamel = s.text(f, "initValue")
			}
		}
	}
	o.Events = p.readEvents(s, n, "events", label+" "+o.Name)
	return o, true
}

// readEvents reads the events that run commands. OM 1.3 numbers an event
// with <vector>, later versions with <index>.
func (p *Project) readEvents(s *stream, n *node, list, label string) []Binding {
	var out []Binding
	for _, e := range s.items(n, list) {
		if p.overCmds {
			break
		}
		var cmds []string
		for _, c := range s.items(e, "commands") {
			if c.text == "" {
				continue
			}
			if p.commands += len(c.text); p.commands > maxCommands {
				if !p.overCmds {
					p.overCmds = true
					p.warn(fmt.Sprintf("%s: event commands over %d MB, the rest left out", label, maxCommands>>20))
				}
				break
			}
			cmds = append(cmds, c.text)
		}
		if len(cmds) == 0 {
			continue
		}
		b := Binding{Event: s.text(e, "name"), Commands: cmds}
		idx := s.text(e, "index")
		if idx == "" {
			idx = s.text(e, "vector")
		}
		i, err := atoi(idx)
		if err != nil {
			p.warn(fmt.Sprintf("%s: event %s has no index, left out", label, b.Event))
			continue
		}
		b.Index = i
		out = append(out, b)
	}
	return out
}

// readScripts reads scripts/<CLU name>/<script>.lua for the scripts system.xml
// lists.
func (p *Project) readScripts(files map[string]*zip.File) {
	total := 0
	for ci := range p.CLUs {
		c := &p.CLUs[ci]
		for si := range c.Scripts {
			sc := &c.Scripts[si]
			name := path.Join("scripts", c.Name, sc.Name+".lua")
			f := files[name]
			if f == nil {
				p.warn(fmt.Sprintf("%s: script %s has no file %s", c.Name, sc.Name, name))
				continue
			}
			if total > maxScripts {
				p.warn(fmt.Sprintf("%s: script %s left out, the scripts are over %d MB", c.Name, sc.Name, maxScripts>>20))
				continue
			}
			rc, err := f.Open()
			if err != nil {
				p.warn(fmt.Sprintf("%s: script %s: %v", c.Name, sc.Name, err))
				continue
			}
			b, err := io.ReadAll(io.LimitReader(rc, maxScript+1))
			_ = rc.Close()
			if err != nil || len(b) > maxScript {
				p.warn(fmt.Sprintf("%s: script %s unreadable or over %d MB, left out", c.Name, sc.Name, maxScript>>20))
				continue
			}
			total += len(b)
			sc.Source = string(b)
		}
		sort.SliceStable(c.Scripts, func(i, j int) bool { return c.Scripts[i].Name < c.Scripts[j].Name })
	}
}

// Names maps CLU IDs to their project names.
func (p *Project) Names() map[string]string {
	names := make(map[string]string, len(p.CLUs))
	for _, c := range p.CLUs {
		names[c.ID] = c.Name
	}
	return names
}
