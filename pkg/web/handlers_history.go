package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/gin-gonic/gin"
)

// SetHistoryStore connects the state change history (Tools -> History).
func (s *Server) SetHistoryStore(store *history.Store) {
	s.historyStore = store
}

// handleHistory renders Tools -> History and audit.
func (s *Server) handleHistory(c *gin.Context) {
	trans := s.getTranslations(c)
	// the page script gets the history.* texts only
	historyT := map[string]string{}
	if all, ok := trans["t"].(map[string]string); ok {
		for k, v := range all {
			if strings.HasPrefix(k, "history.") {
				historyT[strings.TrimPrefix(k, "history.")] = v
			}
		}
	}
	s.renderHTML(c, http.StatusOK, "history.html", gin.H{
		"historyT":        historyT,
		"title":           "History",
		"currentPage":     "history",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"lang":            trans["lang"],
		"t":               trans["t"],
		"langs":           trans["langs"],
	})
}

// historyAdmin allows changing settings and deleting data only to the web
// session (or local web with auth disabled), never to an API key.
func (s *Server) historyAdmin(c *gin.Context) bool {
	if c.GetString("auth_method") == "session" || !config.IsAuthEnabled(s.serverDir) {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{"error": "History settings and data deletion need a web session"})
	return false
}

func (s *Server) historyReady(c *gin.Context) bool {
	if s.historyStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "History not available"})
		return false
	}
	return true
}

// handleAPIHistory pages through the history, newest first.
//
//	@Summary		State change history
//	@Description	Changes of all objects, newest first, from memory and the daily files. Filters: path, origin, actor, q (text), since/until (RFC 3339 or Unix ms). Pass next as cursor for the following page.
//	@Tags			history
//	@Produce		json
//	@Success		200	{object}	history.SearchResult
//	@Router			/api/history [get]
func (s *Server) handleAPIHistory(c *gin.Context) {
	if !s.historyReady(c) {
		return
	}
	q := history.SearchQuery{
		Path:   c.Query("path"),
		Origin: c.Query("origin"),
		Actor:  c.Query("actor"),
		Text:   c.Query("q"),
		Cursor: c.Query("cursor"),
		Ctx:    c.Request.Context(),
	}
	q.Limit, _ = strconv.Atoi(c.Query("limit"))
	var err error
	if q.Since, err = parseHistoryTime(c.Query("since")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid since: " + err.Error()})
		return
	}
	if q.Until, err = parseHistoryTime(c.Query("until")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid until: " + err.Error()})
		return
	}
	res, err := s.historyStore.Search(q)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// parseHistoryTime accepts RFC 3339 or Unix milliseconds; "" is no bound.
func parseHistoryTime(v string) (int64, error) {
	if v == "" {
		return 0, nil
	}
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
		return ms, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return 0, fmt.Errorf("RFC 3339 or Unix ms expected")
	}
	return t.UnixMilli(), nil
}

// handleAPIHistoryStatus reports memory and disk use of the history.
func (s *Server) handleAPIHistoryStatus(c *gin.Context) {
	if !s.historyReady(c) {
		return
	}
	c.JSON(http.StatusOK, s.historyStore.Status())
}

// historySettings is the history section as edited on the page.
type historySettings struct {
	Enabled              bool `json:"enabled"`
	MemoryHours          int  `json:"memoryHours"`
	MaxEntriesPerObject  int  `json:"maxEntriesPerObject"`
	MaxEntriesTotal      int  `json:"maxEntriesTotal"`
	DiskEnabled          bool `json:"diskEnabled"`
	RetentionDays        int  `json:"retentionDays"`
	MaxDiskMB            int  `json:"maxDiskMB"`
	FlushIntervalSeconds int  `json:"flushIntervalSeconds"`
}

func settingsOf(cfg history.Config) historySettings {
	return historySettings{
		Enabled: cfg.Enabled, MemoryHours: cfg.MemoryHours,
		MaxEntriesPerObject: cfg.MaxEntriesPerObject, MaxEntriesTotal: cfg.MaxEntriesTotal,
		DiskEnabled: cfg.DiskEnabled, RetentionDays: cfg.RetentionDays, MaxDiskMB: cfg.MaxDiskMB,
		FlushIntervalSeconds: int(cfg.FlushInterval / time.Second),
	}
}

