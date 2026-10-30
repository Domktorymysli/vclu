package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/metrics"
)

// vclu_metrics reaches back at most the retention and answers with at most metrics.MaxPoints
// samples, in the same shape as before.
func TestMetricsToolCapsRangeAndPoints(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	var b strings.Builder
	for ts := now.Add(-3 * 24 * time.Hour); ts.Before(now); ts = ts.Add(time.Minute) {
		fmt.Fprintf(&b, "%d 1\n", ts.Unix())
	}
	os.MkdirAll(filepath.Join(dir, "t.x"), 0755)
	// one file is enough: the reader takes every file from the first day on
	if err := os.WriteFile(filepath.Join(dir, "t.x", now.Format("2006-01-02")+".ts"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	mc := metrics.NewCollector(dir, metrics.WithRingSize(10), metrics.WithRetainDays(1))
	mc.Register("t:x", "", 60)
	srv := phase2Harness(nil, nil)
	srv.SetMetricsCollector(mc)

	res, _ := srv.handleMetrics(devBotCtx(false), makeCallRequest(map[string]any{"path": "t:x", "range": "30d"}))
	out := parseJSON(t, res)
	samples, ok := out["samples"].([]any)
	if !ok || len(samples) == 0 || len(samples) > metrics.MaxPoints || out["count"] != float64(len(samples)) {
		t.Fatalf("answer = %v samples, count %v", len(samples), out["count"])
	}
	first := samples[0].([]any)
	if ts := int64(first[0].(float64)); ts < now.Add(-24*time.Hour-time.Minute).Unix() {
		t.Errorf("oldest sample %v is past the 1-day retention", time.Unix(ts, 0))
	}
}
