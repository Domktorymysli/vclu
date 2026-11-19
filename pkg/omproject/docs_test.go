package omproject

import (
	"strings"
	"testing"
)

// The index loses a deleted CLU's row and says so under the title; other rows stay.
func TestForgetCLU(t *testing.T) {
	idx := "# Object Manager project\n\nText.\n\n| CLU | ID |\n|---|---|\n| Home | CLU221000001 | x |\n| Garage | CLU221000002 | y |\n"
	out := ForgetCLU(idx, "CLU221000001")
	if strings.Contains(out, "| Home |") || !strings.Contains(out, "| Garage | CLU221000002 |") ||
		!strings.HasPrefix(out, "# Object Manager project\n\n> CLU221000001 was deleted from vCLU") {
		t.Errorf("%s", out)
	}
	if ForgetCLU(idx, "CLU221000009") != idx {
		t.Error("an unknown CLU changed the index")
	}
	// CLU22100000 is not CLU221000001
	if ForgetCLU(idx, "CLU22100000") != idx {
		t.Error("a prefix matched")
	}
}
