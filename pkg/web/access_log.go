package web

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// slowRequest is the time from which a successful request is written to the access log.
const slowRequest = time.Second

// accessLog writes a request in GIN's format only when it failed (400 and up) or took slow or more.
// A WebSocket or an event stream lasts as long as its viewer, so only its status counts.
func accessLog(out io.Writer, slow time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		if q := c.Request.URL.RawQuery; q != "" {
			path += "?" + q
		}
		stream := c.IsWebsocket()

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		stream = stream || strings.HasPrefix(c.Writer.Header().Get("Content-Type"), "text/event-stream")
		if status < 400 && (stream || latency < slow) {
			return
		}
		_, _ = fmt.Fprintf(out, "[GIN] %v | %3d | %13v | %15s | %-7s %#v\n%s",
			start.Format("2006/01/02 - 15:04:05"), status, latency, c.ClientIP(), c.Request.Method, path,
			c.Errors.ByType(gin.ErrorTypePrivate).String())
	}
}
