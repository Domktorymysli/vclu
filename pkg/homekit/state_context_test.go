package homekit

import "testing"

type asExecutor struct{ origin, actor string }

func (e *asExecutor) Execute(string) (string, error) { return "", nil }
func (e *asExecutor) ExecuteAs(origin, actor, _, _ string) (string, error) {
	e.origin, e.actor = origin, actor
	return "", nil
}

func TestHomeKitCommandsAreAttributed(t *testing.T) {
	ex := &asExecutor{}
	s := &Server{luaExecutor: ex}
	s.executeCommand(`h:setValue(1)`)
	if ex.origin != "homekit" || ex.actor != "service:homekit" {
		t.Fatalf("got %s %s", ex.origin, ex.actor)
	}
}
