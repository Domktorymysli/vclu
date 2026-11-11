package lua

import (
	"slices"
	"sync/atomic"
	"testing"
)

// The cloud's side of an exposed handle (:remote(), :roles(), :pin()) and #213's one path for
// every form of expose: exposing again, exposeRegistry() and hidden = true keep or drop the
// device in remoteList() as the cloud expects.
func TestExposeAgainKeepsRemote(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	var n atomic.Int32
	engine.SetCloud(CloudBridge{ObjectsChanged: func() { n.Add(1) }})
	mustExec(t, engine, exposeHiddenSetup)

	// A module's handle (a CLU object: its path is the registry's) with the cloud's options survives exposeRegistry() and exposing again.
	mustExec(t, engine, `
		_h = _shown:expose():remote():roles("dom"):pin("1234")
		exposeRegistry()
		_again = _shown:expose()
		_renamed = _shown:expose({ name = "Roleta" })`)
	if got := mustExec(t, engine, `tostring(_again == _h and _renamed == _h and _h._pin == "1234")`); got != "true" {
		t.Errorf("exposing again gave another handle or lost the PIN: %s", got)
	}
	_, list := remoteList(t, engine)
	i := slices.IndexFunc(list, func(d remoteDevice) bool { return d.Path == "CLU1.ROL1" })
	if i < 0 {
		t.Fatalf("CLU1.ROL1 not in remoteList: %+v", list)
	}
	if !slices.Equal(list[i].Roles, []string{"dom"}) || list[i].Name != "Roleta" {
		t.Errorf("remote device = %+v, want roles [dom] and the new name", list[i])
	}

	// exposeRegistry() first: the registry's handle takes :remote() from obj:expose().
	mustExec(t, engine, `_virtual:expose():remote()`)
	_, list = remoteList(t, engine)
	if !slices.ContainsFunc(list, func(d remoteDevice) bool { return d.Path == "virt.v1" }) {
		t.Errorf("registry-exposed object with :remote() not in remoteList: %+v", list)
	}

	// hidden = true later takes a remote device out and tells the cloud.
	mustExec(t, engine, `_hidden:expose():remote()`)
	before := n.Load()
	mustExec(t, engine, `_hidden:expose({ hidden = true })`)
	if n.Load() == before {
		t.Error("hiding a remote device did not tell the cloud")
	}
	// The dummy handle of a hidden object takes the cloud's methods and stays out.
	mustExec(t, engine, `expose(_hidden, "cover", { hidden = true }):remote():roles("a"):pin("1234")`)
	_, list = remoteList(t, engine)
	if slices.ContainsFunc(list, func(d remoteDevice) bool { return d.Path == "CLU1.ROL2" }) {
		t.Errorf("hidden object still in remoteList: %+v", list)
	}
}
