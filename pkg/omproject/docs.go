package omproject

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// DocsDir is where the agent's files about the project go, under the data
// directory. vCLU owns it: an import replaces everything in it.
const DocsDir = "ai/om"

// maxDoc keeps every file under what MCP's vclu_read_file returns (100 KB):
// scripts are split in parts, anything else longer is cut.
const maxDoc = 90 << 10

// Docs writes what the agent reads about the project: index.md, one file per
// CLU with its objects, peripherals and event bindings, and the CLU's
// scripts in <CLU>-scripts.md (split in parts when they are long). Keys are
// file names in DocsDir.
func (p *Project) Docs() map[string]string {
	files := map[string]string{}
	var idx strings.Builder
	idx.WriteString("# Object Manager project\n\n")
	fmt.Fprintf(&idx, "Written by vCLU from the Object Manager project (OM %s) when it was imported; the next import replaces the files in `%s/`.\n\n", orUnknown(p.Version), DocsDir)
	idx.WriteString("| CLU | ID | IP | Objects | Peripherals | Bindings | Scripts | File |\n|---|---|---|---|---|---|---|---|\n")
	for _, c := range p.CLUs {
		bindings := len(c.Events)
		for _, o := range c.Objects {
			bindings += len(o.Events)
		}
		for _, per := range c.Peripherals {
			bindings += len(per.Events)
		}
		fmt.Fprintf(&idx, "| %s | %s | %s | %d | %d | %d | %d | [%s.md](%s.md) |\n",
			cell(c.Name), c.ID, cell(c.IP), len(c.Objects), len(c.Peripherals), bindings, len(c.Scripts), c.ID, c.ID)
		files[c.ID+".md"] = p.cluDoc(c)
		for i, part := range scriptDocs(c) {
			name := c.ID + "-scripts.md"
			if i > 0 {
				name = fmt.Sprintf("%s-scripts-%d.md", c.ID, i+1)
			}
			files[name] = part
		}
	}
	idx.WriteString(p.howTo())
	idx.WriteString(p.driven())
	if len(p.Warnings) > 0 {
		idx.WriteString("\n## Left out on import\n\n")
		for _, w := range CapWarnings(p.Warnings, p.WarningsOmitted) {
			fmt.Fprintf(&idx, "- %s\n", oneLine(w))
		}
	}
	files["index.md"] = idx.String()
	for name, d := range files {
		files[name] = cut(d)
	}
	return files
}

// ForgetCLU is the index without the row of a CLU deleted from vCLU after the import, with
// a note under the title saying so: the rest of the index may still name it.
func ForgetCLU(index, id string) string {
	lines := strings.Split(index, "\n")
	out := make([]string, 0, len(lines)+2)
	removed := false
	for _, l := range lines {
		if strings.HasPrefix(l, "| ") && strings.Contains(l, " | "+id+" | ") {
			removed = true
			continue
		}
		out = append(out, l)
	}
	if !removed {
		return index
	}
	note := fmt.Sprintf("> %s was deleted from vCLU after this import: its objects are gone, whatever below still names it.", id)
	if len(out) > 0 && strings.HasPrefix(out[0], "# ") {
		out = append([]string{out[0], "", note}, out[1:]...)
	} else {
		out = append([]string{note, ""}, out...)
	}
	return strings.Join(out, "\n")
}

// cut shortens a file over maxDoc at the end of a line, saying so.
func cut(d string) string {
	const note = "\n(cut by vCLU: the rest is in the Object Manager project)\n"
	if len(d) <= maxDoc {
		return d
	}
	d = strings.ToValidUTF8(d[:maxDoc-len(note)], "")
	if i := strings.LastIndexByte(d, '\n'); i > 0 {
		d = d[:i+1]
	}
	return d + note
}

func (p *Project) cluDoc(c CLU) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s)\n\nFrom the Object Manager project (OM %s). IP %s.\n", oneLine(c.Name), c.ID, orUnknown(p.Version), oneLine(c.IP))
	if len(c.Scripts) > 0 {
		fmt.Fprintf(&b, "Scripts: `%s-scripts.md`.\n", c.ID)
	}

	b.WriteString("\n## Event bindings\n\n")
	n := 0
	bind := func(who string, events []Binding) {
		for _, e := range events {
			fmt.Fprintf(&b, "- %s %s: %s\n", who, oneLine(e.Event), code(strings.Join(e.Commands, "; ")))
			n++
		}
	}
	bind("**"+oneLine(c.Name)+"** (the CLU)", c.Events)
	for _, o := range c.Objects {
		bind(fmt.Sprintf("**%s** (%s)", oneLine(o.Name), o.ID), o.Events)
	}
	for _, per := range c.Peripherals {
		bind(fmt.Sprintf("**%s** (%s)", oneLine(per.Name), per.ID), per.Events)
	}
	if n == 0 {
		b.WriteString("None.\n")
	}

	if len(c.Objects) > 0 {
		b.WriteString("\n## Objects\n\n| Name | ID | Type | Module | Index |\n|---|---|---|---|---|\n")
		for _, o := range c.Objects {
			fmt.Fprintf(&b, "| %s | %s | %s | %d | %d |\n", cell(o.Name), o.ID, cell(o.Type), o.Module, o.Index)
		}
	}
	if len(c.Peripherals) > 0 {
		b.WriteString("\n## Peripherals\n\n| Name | ID | Type |\n|---|---|---|\n")
		for _, per := range c.Peripherals {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", cell(per.Name), per.ID, cell(per.Type))
		}
	}
	return b.String()
}