func (h historySettings) validate() error {
	check := func(name string, v, lo, hi int) error {
		if v < lo || v > hi {
			return fmt.Errorf("%s must be between %d and %d", name, lo, hi)
		}
		return nil
	}
	for _, err := range []error{
		check("memoryHours", h.MemoryHours, 1, 168),
		check("maxEntriesPerObject", h.MaxEntriesPerObject, 100, 100000),
		check("maxEntriesTotal", h.MaxEntriesTotal, h.MaxEntriesPerObject, 200000),
		check("retentionDays", h.RetentionDays, 1, 3650),
		check("maxDiskMB", h.MaxDiskMB, 16, 102400),
		check("flushIntervalSeconds", h.FlushIntervalSeconds, 10, 3600),
	} {
		if err != nil {
			return err
		}
	}
	return nil
}

// handleAPIHistorySettings returns the saved settings (what the next start
// uses) and whether they differ from the running ones in a way that needs a restart.
func (s *Server) handleAPIHistorySettings(c *gin.Context) {
	if !s.historyReady(c) {
		return
	}
	saved, err := history.LoadConfig(s.serverDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "reading settings: " + err.Error()})
		return
	}
	cur := s.historyStore.Config()
	c.JSON(http.StatusOK, gin.H{
		"settings":        settingsOf(saved),
		"defaults":        settingsOf(history.DefaultConfig()),
		"restartRequired": saved.Enabled != cur.Enabled || saved.DiskEnabled != cur.DiskEnabled,
	})
}

// handleAPIHistorySettingsSave validates, saves to .vclu.json and only then
// applies the limits live. Turning history or the disk on or off takes
// effect after a restart (restartRequired).
func (s *Server) handleAPIHistorySettingsSave(c *gin.Context) {
	if !s.historyReady(c) || !s.historyAdmin(c) {
		return
	}
	var h historySettings
	if err := c.ShouldBindJSON(&h); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if err := h.validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	err := config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		enabled, disk := h.Enabled, h.DiskEnabled
		mc.History = &config.VCLUHistoryConfig{
			Enabled: &enabled, MemoryHours: h.MemoryHours,
			MaxEntriesPerObject: h.MaxEntriesPerObject, MaxEntriesTotal: h.MaxEntriesTotal,
			DiskEnabled: &disk, RetentionDays: h.RetentionDays, MaxDiskMB: h.MaxDiskMB,
			FlushIntervalSeconds: h.FlushIntervalSeconds,
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "saving settings: " + err.Error()})
		return
	}
	cur := s.historyStore.Config()
	next := cur
	next.MemoryHours, next.MaxEntriesPerObject, next.MaxEntriesTotal = h.MemoryHours, h.MaxEntriesPerObject, h.MaxEntriesTotal
	next.RetentionDays, next.MaxDiskMB = h.RetentionDays, h.MaxDiskMB
	next.FlushInterval = time.Duration(h.FlushIntervalSeconds) * time.Second
	s.historyStore.Apply(next)
	c.JSON(http.StatusOK, gin.H{
		"settings":        settingsOf(s.historyStore.Config()),
		"restartRequired": h.Enabled != cur.Enabled || h.DiskEnabled != cur.DiskEnabled,
	})
}

// handleAPIHistoryFlush writes pending entries to disk now.
func (s *Server) handleAPIHistoryFlush(c *gin.Context) {
	if !s.historyReady(c) || !s.historyAdmin(c) {
		return
	}
	if err := s.historyStore.Flush(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.historyStore.Status())
}

// confirmed requires {"confirm": true}: data deletion is never a side effect.
func confirmed(c *gin.Context) bool {
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if c.ShouldBindJSON(&body) != nil || !body.Confirm {
		c.JSON(http.StatusBadRequest, gin.H{"error": `send {"confirm": true}`})
		return false
	}
	return true
}

// handleAPIHistoryClearMemory empties the in-memory history; files stay.
func (s *Server) handleAPIHistoryClearMemory(c *gin.Context) {
	if !s.historyReady(c) || !s.historyAdmin(c) || !confirmed(c) {
		return
	}
	s.historyStore.ClearMemory()
	c.JSON(http.StatusOK, s.historyStore.Status())
}

// handleAPIHistoryClearDisk deletes the history files and pending entries;
// memory stays.
func (s *Server) handleAPIHistoryClearDisk(c *gin.Context) {
	if !s.historyReady(c) || !s.historyAdmin(c) || !confirmed(c) {
		return
	}
	if err := s.historyStore.ClearDisk(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.historyStore.Status())
}
