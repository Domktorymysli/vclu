// Package dashspec is vclu's own model of a dashboard imported from elsewhere
// (the myGrenton interface, #219): pages of widgets, what each reads and what it
// does. It is the layer between a foreign format and vclu: a translator (e.g.
// omproject for myGrenton) fills it, Lua is generated from it alone, so a change
// in the foreign format stops at its translator.
package dashspec

import (
	"fmt"
	"regexp"
)

// Interface is one imported interface: its pages become dashboards in one tab group.
type Interface struct {
	ID     string `json:"id"`     // letters, digits and -
	Label  string `json:"label"`  // text
	Source string `json:"source"` // what it was imported from, e.g. "myGrenton"
	Pages  []Page `json:"pages"`
}

// Page is one dashboard.
type Page struct {
	ID      string   `json:"id"` // digits
	Label   string   `json:"label"`
	Widgets []Widget `json:"widgets"`
	// LeftOut says what of the source page has no widget here, and why.
	LeftOut []string `json:"leftOut,omitempty"`
}

// Widget kinds.
const (
	Group      = "group"
	Toggle     = "toggle"
	Status     = "status"
	Slider     = "slider"
	Button     = "button"
	Thermostat = "thermostat"
)

// Widget is one dashboard widget. Which fields it uses depends on its Kind:
//
//	group       Label
//	toggle      Label, Read (on when not 0), On, Off
//	status      Label, Read, Unit, Decimals, OnText/OffText (Read shown as one of them)
//	slider      Label, Read, Set (with Input), Min, Max, Unit
//	button      Label, Do
//	thermostat  Label, Current, Target, Set (with Input), State, Min, Max
type Widget struct {
	Kind     string   `json:"kind"`
	ID       string   `json:"id,omitempty"` // unique on its page; group has none
	Label    string   `json:"label"`
	Read     *Read    `json:"read,omitempty"`
	On       *Action  `json:"on,omitempty"`
	Off      *Action  `json:"off,omitempty"`
	Set      *Action  `json:"set,omitempty"`
	Do       *Action  `json:"do,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Decimals *int     `json:"decimals,omitempty"`
	OnText   string   `json:"onText,omitempty"`
	OffText  string   `json:"offText,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Current  *Read    `json:"current,omitempty"`
	Target   *Read    `json:"target,omitempty"`
	State    *Read    `json:"state,omitempty"`
}

// Read is a value of a CLU: a feature of an object (Object "" is the CLU
// itself) or a user feature of the CLU.
type Read struct {
	CLU      string `json:"clu"`
	Object   string `json:"object,omitempty"`
	Feature  *int   `json:"feature,omitempty"`
	Variable string `json:"variable,omitempty"`
}

// Action kinds.
const (
	SetFeature = "set"     // Object:set(Feature, Value)
	Execute    = "execute" // Object:execute(Method, Value)
	Script     = "script"  // Script(Args...) on the CLU
)

// Action is a command to a CLU.
type Action struct {
	Op     string `json:"op"`
	CLU    string `json:"clu"`
	Object string `json:"object,omitempty"`
	Index  int    `json:"index,omitempty"` // feature for set, method for execute
	// Value is a number or a string; nil sends none (execute only).
	Value any `json:"value,omitempty"`
	// Input takes the value the user enters (slider, thermostat) instead of Value.
	Input  bool   `json:"input,omitempty"`
	Script string `json:"script,omitempty"`
	Args   []any  `json:"args,omitempty"` // numbers and strings
}

