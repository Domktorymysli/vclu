package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/notify"
	"github.com/gin-gonic/gin"
)

// notificationsList is GET /api/notifications.
type notificationsList struct {
	Entries []notify.Entry `json:"entries"`
	Unread  int            `json:"unread"`
	Worst   string         `json:"worst" example:"warning"` // the worst unread level, "" with none
}

func (s *Server) notificationsJSON(c *gin.Context) {
	entries, unread, worst := s.notifier.List()
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, notificationsList{Entries: entries, Unread: unread, Worst: worst})
}

// needNotifier answers 503 when the bell is not wired.
func (s *Server) needNotifier(c *gin.Context) bool {
	if s.notifier == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "notifications not available"})
		return false
	}
	return true
}

// handleAPINotifications lists the panel's notifications
//
//	@Summary		Notifications
//	@Description	The panel's notifications (the bell), the last changed first. `kind` "state" lasts until it is over (a new version, a module that does not load, plugin updates as one entry) and is unread again only when it grows; "event" (a failed login, a lock) is merged by `key` while unread (`count`), and a new one comes once it is read. `msg` and `params` are the stable fields; `title` and `body` are English made from them, `detail` is raw text without a translation (a module's error). A `link` starting with "#" is an action of the panel ("#update": the new-version window). `worst` is the worst unread level, "" with none.
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	web.notificationsList
//	@Failure		401	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/notifications [get]
func (s *Server) handleAPINotifications(c *gin.Context) {
	s.notificationsJSON(c)
}

// handleAPINotificationsRead marks notifications read
//
//	@Summary		Mark notifications read
//	@Description	Marks the entries with `ids` read, or every one with `all`. Answers the list as GET /api/notifications does.
//	@Tags			system
//	@Accept			json
//	@Produce		json
//	@Param			body	body		object{ids=[]int,all=bool}	true	"What to mark"
//	@Success		200		{object}	web.notificationsList
//	@Failure		400		{object}	web.APIError
//	@Failure		401		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/notifications/read [post]
func (s *Server) handleAPINotificationsRead(c *gin.Context) {
	if !s.needNotifier(c) {
		return
	}
	var body struct {
		IDs []int64 `json:"ids"`
		All bool    `json:"all"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || (len(body.IDs) == 0 && !body.All) {
		c.JSON(http.StatusBadRequest, gin.H{"error": `send {"ids": [1, 2]} or {"all": true}`})
		return
	}
	s.notifier.MarkRead(body.IDs, body.All)
	s.notificationsJSON(c)
}

// handleAPINotificationsClear removes the events
//
//	@Summary		Clear notifications
//	@Description	Removes every event, read or not; states stay until they are over. Answers the list.
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	web.notificationsList
//	@Failure		401	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/notifications/clear [post]
func (s *Server) handleAPINotificationsClear(c *gin.Context) {
	if !s.needNotifier(c) {
		return
	}
	s.notifier.Clear()
	s.notificationsJSON(c)
}

// muteFor parses "24h", "7d" or "forever" (0).
func muteFor(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "forever" {
		return 0, nil
	}
	var d time.Duration
	var err error
	if days, ok := strings.CutSuffix(v, "d"); ok {
		var n int
		n, err = strconv.Atoi(days)
		d = time.Duration(n) * 24 * time.Hour
	} else {
		d, err = time.ParseDuration(v)
	}
	if err != nil || d <= 0 || d > 366*24*time.Hour {
		return 0, errors.New(`"for" is a duration ("24h", "7d") or "forever"`)
	}
	return d, nil
}

// handleAPINotificationsMute mutes a key
//
//	@Summary		Mute a notification
//	@Description	Nothing with this `key` is added for `for` ("24h", "7d" or "forever"), nor passed to Notifications.on handlers, and its entry is removed. Saved in .vclu.json (`notifications.muted`); "forever" on `update:<version>` hides that version until a newer one. Answers the list.
//	@Tags			system
//	@Accept			json
//	@Produce		json
//	@Param			body	body		object{key=string,for=string}	true	"What to mute"
//	@Success		200		{object}	web.notificationsList
//	@Failure		400		{object}	web.APIError
//	@Failure		401		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/notifications/mute [post]
func (s *Server) handleAPINotificationsMute(c *gin.Context) {
	if !s.needNotifier(c) {
		return
	}
	var body struct {
		Key string `json:"key"`
		For string `json:"for"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": `send {"key": "...", "for": "24h"|"7d"|"forever"}`})
		return
	}
	d, err := muteFor(body.For)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.notifier.Mute(body.Key, d); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "saving settings: " + err.Error()})
		return
	}
	s.notificationsJSON(c)
}

// handleAPINotificationsUnmute lets a muted key come again
//
//	@Summary		Unmute a notification
//	@Description	Removes `key` from the muted ones. Answers the settings.
//	@Tags			system
//	@Accept			json
//	@Produce		json
//	@Param			body	body		object{key=string}	true	"The key"
//	@Success		200		{object}	notify.Settings
//	@Failure		400		{object}	web.APIError
//	@Failure		401		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/notifications/mute [delete]
func (s *Server) handleAPINotificationsUnmute(c *gin.Context) {
	if !s.needNotifier(c) {
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": `send {"key": "..."}`})
		return
	}
	if err := s.notifier.Unmute(body.Key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "saving settings: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.notifier.Settings())
}

// handleAPINotificationsSettings gives the sources and the muted keys
//
//	@Summary		Notification settings
//	@Description	Each source in the order of the settings, whether it adds notifications now and whether it does by default (log does not), and the keys muted now: `until` is RFC 3339 or "forever". The panel names a source by its translation `notify.source.<id>`.
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	notify.Settings
//	@Failure		401	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/notifications/settings [get]
func (s *Server) handleAPINotificationsSettings(c *gin.Context) {
	if !s.needNotifier(c) {
		return
	}
	c.JSON(http.StatusOK, s.notifier.Settings())
}

// handleAPINotificationsSettingsSave switches sources
//
//	@Summary		Switch notification sources
//	@Description	`sources` maps source ids to on (true) or off (false); sources not sent keep their setting, and one never switched keeps its default. Saved in .vclu.json (`notifications.sources`). A source turned off adds nothing (nor asks the plugin registries, for "plugin") and its states are resolved. Answers the settings.
//	@Tags			system
//	@Accept			json
//	@Produce		json
//	@Param			body	body		object{sources=map[string]bool}	true	"The sources to switch"
//	@Success		200		{object}	notify.Settings
//	@Failure		400		{object}	web.APIError
//	@Failure		401		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/notifications/settings [put]
func (s *Server) handleAPINotificationsSettingsSave(c *gin.Context) {
	if !s.needNotifier(c) {
		return
	}
	var body struct {
		Sources map[string]bool `json:"sources"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Sources == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": `send {"sources": {"log": true}}`})
		return
	}
	for id := range body.Sources {
		if _, ok := notify.Lookup(id); !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown source %q", id)})
			return
		}
	}
	if err := s.notifier.SetSources(body.Sources); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "saving settings: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.notifier.Settings())
}
