package web

import "testing"

func TestSetVersion(t *testing.T) {
	for in, want := range map[string]string{
		"":       "dev",
		"  ":     "dev",
		"dev":    "dev",
		"1.3.0":  "v1.3.0",
		"v1.3.0": "v1.3.0",
	} {
		s := &Server{}
		s.SetVersion(in)
		if s.version != want {
			t.Errorf("SetVersion(%q) shows %q, want %q", in, s.version, want)
		}
	}
}