var (
	reInterfaceID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	rePageID      = regexp.MustCompile(`^[0-9]{1,19}$`)
	reWidgetID    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	reCLU         = regexp.MustCompile(`^CLU[0-9]{1,12}$`)
	reObject      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	reName        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

var keywords = map[string]bool{
	"and": true, "break": true, "do": true, "else": true, "elseif": true, "end": true,
	"false": true, "for": true, "function": true, "if": true, "in": true, "local": true,
	"nil": true, "not": true, "or": true, "repeat": true, "return": true, "then": true,
	"true": true, "until": true, "while": true,
}

// IsName says whether s is a Lua name: a script or a user feature.
func IsName(s string) bool { return reName.MatchString(s) && !keywords[s] }

// IsCLU and IsObject check IDs as om.lua writes them.
func IsCLU(s string) bool    { return reCLU.MatchString(s) }
func IsObject(s string) bool { return reObject.MatchString(s) && !keywords[s] }

// ValidID says whether an interface or page id may name files and dashboards.
func ValidID(s string) bool     { return reInterfaceID.MatchString(s) }
func ValidPageID(s string) bool { return rePageID.MatchString(s) }

// Check says what is wrong with a read, nil when it may be generated.
func (r *Read) Check() error {
	switch {
	case r == nil:
		return fmt.Errorf("no read")
	case !IsCLU(r.CLU):
		return fmt.Errorf("CLU %q is no CLU ID", r.CLU)
	case r.Variable != "":
		if r.Object != "" || r.Feature != nil {
			return fmt.Errorf("a user feature with an object")
		}
		if !IsName(r.Variable) {
			return fmt.Errorf("user feature %q is no Lua name", r.Variable)
		}
	case r.Feature == nil || *r.Feature < 0 || *r.Feature > 9999:
		return fmt.Errorf("no feature number")
	case r.Object != "" && !IsObject(r.Object):
		return fmt.Errorf("object %q is no object ID", r.Object)
	}
	return nil
}

// Check says what is wrong with an action, nil when it may be generated.
func (a *Action) Check() error {
	if a == nil {
		return fmt.Errorf("no action")
	}
	if !IsCLU(a.CLU) {
		return fmt.Errorf("CLU %q is no CLU ID", a.CLU)
	}
	value := func(v any) error {
		switch v.(type) {
		case nil, float64, string:
			return nil
		}
		return fmt.Errorf("value %v is no number or string", v)
	}
	switch a.Op {
	case SetFeature, Execute:
		// commands go to objects, through their proxies; the CLU itself is only read
		if !IsObject(a.Object) || IsCLU(a.Object) {
			return fmt.Errorf("object %q is no object ID", a.Object)
		}
		if a.Index < 0 || a.Index > 9999 {
			return fmt.Errorf("index %d out of range", a.Index)
		}
		if a.Op == SetFeature && a.Value == nil && !a.Input {
			return fmt.Errorf("set without a value")
		}
		return value(a.Value)
	case Script:
		if !IsName(a.Script) {
			return fmt.Errorf("script %q is no Lua name", a.Script)
		}
		if len(a.Args) > 16 {
			return fmt.Errorf("more than 16 arguments")
		}
		for _, x := range a.Args {
			if x == nil {
				return fmt.Errorf("a nil argument")
			}
			if err := value(x); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("action %q unknown", a.Op)
}

// Check says what is wrong with a widget, nil when it may be generated.
func (w *Widget) Check() error {
	if w.Kind != Group && !reWidgetID.MatchString(w.ID) {
		return fmt.Errorf("widget id %q", w.ID)
	}
	need := func(r *Read, what string) error {
		if err := r.Check(); err != nil {
			return fmt.Errorf("%s: %v", what, err)
		}
		return nil
	}
	may := func(a *Action, what string) error {
		if a == nil {
			return nil
		}
		if err := a.Check(); err != nil {
			return fmt.Errorf("%s: %v", what, err)
		}
		return nil
	}
	input := func(a *Action, what string) error {
		if a != nil && !a.Input {
			return fmt.Errorf("%s takes what the user enters", what)
		}
		return may(a, what)
	}
	rng := func() error {
		if w.Min == nil || w.Max == nil || *w.Min >= *w.Max {
			return fmt.Errorf("no range")
		}
		return nil
	}
	switch w.Kind {
	case Group:
		return nil
	case Toggle:
		if err := need(w.Read, "read"); err != nil {
			return err
		}
		if err := may(w.On, "on"); err != nil {
			return err
		}
		return may(w.Off, "off")
	case Status:
		return need(w.Read, "read")
	case Slider:
		if err := need(w.Read, "read"); err != nil {
			return err
		}
		if w.Set == nil {
			return fmt.Errorf("slider without set")
		}
		if err := input(w.Set, "set"); err != nil {
			return err
		}
		return rng()
	case Button:
		if w.Do == nil {
			return fmt.Errorf("button without action")
		}
		if w.Do.Input {
			return fmt.Errorf("a button has no value to enter")
		}
		return may(w.Do, "do")
	case Thermostat:
		if err := need(w.Current, "current"); err != nil {
			return err
		}
		if err := need(w.Target, "target"); err != nil {
			return err
		}
		if w.State != nil {
			if err := need(w.State, "state"); err != nil {
				return err
			}
		}
		if w.Min != nil || w.Max != nil {
			if err := rng(); err != nil {
				return err
			}
		}
		return input(w.Set, "set")
	}
	return fmt.Errorf("kind %q unknown", w.Kind)
}
