package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/metrics"
)

// GET /api/metrics/data?path=salda:outsideAir&range=2h
//
//	@Summary		Metric samples
//	@Description	Returns recorded samples of one dashboard metric over the requested time range as [timestamp, value] pairs (Unix seconds).
//	@Tags			stats
//	@Produce		json
//	@Param			path	query		string		true	"Metric path, e.g. salda:outsideAir"
//	@Param			range	query		string		false	"Time range, e.g. 30m, 2h, 7d (default 2h)"
//	@Success		200		{array}		[]float64	"Array of [timestamp, value] pairs"
//	@Failure		400		{object}	web.APIError
//	@Failure		503		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/metrics/data [get]
func (s *Server) handleAPIMetricsData(c *gin.Context) {
	if s.metricsCollector == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "metrics not available"})
		return
	}

	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	s.writeMetricSamples(c, path)
}

// writeMetricSamples answers with the path's samples over ?range= (default 2h), at most the
// retention back and at most metrics.MaxPoints of them.
func (s *Server) writeMetricSamples(c *gin.Context, path string) {
	d := parseRange(c.DefaultQuery("range", "2h"))
	var samples []metrics.Sample
	now := time.Now()
	step := chartStep(d)
	if old, upTo, ok := s.cloudSamples(c.Request.Context(), path, now.Add(-d), now, step, func(p [5]float64, _ int) float64 { return p[1] }); ok {
		// the older part from the cloud, the rest from the files in the same steps
		samples = append(old, stepAverages(s.metricsCollector.Query(path, upTo), step)...)
	} else {
		samples = s.metricsCollector.QueryRange(path, d)
	}

	// Output: [[timestamp, value], ...]
	out := make([][2]float64, len(samples))
	for i, sample := range samples {
		out[i] = [2]float64{float64(sample.Timestamp), sample.Value}
	}
	c.JSON(http.StatusOK, out)
}

// chartSteps are the steps a chart longer than vCLU keeps is drawn in.
var chartSteps = []int{60, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400}

// chartStep is the smallest step that keeps a chart over d within metrics.MaxPoints.
func chartStep(d time.Duration) int {
	for _, st := range chartSteps {
		if int64(d/time.Second)/int64(st) <= metrics.MaxPoints {
			return st
		}
	}
	return chartSteps[len(chartSteps)-1]
}

// cloudSamples is the part of path from since that vCLU no longer keeps, from what it sent the
// cloud (cloud.Client.MetricsHistory), one sample a step with the value value picks, and where
// that part ends. False when the cloud has nothing to add: the metric is not sent there, the range
// fits in what vCLU keeps, or the cloud cannot answer now.
func (s *Server) cloudSamples(ctx context.Context, path string, since, now time.Time, step int, value func(p [5]float64, step int) float64) ([]metrics.Sample, int64, bool) {
	kept := s.metricsCollector.Retention(now)
	if s.metricsHistory == nil || !since.Before(kept) || !s.metricsCollector.IsCloud(path) {
		return nil, 0, false
	}
	pts, upTo, err := s.metricsHistory.MetricsHistory(ctx, path, since.Unix(), kept.Unix(), step)
	if err != nil || len(pts) == 0 {
		return nil, 0, false
	}
	out := make([]metrics.Sample, len(pts))
	for i, p := range pts {
		out[i] = metrics.Sample{Timestamp: int64(p[0]), Value: value(p, step)}
	}
	return out, upTo, true
}

// stepAverages averages samples into steps of step seconds, each at its step's start.
func stepAverages(samples []metrics.Sample, step int) []metrics.Sample {
	var out []metrics.Sample
	var sum float64
	var n int
	for _, x := range samples {
		at := x.Timestamp / int64(step) * int64(step)
		if n > 0 && at != out[len(out)-1].Timestamp {
			out[len(out)-1].Value = sum / float64(n)
			sum, n = 0, 0
		}
		if n == 0 {
			out = append(out, metrics.Sample{Timestamp: at})
		}
		sum += x.Value
		n++
	}
	if n > 0 {
		out[len(out)-1].Value = sum / float64(n)
	}
	return out
}

// GET /api/metrics/list
//
//	@Summary		List dashboard metrics
//	@Description	Lists all registered dashboard metrics with their latest values. Returns an empty array (still 200) when the metrics collector is not available.
//	@Tags			stats
//	@Produce		json
//	@Success		200	{array}		metrics.MetricInfo
//	@Security		ApiKeyAuth
//	@Router			/api/metrics/list [get]
func (s *Server) handleAPIMetricsList(c *gin.Context) {
	if s.metricsCollector == nil {
		c.JSON(http.StatusOK, []struct{}{})
		return
	}
	c.JSON(http.StatusOK, s.metricsCollector.List())
}

// parseRange converts a human-readable range string to a duration.
// Supported: "2h", "24h", "7d", "30m", "1h30m" etc.
// Falls back to time.ParseDuration, with added "d" (days) support.
func parseRange(s string) time.Duration {
	s = strings.TrimSpace(s)

	// Handle "Nd" suffix for days
	if strings.HasSuffix(s, "d") {
		prefix := strings.TrimSuffix(s, "d")
		if days, err := strconv.Atoi(prefix); err == nil {
			return time.Duration(days) * 24 * time.Hour
		}
	}

	if d, err := time.ParseDuration(s); err == nil {
		return d
	}

	// Default: 2 hours
	return 2 * time.Hour
}
