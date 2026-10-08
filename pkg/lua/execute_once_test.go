package lua

import (
	"strings"
	"testing"
	"time"
)

// A script that fails at runtime used to be retried in up to three wrappers,
// repeating every side effect before the error (e.g. a CLU command).
func TestExecute_RuntimeErrorRunsOnce(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`_sent = 0`); err != nil {
		t.Fatal(err)
	}
	scripts := []string{
		`_sent = _sent + 1; error("boom")`,                     // statements
		`(function() _sent = _sent + 1; error("boom") end)()`,  // expression
		`_sent = _sent + 1 local x = nil; return x.y`,          // chunk with return
		"_sent = _sent + 1 -- trailing comment\nerror('boom')", // comment at the end
	}
	// Execute serves CLU commands and the web console, ExecuteWithTimeout vclu_exec
	run := map[string]func(string) error{
		"Execute": func(s string) error { _, err := engine.Execute(s); return err },
		"ExecuteWithTimeout": func(s string) error {
			_, err := engine.ExecuteWithTimeout(s, time.Second)
			return err
		},
	}
	for name, exec := range run {
		for i, s := range scripts {
			engine.Execute(`_sent = 0`)
			if err := exec(s); err == nil {
				t.Fatalf("%s script %d: expected an error", name, i)
			}
			if got, _ := engine.Execute(`_sent`); got != "1" {
				t.Errorf("%s script %d ran %s times, want 1", name, i, got)
			}
		}
	}
}

func TestExecuteWithTimeout_StillTimesOut(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	_, err := engine.ExecuteWithTimeout(`while true do end`, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
	if got, err := engine.ExecuteWithTimeout("return 7 -- comment", time.Second); err != nil || got != "7" {
		t.Fatalf("after timeout: %q, %v", got, err)
	}
}

func TestExecute_Forms(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	cases := map[string]string{
		`1 + 2`:                     "3",
		`return 1 + 2`:              "3",
		`local a = 2; return a * 3`: "6",
		`x_forms = 5`:               "nil",
		`print("alive")`:            "alive",
		"return 7 -- comment":       "7",
	}
	for script, want := range cases {
		got, err := engine.Execute(script)
		if err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", script, got, err, want)
		}
	}
	if _, err := engine.Execute(`this is not lua`); err == nil || !strings.Contains(err.Error(), "failed to execute") {
		t.Errorf("syntax error not reported: %v", err)
	}
}