// scriptDocs are the CLU's scripts in files under maxDoc each.
func scriptDocs(c CLU) []string {
	if len(c.Scripts) == 0 {
		return nil
	}
	head := func(part int) string {
		h := fmt.Sprintf("# Scripts of %s (%s)\n\nFrom the Object Manager project; they run on the CLU, not in vCLU.\n", oneLine(c.Name), c.ID)
		if part > 1 {
			h = fmt.Sprintf("# Scripts of %s (%s), part %d\n", oneLine(c.Name), c.ID, part)
		}
		return h
	}
	var parts []string
	var b strings.Builder
	b.WriteString(head(1))
	for _, s := range c.Scripts {
		var one strings.Builder
		fmt.Fprintf(&one, "\n## %s(%s)\n\n", oneLine(s.Name), oneLine(strings.Join(s.Params, ", ")))
		if s.Source == "" {
			one.WriteString("Not in the project.\n")
		} else {
			src := s.Source
			if len(src) > maxDoc-1024 {
				src = strings.ToValidUTF8(src[:maxDoc-1024], "") + "\n-- (cut by vCLU: the script is longer)\n"
			}
			f := fence(src)
			fmt.Fprintf(&one, "%slua\n%s\n%s\n", f, strings.TrimRight(src, "\n"), f)
		}
		if b.Len()+one.Len() > maxDoc {
			parts = append(parts, b.String())
			b.Reset()
			b.WriteString(head(len(parts) + 1))
		}
		b.WriteString(one.String())
	}
	return append(parts, b.String())
}

