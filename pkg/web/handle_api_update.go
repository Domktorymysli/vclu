package web

import (
	"net/http"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/updatecheck"
	"github.com/gin-gonic/gin"
)

// SetUpdateChecker wires the new-version check shown in the top bar.
func (s *Server) SetUpdateChecker(c *updatecheck.Checker) {
	s.updateChecker = c
}

// updateRelease is a release with its summary and notes rendered to safe HTML in the
// panel's language (else English).
type updateRelease struct {
	updatecheck.Release
	HighlightsHTML   string   `json:"highlights_html,omitempty"`
	BeforeUpdateHTML []string `json:"before_update_html,omitempty"`
	NotesHTML        string   `json:"notes_html,omitempty"`
}

func renderRelease(r updatecheck.Release, lang string) updateRelease {
	ur := updateRelease{Release: r}
	sum, ok := r.Summary[lang]
	if !ok {
		sum = r.Summary["en"]
	}
	if sum.Highlights != "" {
		ur.HighlightsHTML = updatecheck.RenderInline(sum.Highlights)
	}
	for _, p := range sum.BeforeUpdate {
		ur.BeforeUpdateHTML = append(ur.BeforeUpdateHTML, updatecheck.RenderInline(p))
	}
	notes := r.Notes[lang]
	if notes == "" {
		notes = r.Notes["en"]
	}
	if notes != "" {
		ur.NotesHTML = updatecheck.RenderNotes(notes)
	}
	return ur
}

// updateStatus is the answer of GET /api/update while the check is on.
type updateStatus struct {
	Enabled         bool                 `json:"enabled"`
	CheckedAt       *time.Time           `json:"checked_at"`
	Current         string               `json:"current"`
	UpdateAvailable bool                 `json:"update_available"`
	Latest          *updatecheck.Release `json:"latest"`
	Releases        []updateRelease      `json:"releases"`
}

// handleAPIUpdate returns the last good answer of the new-version check
//
//	@Summary		New vCLU version
//	@Description	The last good answer of the check for new vCLU versions: whether one is available, the latest release and every release newer than the running one, newest first. `highlights_html`, `before_update_html` (from `summary`) and `notes_html` are rendered to safe HTML in the panel's language, else English. `checked_at` and `latest` are null before the first answer. When the check is off only `enabled: false` is returned.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	web.updateStatus
//	@Failure		401	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/update [get]
func (s *Server) handleAPIUpdate(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if s.updateChecker == nil || !config.UpdateCheckEnabled(s.serverDir) {
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}
	res := updateStatus{Enabled: true, Current: s.updateChecker.Version(), Releases: []updateRelease{}}
	if a, at := s.updateChecker.Last(); a != nil {
		at = at.UTC()
		res.CheckedAt, res.UpdateAvailable, res.Latest = &at, a.UpdateAvailable, a.Latest
		lang := s.getLang(c)
		for _, r := range a.Releases {
			res.Releases = append(res.Releases, renderRelease(r, lang))
		}
	}
	c.JSON(http.StatusOK, res)
}

// handleAPIUpdateSettings turns the new-version check on or off
//
//	@Summary		Turn the new-version check on or off
//	@Description	Saves `updateCheck.enabled` in .vclu.json. Off stops the checks at once; on schedules a check within minutes. `VCLU_UPDATE_CHECK=off` in the environment keeps it off whatever is saved (`forced_off`).
//	@Tags			settings
//	@Accept			json
//	@Produce		json
//	@Param			body	body		object{enabled=bool}	true	"The setting"
//	@Success		200		{object}	object{enabled=bool,forced_off=bool}
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/update/settings [put]
func (s *Server) handleAPIUpdateSettings(c *gin.Context) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": `send {"enabled": true|false}`})
		return
	}
	turnedOn := false
	err := updateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		was := mc.UpdateCheckOn()
		if mc.UpdateCheck != nil && mc.UpdateCheck.Enabled != nil && was == *body.Enabled {
			return errUnchanged
		}
		on := *body.Enabled
		mc.UpdateCheck = &config.VCLUUpdateCheckConfig{Enabled: &on}
		turnedOn = on && !was
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "saving settings: " + err.Error()})
		return
	}
	if turnedOn && s.updateChecker != nil {
		s.updateChecker.Reschedule()
	}
	forced := config.UpdateCheckForcedOff()
	c.JSON(http.StatusOK, gin.H{"enabled": *body.Enabled && !forced, "forced_off": forced})
}
