package config

import "testing"

func TestVCLUSyncConfigClamp(t *testing.T) {
	cases := []struct {
		in, want VCLUSyncConfig
	}{
		{VCLUSyncConfig{TTL: 0, BatchSize: 0, BatchDelay: 0}, VCLUSyncConfig{TTL: 10, BatchSize: 2, BatchDelay: 30}},
		{VCLUSyncConfig{TTL: 60, BatchSize: 4, BatchDelay: 50}, VCLUSyncConfig{TTL: 60, BatchSize: 4, BatchDelay: 50}},
		{VCLUSyncConfig{TTL: 60, BatchSize: 28, BatchDelay: 100}, VCLUSyncConfig{TTL: 60, BatchSize: 28, BatchDelay: 100}},
		{VCLUSyncConfig{TTL: 60, BatchSize: 50, BatchDelay: 500}, VCLUSyncConfig{TTL: 60, BatchSize: 28, BatchDelay: 100}},
	}
	for _, c := range cases {
		got := c.in
		got.Clamp()
		if got.TTL != c.want.TTL || got.BatchSize != c.want.BatchSize || got.BatchDelay != c.want.BatchDelay {
			t.Errorf("Clamp(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}
