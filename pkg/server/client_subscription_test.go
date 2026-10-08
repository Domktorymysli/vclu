package server

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestParseClientReportValues(t *testing.T) {
	tests := []struct {
		input    string
		expected []float64
	}{
		{"{1,0,0.5}", []float64{1, 0, 0.5}},
		{"{0,0,0,0}", []float64{0, 0, 0, 0}},
		{"{1}", []float64{1}},
		{"{100,255}", []float64{100, 255}},
		{"{ 1 , 2 , 3 }", []float64{1, 2, 3}},
		{"{}", nil},
		{"", nil},
	}

	for _, tt := range tests {
		tbl := new(lua.LTable)
		parseClientReportValues(tt.input, tbl)

		got := tbl.Len()
		want := len(tt.expected)
		if got != want {
			t.Errorf("parseClientReportValues(%q): got %d values, want %d", tt.input, got, want)
			continue
		}

		for i, exp := range tt.expected {
			val := tbl.RawGetInt(i + 1)
			num, ok := val.(lua.LNumber)
			if !ok {
				t.Errorf("parseClientReportValues(%q)[%d]: expected LNumber, got %T", tt.input, i+1, val)
				continue
			}
			if float64(num) != exp {
				t.Errorf("parseClientReportValues(%q)[%d]: got %v, want %v", tt.input, i+1, num, exp)
			}
		}
	}
}
