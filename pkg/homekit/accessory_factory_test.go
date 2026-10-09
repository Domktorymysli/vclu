package homekit

import (
	"testing"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
)

type fakeLua struct{ json string }

func (f fakeLua) Execute(script string) (string, error) { return f.json, nil }

func findChar(a *accessory.A, typ string) *characteristic.C {
	for _, sv := range a.Ss {
		for _, c := range sv.Cs {
			if c.Type == typ {
				return c
			}
		}
	}
	return nil
}

func hasPerm(c *characteristic.C, p string) bool {
	for _, x := range c.Permissions {
		if x == p {
			return true
		}
	}
	return false
}

// The accessory name is prefixed with the room so accessories are distinguishable
// during Apple Home pairing (HomeKit has no per-accessory room hint).
func TestDisplayNameIncludesRoom(t *testing.T) {
	cases := []struct{ area, name, path, want string }{
		{"Salon", "Światło", "p", "Salon Światło"},
		{"Kuchnia", "Temperatura", "p", "Kuchnia Temperatura"},
		{"", "Światło", "p", "Światło"},
		{"", "", "clu.dou1", "clu.dou1"},
	}
	for _, c := range cases {
		o := ExposedObject{Area: c.area, Name: c.name, Path: c.path}
		if got := o.displayName(); got != c.want {
			t.Errorf("displayName(area=%q, name=%q) = %q, want %q", c.area, c.name, got, c.want)
		}
	}
}

// #3: a readonly accessory's control characteristic must drop PairedWrite so the
// Home app shows it as a read-only status tile instead of a (useless) toggle.
func TestReadonlyAccessoryDropsWritePerm(t *testing.T) {
	s := &Server{}
	obj := ExposedObject{Path: "t:sw", Name: "Pompa", Type: "switch", Value: float64(0)}

	ro := s.createExposedAccessory(obj, true)
	on := findChar(ro.Accessory, characteristic.TypeOn)
	if on == nil {
		t.Fatal("no On characteristic on switch accessory")
	}
	if hasPerm(on, characteristic.PermissionWrite) {
		t.Error("readonly switch On must NOT have PairedWrite")
	}
	if !hasPerm(on, characteristic.PermissionRead) {
		t.Error("readonly switch On must still be readable")
	}

	rw := s.createExposedAccessory(obj, false)
	on2 := findChar(rw.Accessory, characteristic.TypeOn)
	if !hasPerm(on2, characteristic.PermissionWrite) {
		t.Error("writable switch On must keep PairedWrite")
	}
}

// #2: a Hidden object must NOT become a HomeKit accessory, even when it was exposed
// with readonly=true (hidden is the stricter level, must win).
func TestHiddenWinsOverReadonly(t *testing.T) {
	mgr := accesscontrol.NewManager(t.TempDir())
	mgr.SetAccess(accesscontrol.IntegrationHomeKit, "t:hidden", accesscontrol.AccessHidden)

	objectsJSON := `[
		{"path":"t:hidden","type":"switch","name":"Piec","value":0,"readonly":true},
		{"path":"t:visible","type":"switch","name":"Lampa","value":0,"readonly":false}
	]`

	s := &Server{
		luaExecutor:   fakeLua{json: objectsJSON},
		accessManager: mgr,
		accessories:   make(map[string]*AccessoryWrapper),
	}
	objs, err := s.fetchExposed()
	if err != nil {
		t.Fatalf("fetchExposed: %v", err)
	}
	s.accessories = s.buildAccessories(objs)

	if _, ok := s.accessories["t:hidden"]; ok {
		t.Error("readonly+hidden object must NOT become an accessory")
	}
	if _, ok := s.accessories["t:visible"]; !ok {
		t.Error("non-hidden object must become an accessory")
	}
}
