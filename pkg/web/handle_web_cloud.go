package web

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Domktorymysli/vclu-cloud/proto"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/cloud"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/i18n"
)

// SetCloudClient wires the vclu cloud connection (nil hides the page).
func (s *Server) SetCloudClient(c *cloud.Client) { s.cloudClient = c }

func (s *Server) handleCloud(c *gin.Context) {
	trans := s.getTranslations(c)
	lang, _ := trans["lang"].(string)
	i18nJSON, err := json.Marshal(i18n.GetSection(lang, "cloud"))
	if err != nil {
		i18nJSON = []byte("{}")
	}
	var st cloud.Status
	if s.cloudClient != nil {
		st = s.cloudClient.Status()
	}
	statusJSON, _ := json.Marshal(st)
	// the home's people as the cloud last sent them, and where to manage them
	var members []proto.Member
	if s.local != nil && st.HomeID != 0 {
		members = s.local.Members()
	}
	manage := ""
	if st.HomeID != 0 && st.URL != "" {
		manage = strings.TrimSuffix(st.URL, "/") + "/app/homes/" + strconv.FormatInt(st.HomeID, 10) + "/members"
	}
	s.renderHTML(c, http.StatusOK, "cloud.html", gin.H{
		"cloudMembers": members,
		"cloudManage":  manage,
		"title":        "Cloud",
		"currentPage":  "cloud",
		"configured":   s.isConfigured,
		"available":    s.cloudClient != nil,
		"cloudI18n":    template.JS(i18nJSON),
		"cloudStatus":  template.JS(statusJSON),
		"lang":         trans["lang"],
		"t":            trans["t"],
	})
}

// cloudReady answers 503 when the cloud client is not wired.
func (s *Server) cloudReady(c *gin.Context) bool {
	if s.cloudClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cloud client not available"})
		return false
	}
	return true
}

// cloudAdmin allows pairing and unpairing only to the web session (or local
// web with auth disabled), never to an API key: whoever enters the code
// attaches this house to their cloud account.
func (s *Server) cloudAdmin(c *gin.Context) bool {
	if !s.cloudReady(c) {
		return false
	}
	if c.GetString("auth_method") == "session" || !config.IsAuthEnabled(s.serverDir) {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{"error": "Cloud pairing needs a web session"})
	return false
}

func (s *Server) handleAPICloudStatus(c *gin.Context) {
	if !s.cloudReady(c) {
		return
	}
	c.JSON(http.StatusOK, s.cloudClient.Status())
}

func (s *Server) handleAPICloudPair(c *gin.Context) {
	if !s.cloudAdmin(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	st, err := s.cloudClient.Pair(ctx)
	if errors.Is(err, cloud.ErrPaired) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "status": st})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "status": st})
		return
	}
	c.JSON(http.StatusOK, st)
}

func (s *Server) handleAPICloudPairCancel(c *gin.Context) {
	if !s.cloudAdmin(c) {
		return
	}
	s.cloudClient.CancelPairing()
	c.JSON(http.StatusOK, s.cloudClient.Status())
}

func (s *Server) handleAPICloudUnpair(c *gin.Context) {
	if !s.cloudAdmin(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := s.cloudClient.Unpair(ctx); err != nil {
		// The token is gone either way; the warning says the cloud was not told.
		c.JSON(http.StatusOK, gin.H{"warning": err.Error(), "status": s.cloudClient.Status()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": s.cloudClient.Status()})
}

// handleAPICloudEnabled switches the connection off (?on=0) or on (?on=1), keeping the pairing.
func (s *Server) handleAPICloudEnabled(c *gin.Context) {
	if !s.cloudAdmin(c) {
		return
	}
	if err := s.cloudClient.SetEnabled(c.Query("on") == "1"); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "status": s.cloudClient.Status()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": s.cloudClient.Status()})
}

func (s *Server) handleAPICloudReconnect(c *gin.Context) {
	if !s.cloudAdmin(c) {
		return
	}
	if err := s.cloudClient.Reconnect(); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.cloudClient.Status())
}
