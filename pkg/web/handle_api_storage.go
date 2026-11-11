package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/storage"
	"github.com/gin-gonic/gin"
)

// SetStorageMonitor wires the disk space card, banner and GET /api/storage.
func (s *Server) SetStorageMonitor(m *storage.Monitor) {
	s.storage = m
}

// handleAPIStorage returns the last disk space measurement
//
//	@Summary		Disk space
//	@Description	The file system of the data directory (total, free, free in %, the thresholds), what vCLU keeps on it (history, metrics, backups of updates, promotes and the panel, plugins, the rest of the data directory; sizes on disk as `du` counts them) and how fast it grows: history written in the last 24 h, log output to stdout in the last hour, and `daysToFull` at that rate (null without growth or at 90 days and more). `level` is `low` below 10% or 500 MB free, whichever is larger, and `critical` below 3% or 100 MB. Directories are walked at start and every 15 minutes, free space is read every minute; `ready` is false until the first walk ends.
//	@Tags			stats
//	@Produce		json
//	@Success		200	{object}	storage.Snapshot
//	@Failure		401	{object}	web.APIError
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/storage [get]
func (s *Server) handleAPIStorage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if s.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage monitor not available"})
		return
	}
	c.JSON(http.StatusOK, s.storage.Snapshot())
}

// storageView is the Storage card of the status page, sizes formatted.
type storageView struct {
	Ready       bool
	Level       string
	Path        string
	Total, Free string
	FreePercent string
	Error       string
	Parts       []storagePartView
	History24h  string
	LogPerHour  string
	DaysToFull  int
	Full        bool // DaysToFull is set
}

type storagePartView struct {
	Name, Size string
	Percent    float64 // of the parts' sum, for the bar
}

func newStorageView(snap storage.Snapshot, lang string) *storageView {
	v := &storageView{
		Ready: snap.Ready, Level: string(snap.Level), Path: snap.FileSystem.Path, Error: snap.FileSystem.Error,
		Total:       formatBytes(snap.FileSystem.TotalBytes, lang),
		Free:        formatBytes(snap.FileSystem.FreeBytes, lang),
		FreePercent: localDecimal(fmt.Sprintf("%.1f", snap.FileSystem.FreePercent), lang),
		History24h:  formatBytes(snap.Growth.HistoryLast24hBytes, lang),
		LogPerHour:  formatBytes(snap.Growth.LogBytesPerHour, lang),
	}
	if d := snap.Growth.DaysToFull; d != nil {
		v.DaysToFull, v.Full = *d, true
	}
	var sum int64
	for _, p := range snap.Parts {
		sum += p.Bytes
	}
	for _, p := range snap.Parts {
		pv := storagePartView{Name: p.Name, Size: formatBytes(p.Bytes, lang)}
		if sum > 0 {
			pv.Percent = float64(p.Bytes) * 100 / float64(sum)
		}
		v.Parts = append(v.Parts, pv)
	}
	return v
}

// formatBytes gives a size in binary units with one decimal from KB up.
func formatBytes(n int64, lang string) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	f, i := float64(n)/unit, 0
	for f >= unit && i < 3 {
		f /= unit
		i++
	}
	return localDecimal(fmt.Sprintf("%.1f", f), lang) + " " + []string{"KB", "MB", "GB", "TB"}[i]
}

func localDecimal(s, lang string) string {
	if lang == "pl" {
		return strings.Replace(s, ".", ",", 1)
	}
	return s
}

// storagePrometheus is the measurement in the Prometheus text format.
func (s *Server) storagePrometheus() string {
	if s.storage == nil {
		return ""
	}
	snap := s.storage.Snapshot()
	if !snap.Ready {
		return ""
	}
	var history int64
	for _, p := range snap.Parts {
		if p.Name == storage.PartHistory {
			history = p.Bytes
		}
	}
	var b strings.Builder
	gauge := func(name, help string, v int64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, v)
	}
	if snap.FileSystem.Error == "" {
		gauge("vclu_disk_free_bytes", "Free space on the data directory's file system", snap.FileSystem.FreeBytes)
	}
	gauge("vclu_history_bytes", "History files on disk", history)
	gauge("vclu_log_bytes_per_hour", "Log output to stdout in the last hour", snap.Growth.LogBytesPerHour)
	return b.String()
}
