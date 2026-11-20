package dashspec

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/luastr"
)

// DashboardID is the dashboard a page becomes.
func DashboardID(p Page) string { return "imported_" + p.ID }

// PageLua is the dashboard code of a page: plain Dashboard calls, the reads and
// commands through Wire's helpers. Widgets that fail Check are left out.
// Strings go through luastr.Quote, IDs and names are checked before they are written.
func PageLua(in Interface, p Page) string {
	var b strings.Builder
	fmt.Fprintf(&b, "-- Dashboard from the %s page %s of %s.\n", strconv.Quote(in.Source), strconv.Quote(p.Label), strconv.Quote(in.Label))
	b.WriteString("-- This file is yours: change labels, icons and order, remove or add widgets.\n")
	b.WriteString("-- vclu does not replace it once it is changed. Helpers: docs.vclu.pl, Grenton > myGrenton.\n")
	b.WriteString("local s = Dashboard.new(" + luastr.Quote(DashboardID(p)) + ")\n")
	b.WriteString("    :name(" + luastr.Quote(p.Label) + ")\n")
	// the page controls the house: only for those logged in, as the panel
	b.WriteString("    :auth(\"session\")\n")
	b.WriteString("    :tabGroup(" + luastr.Quote(in.Label) + ")\n")
	b.WriteString("    :section(" + luastr.Quote(p.Label) + ")\n")
	for _, w := range p.Widgets {
		if w.Check() != nil {
			continue
		}
		b.WriteString(widgetLua(w))
	}
	b.WriteString("s:build()\n")
	return b.String()
}

func widgetLua(w Widget) string {
	id, label := luastr.Quote(w.ID), luastr.Quote(w.Label)
	switch w.Kind {
	case Group:
		return "s:group(" + label + ")\n"
	case Toggle:
		return fmt.Sprintf("s:toggle(%s, %s, nil,\n    Wire.isOn(%s),\n    Wire.onOff(%s, %s))\n",
			id, label, readLua(w.Read), actionOrNil(w.On), actionOrNil(w.Off))
	case Status:
		read := readLua(w.Read)
		if w.Percent {
			read = "Wire.percent(" + read + ")"
		}
		if w.OnText != "" || w.OffText != "" {
			read = fmt.Sprintf("Wire.text(%s, %s, %s)", read, luastr.Quote(w.OnText), luastr.Quote(w.OffText))
		}
		args := []string{id, label, "nil", read}
		var opts []string
		if w.Decimals != nil {
			opts = append(opts, "decimals = "+strconv.Itoa(*w.Decimals))
		}
		if w.Unit != "" || len(opts) > 0 {
			args = append(args, luastr.Quote(w.Unit))
		}
		if len(opts) > 0 {
			args = append(args, "{ "+strings.Join(opts, ", ")+" }")
		}
		return "s:status(" + strings.Join(args, ", ") + ")\n"
	case Slider:
		opts := ""
		if w.Unit != "" {
			opts = ", { unit = " + luastr.Quote(w.Unit) + " }"
		}
		read, set := readLua(w.Read), actionLua(w.Set)
		if w.Percent {
			read, set = "Wire.percent("+read+")", "Wire.fromPercent("+set+")"
		}
		return fmt.Sprintf("s:slider(%s, %s, %s, %s,\n    %s,\n    %s%s)\n",
			id, label, num(*w.Min), num(*w.Max), read, set, opts)
	case Button:
		return fmt.Sprintf("s:button(%s, %s, nil, %s)\n", id, label, actionLua(w.Do))
	case Thermostat:
		fns := []string{"getCurrent = " + readLua(w.Current), "getTarget = " + readLua(w.Target)}
		if w.Set != nil {
			fns = append(fns, "setTarget = "+actionLua(w.Set))
		}
		if w.State != nil {
			fns = append(fns, "getState = Wire.isOn("+readLua(w.State)+")")
		}
		opts := ""
		if w.Min != nil && w.Max != nil {
			opts = fmt.Sprintf(", { min = %s, max = %s }", num(*w.Min), num(*w.Max))
		}
		return fmt.Sprintf("s:thermostat(%s, %s, nil, {\n    %s,\n}%s)\n", id, label, strings.Join(fns, ",\n    "), opts)
	}
	return ""
}