// fence is a code fence longer than any run of backticks in s.
func fence(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// code is s as an inline code span.
func code(s string) string {
	s = oneLine(s)
	if strings.Contains(s, "`") {
		return "`` " + s + " ``"
	}
	return "`" + s + "`"
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func cell(s string) string {
	return strings.ReplaceAll(oneLine(s), "|", `\|`)
}

// verbs are the OM methods a vCLU proxy has, by om.lua class: OM's name and
// the proxy's, which does the same (pkg/lua/runtime/objects/Remote*.lua). Not
// here: a DIMM's switchOn goes back to its last brightness, and a roller with
// a position (24) moves by SetPosition.
var verbs = map[int]map[string]string{
	3:  {"Value": "getValue"},
	4:  {"Value": "getValue", "SetValue": "setValue", "SwitchOn": "switchOn", "SwitchOff": "switchOff", "Switch": "switch"},
	5:  {"Value": "getValue", "SetValue": "setValue", "SwitchOff": "switchOff", "Switch": "toggle"},
	11: {"MoveUp": "up", "MoveDown": "down", "Stop": "stop"},
}

// reCall is a command in OM's notation that calls an object's method.
var reCall = regexp.MustCompile(`^\s*(\w+)->(\w+)->(\w+)\((.*)\)\s*;?\s*$`)

// reads are OM methods that only read.
var reads = map[string]bool{"Value": true, "get": true}

// target is the object a command calls, with its CLU.
type target struct {
	clu *CLU
	obj *Object
}

// targets finds objects by "<CLU name>-><object name>", as commands name them;
// the first of two with the same names.
type targets map[string]target

func (p *Project) targets() targets {
	t := targets{}
	for ci := range p.CLUs {
		c := &p.CLUs[ci]
		for oi := range c.Objects {
			key := c.Name + "->" + c.Objects[oi].Name
			if _, ok := t[key]; !ok {
				t[key] = target{c, &c.Objects[oi]}
			}
		}
	}
	return t
}

// call resolves a command in OM's notation: its object, method and arguments.
func (ts targets) call(cmd string) (t target, method, args string, ok bool) {
	m := reCall.FindStringSubmatch(cmd)
	if m == nil {
		return t, "", "", false
	}
	t, ok = ts[m[1]+"->"+m[2]]
	return t, m[3], m[4], ok
}

// howTo tells the agent how OM's notation reads in vCLU's Lua, with examples
// from the project's own bindings when it has some vCLU can translate.
func (p *Project) howTo() string {
	var b strings.Builder
	b.WriteString("\n## From Object Manager to vCLU's Lua\n\n")
	b.WriteString("The bindings and scripts here are in Object Manager's notation: `CLU->object->Method(args)` calls an object's method, `CLU->script(args)` a script. In vCLU:\n\n")
	b.WriteString("- An object is in the registry as `<CLU ID>.<object ID>`, the IDs from the tables in the CLU's file: `_:get(\"CLU221000001.DOU0001\")`. Its name works too (`_:get(\"hall_light\")`) only while no other object has it; use the path.\n")
	b.WriteString("- Methods start with a small letter. The same as in OM: on a DOUT `SwitchOn`, `SwitchOff`, `Switch` (`switch`), `SetValue`; on a DIMM `SwitchOff`, `Switch` (`toggle`), `SetValue`; on a roller without a position `MoveUp`/`MoveDown`/`Stop` (`up`/`down`/`stop`). Not the same: a DIMM's `switchOn()` goes back to its last brightness, OM's `SwitchOn` to the module's start level (`setValue` sets a brightness); a roller with a position goes up and down by its position. `->Value()` is `:getValue()`, or `_:value(\"<path>\")`. For anything else, read the object's methods before using them.\n")
	b.WriteString("- Events: `obj:onEvent(\"OnSwitchOn\", function() ... end)` or `obj:onChange(function(new, old) ... end)`. OnChange comes on every object when its value changes (not for the first value after a start); OnSwitchOn and OnSwitchOff only on a DOUT and a DIN. Other events (OnClick, OnHold, ...) come only when the CLU sends them to vCLU.\n")
	b.WriteString("- Scripts run on the CLU; vCLU does not run them. What a script does is written again in Lua, or left on the CLU.\n")
	b.WriteString("- `vclu_registry` lists what is registered; `vclu_device_get` gives the methods of an exposed one.\n")

	seen := map[string]bool{}
	var ex []string
	ts := p.targets()
	for _, c := range p.CLUs {
		for _, o := range c.Objects {
			for _, e := range o.Events {
				for _, cmd := range e.Commands {
					if len(ex) == 6 {
						break
					}
					t, method, args, ok := ts.call(cmd)
					if !ok || strings.Contains(args, "->") {
						// an argument in OM's notation is no Lua
						continue
					}
					verb := verbs[t.obj.Class][method]
					if verb == "" || seen[t.obj.Type+method] {
						continue
					}
					seen[t.obj.Type+method] = true
					ex = append(ex, fmt.Sprintf("| %s | %s |\n", code(cmd), code(fmt.Sprintf("_:get(%q):%s(%s)", t.clu.ID+"."+t.obj.ID, verb, args))))
				}
			}
		}
	}
	if len(ex) > 0 {
		b.WriteString("\nFrom this project:\n\n| Object Manager | vCLU |\n|---|---|\n")
		b.WriteString(strings.Join(ex, ""))
	}
	return b.String()
}

// maxDriven keeps the list of driven objects from filling index.md.
const maxDriven = 300

// driven lists the objects Object Manager's logic already commands, and from
// where: an automation in vCLU on one of them runs next to that logic.
func (p *Project) driven() string {
	by := map[string][]string{}
	name := map[string]string{}
	seen := map[string]bool{}
	ts := p.targets()
	add := func(cmd, from string) {
		t, method, _, ok := ts.call(cmd)
		if !ok || reads[method] {
			return
		}
		path := t.clu.ID + "." + t.obj.ID
		if seen[path+" "+from] {
			return
		}
		seen[path+" "+from] = true
		name[path] = t.obj.Name
		by[path] = append(by[path], from)
	}
	for _, c := range p.CLUs {
		bind := func(who string, events []Binding) {
			for _, e := range events {
				for _, cmd := range e.Commands {
					add(cmd, fmt.Sprintf("%s %s", who, oneLine(e.Event)))
				}
			}
		}
		bind(c.Name, c.Events)
		for _, o := range c.Objects {
			bind(o.Name, o.Events)
		}
		for _, per := range c.Peripherals {
			bind(per.Name, per.Events)
		}
		for _, s := range c.Scripts {
			for _, line := range strings.Split(s.Source, "\n") {
				// a script's own Lua is not OM's notation; only its calls are
				for _, m := range reInScript.FindAllString(line, -1) {
					add(m, "script "+s.Name)
				}
			}
		}
	}
	var b strings.Builder
	b.WriteString("\n## What Object Manager already does\n\n")
	if len(by) == 0 {
		b.WriteString("No binding or script of the project commands an object.\n")
		return b.String()
	}
	b.WriteString("These objects are commanded by the project's own event bindings and scripts. Before an automation in vCLU commands one of them, read what the CLU does with it: both run, and the last command wins.\n\n")
	b.WriteString("| Object | Name | Commanded by |\n|---|---|---|\n")
	paths := make([]string, 0, len(by))
	for path := range by {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for i, path := range paths {
		if i == maxDriven {
			fmt.Fprintf(&b, "\n%d more; the bindings are in the CLUs' files.\n", len(paths)-maxDriven)
			break
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", path, cell(name[path]), cell(strings.Join(by[path], ", ")))
	}
	return b.String()
}

// reInScript finds OM calls of objects inside a script's source.
var reInScript = regexp.MustCompile(`\w+->\w+->\w+\([^()]*\)`)
