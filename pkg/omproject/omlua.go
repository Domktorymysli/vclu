package omproject

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// OMLua writes the declarations of om.lua for a CLU of the project: the lines
// pkg/omparser reads, as Object Manager writes them. It has no event bodies or
// feature settings and is never run; vCLU parses it like the om.lua of a
// diagnostic pack.
func (p *Project) OMLua(c CLU) string {
	var b strings.Builder
	fmt.Fprintf(&b, "-- Declarations from the Object Manager project (OM %s), written by vCLU\n", orUnknown(p.Version))
	fmt.Fprintf(&b, "-- FwType %s\n-- FwVersion %s\n-- HwType %s\n-- HwVersion %s\n\n", c.FwType, c.FwVersion, c.HwType, c.HwVersion)

	// A GATE's om.lua carries the Wi-Fi network and password; vCLU needs neither.
	if c.Gate {
		fmt.Fprintf(&b, "%s = GATE:new(5000, 0x%s, 0x0, 0x0, \"\", \"\")\n", c.ID, hexIP(c.IP))
	} else {
		fmt.Fprintf(&b, "%s = OBJECT:new(0, 0x%s, %q)\n", c.ID, hexIP(c.IP), c.ID)
	}
	fmt.Fprintf(&b, "-- NAME_CLU %s=%s\n\n", luaName(c.Name, c.ID), c.ID)
	for _, o := range p.CLUs {
		if o.ID != c.ID {
			fmt.Fprintf(&b, "%s = OBJECT:new(1, 0x%s, %q)\n", o.ID, hexIP(o.IP), o.ID)
		}
	}
	b.WriteString("\n")
	// A GATE's peripherals are GATE objects in om.lua (SAT0001 = GATE:new(100, ...)).
	kind := "OBJECT"
	if c.Gate {
		kind = "GATE"
	}
	for _, per := range c.Peripherals {
		if per.Class == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s = %s:new(%d, %q)\n-- NAME_PERIPHERY %s=%s\n", per.ID, kind, per.Class, per.ID, luaName(per.Name, per.ID), per.ID)
	}
	b.WriteString("\n-- MODULES\n")
	for _, m := range c.Modules {
		fmt.Fprintf(&b, "mm_%d = OBJECT:new(2, 0x%08x, 0x%s, %q)\n", m.Serial, m.Serial, m.Type, m.InnerName)
	}
	b.WriteString("\n-- IO_MODULES\n")
	for _, o := range c.Objects {
		fmt.Fprintf(&b, " %s = OBJECT:new(%d, mm_%d, %d, %q)\n-- NAME_IO %s=%s\n", o.ID, o.Class, o.Module, o.Index, o.ID, luaName(o.Name, o.ID), o.ID)
	}
	b.WriteString("\n")
	for _, o := range c.Objects {
		if _, err := strconv.ParseFloat(o.Lamel, 64); err == nil {
			fmt.Fprintf(&b, "%s:set(10, %s)\n", o.ID, o.Lamel)
		}
	}
	add := func(id string, events []Binding) {
		for _, e := range events {
			fmt.Fprintf(&b, "%s:add_event(%d, EventsFor_%s_%d)\n", id, e.Index, id, e.Index)
		}
	}
	// OM binds every CLU's OnInit, with or without commands.
	fmt.Fprintf(&b, "%s:add_event(0, OnInit)\n", c.ID)
	for _, o := range c.Objects {
		add(o.ID, o.Events)
	}
	for _, per := range c.Peripherals {
		add(per.ID, per.Events)
	}
	return b.String()
}

// hexIP is an IPv4 address as OM writes it in om.lua: C0A80003.
func hexIP(ip string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || !a.Is4() {
		return "0"
	}
	v := a.As4()
	return fmt.Sprintf("%02X%02X%02X%02X", v[0], v[1], v[2], v[3])
}

func orUnknown(s string) string {
	if s == "" {
		return "version unknown"
	}
	return s
}