func num(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// path is the object a read or a command names: "CLU221000002.DOU0001", or the CLU.
func path(clu, object string) string {
	if object == "" {
		return luastr.Quote(clu)
	}
	return luastr.Quote(clu + "." + object)
}

func readLua(r *Read) string {
	switch {
	case r.Variable != "":
		return fmt.Sprintf("Wire.variable(%s, %s)", luastr.Quote(r.CLU), luastr.Quote(r.Variable))
	case *r.Feature == 0:
		return fmt.Sprintf("Wire.value(%s)", path(r.CLU, r.Object))
	}
	return fmt.Sprintf("Wire.feature(%s, %d)", path(r.CLU, r.Object), *r.Feature)
}

func valueLua(v any) string {
	switch v := v.(type) {
	case float64:
		return num(v)
	case string:
		return luastr.Quote(v)
	}
	return "nil"
}

func actionLua(a *Action) string {
	value := valueLua(a.Value)
	if a.Input {
		value = "Wire.INPUT"
	}
	switch a.Op {
	case SetFeature:
		return fmt.Sprintf("Wire.set(%s, %d, %s)", path(a.CLU, a.Object), a.Index, value)
	case Execute:
		if a.Value == nil && !a.Input {
			return fmt.Sprintf("Wire.execute(%s, %d)", path(a.CLU, a.Object), a.Index)
		}
		return fmt.Sprintf("Wire.execute(%s, %d, %s)", path(a.CLU, a.Object), a.Index, value)
	case Script:
		args := []string{luastr.Quote(a.CLU), luastr.Quote(a.Script)}
		for _, x := range a.Args {
			args = append(args, valueLua(x))
		}
		return "Wire.script(" + strings.Join(args, ", ") + ")"
	}
	return "nil"
}

func actionOrNil(a *Action) string {
	if a == nil {
		return "nil"
	}
	return actionLua(a)
}

// InitLua loads the given pages (module names under modules/), each on its own,
// so one that fails does not stop the others.
func InitLua(modules []string) string {
	var b strings.Builder
	b.WriteString("-- The imported dashboards; remove a line to drop a page.\n")
	for _, m := range modules {
		b.WriteString(LoadLine(m) + "\n")
	}
	return b.String()
}

// LoadLine is the line of InitLua that loads one page.
func LoadLine(module string) string { return "Wire.load(" + luastr.Quote(module) + ")" }

// describeValue is an action's value as the page list shows it; "…" is what the user enters.
func describeValue(a *Action) string {
	if a.Input {
		return "…"
	}
	if s, ok := a.Value.(string); ok {
		return strconv.Quote(s)
	}
	return valueLua(a.Value)
}

// Describe says what a widget shows, reads and does, for the page list.
func Describe(w Widget) (reads, does []string) {
	addR := func(r *Read) {
		if r == nil {
			return
		}
		if r.Variable != "" {
			reads = append(reads, r.CLU+":"+r.Variable)
			return
		}
		p := r.CLU
		if r.Object != "" {
			p += "." + r.Object
		}
		reads = append(reads, fmt.Sprintf("%s[%d]", p, *r.Feature))
	}
	addA := func(a *Action) {
		if a == nil {
			return
		}
		switch a.Op {
		case Script:
			does = append(does, a.CLU+":"+a.Script+"()")
		case SetFeature:
			does = append(does, fmt.Sprintf("%s.%s:set(%d, %s)", a.CLU, a.Object, a.Index, describeValue(a)))
		case Execute:
			if a.Value == nil && !a.Input {
				does = append(does, fmt.Sprintf("%s.%s:execute(%d)", a.CLU, a.Object, a.Index))
			} else {
				does = append(does, fmt.Sprintf("%s.%s:execute(%d, %s)", a.CLU, a.Object, a.Index, describeValue(a)))
			}
		}
	}
	addR(w.Read)
	addR(w.Current)
	addR(w.Target)
	addR(w.State)
	addA(w.On)
	addA(w.Off)
	addA(w.Set)
	addA(w.Do)
	return reads, does
}
