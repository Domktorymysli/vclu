package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A draft test must not run processes or write files, even when the call
// is built at runtime so that ValidateDraft cannot see it.
func TestSandbox_BlocksProcessesAndFileWrites(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	out := t.TempDir()
	readable := filepath.Join(out, "in.txt")
	if err := os.WriteFile(readable, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(out, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeDraft(t, ws, "modules/probe.lua", fmt.Sprintf(`
		local o = _G["o".."s"]
		o["exe".."cute"]("touch %[1]s/exec.txt")
		local p = io.popen("touch %[1]s/popen.txt")
		local w = io.open("%[1]s/write.txt", "w")
		local a = io.open("%[1]s/victim.txt", "a")
		o.remove("%[1]s/victim.txt")
		o.rename("%[1]s/victim.txt", "%[1]s/moved.txt")
		io.output("%[1]s/output.txt")
		local f = assert(io.open("%[1]s/in.txt", "r"))
		print("read:" .. f:read("*a"))
		f:close()
	`, out))

	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"exec.txt", "popen.txt", "write.txt", "moved.txt", "output.txt"} {
		if _, err := os.Stat(filepath.Join(out, f)); err == nil {
			t.Errorf("%s was created by the sandbox", f)
		}
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep" {
		t.Errorf("victim.txt changed: %q", data)
	}

	blocked := map[string]bool{}
	for _, c := range res.BlockedCalls {
		blocked[c.Type] = true
	}
	for _, want := range []string{"os.execute", "io.popen", "io.open", "os.remove", "os.rename", "io.output"} {
		if !blocked[want] {
			t.Errorf("%s not in blocked_calls: %+v", want, res.BlockedCalls)
		}
	}
	if !strings.Contains(strings.Join(res.Logs, "\n"), "read:hello") {
		t.Errorf("reading a file should still work, logs: %v, errors: %v", res.Logs, res.Errors)
	}
}
