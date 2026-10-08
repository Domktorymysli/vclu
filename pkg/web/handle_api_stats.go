package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// handleStatsPage renders the statistics page
func (s *Server) handleStatsPage(c *gin.Context) {
	s.renderHTML(c, http.StatusOK, "stats.html", gin.H{
		"currentPage":     "stats",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"t":               s.getTranslations(c)["t"],
		"lang":            s.getLang(c),
	})
}

// handleAPIStats returns current metrics snapshot
//
//	@Summary		Metrics snapshot
//	@Description	Current application metrics snapshot (uptime, HTTP, Lua, MQTT, HomeKit, protocol sections), enriched with live MQTT broker counters, HomeKit status and Lua runtime busy/lag gauges when available.
//	@Tags			stats
//	@Produce		json
//	@Success		200	{object}	stats.AppMetricsSnapshot
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/stats [get]
func (s *Server) handleAPIStats(c *gin.Context) {
	if s.metrics == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Metrics not available",
		})
		return
	}

	snapshot := s.metrics.Snapshot()

	// Enrich with MQTT broker stats if available
	if s.brokerController != nil {
		broker := s.brokerController.GetMQTTBroker()
		if broker != nil {
			brokerStats := broker.Stats()
			// Override MQTT section with broker stats
			snapshot.MQTT.Connected = brokerStats.Running
			snapshot.MQTT.Published = brokerStats.MessagesSent
			snapshot.MQTT.Received = brokerStats.MessagesReceived
		}
	}

	// Enrich with HomeKit status
	if s.homekitServer != nil {
		hkStatus := s.homekitServer.GetStatus()
		snapshot.HomeKit.Running = hkStatus.Running
	}

	// Enrich with CLU Protocol running status
	snapshot.Protocol.Running = s.isConfigured

	// Enrich with runtime metrics from collector
	if s.metricsCollector != nil {
		for _, m := range s.metricsCollector.List() {
			switch m.Path {
			case "runtime:busy_percent":
				snapshot.Lua.BusyPercent = m.Latest
			case "runtime:loop_lag_ms":
				snapshot.Lua.LoopLagMs = m.Latest
			}
		}
	}

	c.JSON(http.StatusOK, snapshot)
}

// handleAPIStatsTimeSeries returns time series data for charts
//
//	@Summary		Metrics time series
//	@Description	Time series data for the statistics charts, keyed by metric name. Pass "since" (Unix milliseconds) to receive only data points recorded after that moment, for incremental chart updates.
//	@Tags			stats
//	@Produce		json
//	@Param			since	query		integer	false	"Unix timestamp in milliseconds; return only newer data points"
//	@Success		200		{object}	map[string][]stats.DataPoint
//	@Failure		503		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/stats/timeseries [get]
func (s *Server) handleAPIStatsTimeSeries(c *gin.Context) {
	if s.metrics == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Metrics not available",
		})
		return
	}

	// Check for "since" parameter for incremental updates
	if sinceStr := c.Query("since"); sinceStr != "" {
		if sinceMs, err := strconv.ParseInt(sinceStr, 10, 64); err == nil {
			c.JSON(http.StatusOK, s.metrics.GetTimeSeriesSince(time.UnixMilli(sinceMs)))
			return
		}
	}

	// Return all time series data
	c.JSON(http.StatusOK, s.metrics.GetTimeSeries())
}

// handleMetrics returns metrics in Prometheus text format
//
//	@Summary		Prometheus metrics
//	@Description	Application metrics in Prometheus text exposition format (version 0.0.4), for scraping. Returns a comment line with status 503 when metrics are not available.
//	@Tags			system
//	@Produce		plain
//	@Success		200	{string}	string	"Prometheus text exposition format"
//	@Failure		503	{string}	string	"# Metrics not available"
//	@Security		ApiKeyAuth
//	@Router			/metrics [get]
func (s *Server) handleMetrics(c *gin.Context) {
	if s.metrics == nil {
		c.String(http.StatusServiceUnavailable, "# Metrics not available\n")
		return
	}

	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	c.String(http.StatusOK, s.metrics.PrometheusFormat())
}
