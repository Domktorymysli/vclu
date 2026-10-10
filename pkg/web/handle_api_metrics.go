package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

// writeMetricSamples answers with the path's samples over ?range= (default 2h).
func (s *Server) writeMetricSamples(c *gin.Context, path string) {
	rangeStr := c.DefaultQuery("range", "2h")
	duration := parseRange(rangeStr)
	since := time.Now().Add(-duration).Unix()

	samples := s.metricsCollector.Query(path, since)

	// Output: [[timestamp, value], ...]
	out := make([][2]float64, len(samples))
	for i, sample := range samples {
		out[i] = [2]float64{float64(sample.Timestamp), sample.Value}
	}
	c.JSON(http.StatusOK, out)
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
