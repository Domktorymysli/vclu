package mcp

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if raceEnabled {
		// the race detector runs Lua several times slower; tests of the
		// timeout itself set their own
		sandboxTimeout = 30 * time.Second
	}
	os.Exit(m.Run())
}
