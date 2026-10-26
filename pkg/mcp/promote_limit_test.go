package mcp

import (
	"testing"
	"time"
)

// The promote limit stops a looping agent (one reload a minute on average), not normal work.
func TestPromoteLimitPerHour(t *testing.T) {
	if promotesPerHour < 60 {
		t.Fatalf("promotesPerHour = %d, want at least 60", promotesPerHour)
	}
	rl := newRateLimiter(time.Hour, promotesPerHour)
	for i := 0; i < promotesPerHour; i++ {
		if !rl.allow() {
			t.Fatalf("promote %d refused", i+1)
		}
	}
	if rl.allow() {
		t.Errorf("promote %d allowed, want refused", promotesPerHour+1)
	}
}
