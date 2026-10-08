package web

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleDebug renders the debug page
func (s *Server) handleDebug(c *gin.Context) {
	trans := s.getTranslations(c)

	data := gin.H{
		"title":           "Debug Logs",
		"currentPage":     "debug",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"lang":            trans["lang"],
		"t":               trans["t"],
		"langs":           trans["langs"],
	}

	s.renderHTML(c, http.StatusOK, "debug.html", data)
}

// handleAPIDebugStream handles SSE streaming of logs
func (s *Server) handleAPIDebugStream(c *gin.Context) {
	if s.logStream == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "log stream not available"})
		return
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // Disable nginx buffering

	// Send history as initial batch
	history := s.logStream.GetHistory()
	for _, entry := range history {
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		c.SSEvent("message", string(data))
	}
	c.Writer.Flush()

	// Subscribe to new logs
	ch := s.logStream.Subscribe()
	defer s.logStream.Unsubscribe(ch)

	// Stream until client disconnects
	for {
		select {
		case entry := <-ch:
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			c.SSEvent("message", string(data))
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			return
		}
	}
}

// handleAPIDebugSources returns available log sources
func (s *Server) handleAPIDebugSources(c *gin.Context) {
	if s.logStream == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "log stream not available"})
		return
	}

	sources := s.logStream.GetSources()
	c.JSON(http.StatusOK, gin.H{
		"sources": sources,
	})
}

// handleAPIDebugHistory returns buffered log history
func (s *Server) handleAPIDebugHistory(c *gin.Context) {
	if s.logStream == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "log stream not available"})
		return
	}

	history := s.logStream.GetHistory()
	c.JSON(http.StatusOK, gin.H{
		"entries": history,
		"count":   len(history),
	})
}

// handleAPIDebugStats returns log stream statistics
func (s *Server) handleAPIDebugStats(c *gin.Context) {
	if s.logStream == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "log stream not available"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"entryCount":  s.logStream.EntryCount(),
		"clientCount": s.logStream.ClientCount(),
		"sources":     s.logStream.GetSources(),
	})
}
