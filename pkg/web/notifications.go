package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/notify"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/Domktorymysli/vclu/pkg/updatecheck"
	"github.com/gin-gonic/gin"
)

// pluginCheckTimeout bounds the background check of the plugin registries.
const pluginCheckTimeout = 2 * time.Minute

// SetNotifier wires the panel's bell; nil leaves it empty. Notices of versions older than the
// running one are resolved at once.
func (s *Server) SetNotifier(n *notify.Store) {
	s.notifier = n
	cur := strings.TrimPrefix(s.version, "v")
	n.Resolve(func(e notify.Entry) bool {
		v, ok := strings.CutPrefix(e.Key, "update:")
		return ok && atLeast(cur, v)
	})
	if !config.UpdateCheckEnabled(s.serverDir) {
		s.resolveUpdateNotices()
	}
	s.resolvePluginNotices()
}

// resolveUpdateNotices resolves the new-version notices: the check is off, nothing would
// resolve them later.
func (s *Server) resolveUpdateNotices() {
	s.notifier.Resolve(func(e notify.Entry) bool { return strings.HasPrefix(e.Key, "update:") })
}

// atLeast is whether version a is v or newer; false when they do not compare.
func atLeast(a, v string) bool {
	cmp, ok := plugins.CompareVersions(a, v)
	return ok && cmp >= 0
}

// UpdateAnswered takes a good answer of the new-version check (updatecheck.Options.OnAnswer):
// a notice of the latest version, older ones resolved; then the plugins are checked.
func (s *Server) UpdateAnswered(a *updatecheck.Answer) {
	if s.notifier == nil || a == nil {
		return
	}
	latest := ""
	if a.UpdateAvailable && a.Latest != nil && !atLeast(a.Current, a.Latest.Version) {
		latest = a.Latest.Version
	}
	s.notifier.Resolve(func(e notify.Entry) bool {
		v, ok := strings.CutPrefix(e.Key, "update:")
		return ok && v != latest
	})
	if latest != "" {
		s.notifier.Add(notify.Entry{
			Kind: notify.KindState, Source: notify.SourceUpdate, Level: notify.LevelInfo,
			Key: "update:" + latest, Msg: notify.MsgUpdate, Params: map[string]string{"version": latest},
		})
	}
	if s.pluginCheck.CompareAndSwap(false, true) {
		go func() {
			defer s.pluginCheck.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), pluginCheckTimeout)
			defer cancel()
			s.checkPluginUpdates(ctx)
		}()
	}
}

// checkPluginUpdates adds a notice for each installed plugin with a newer version in its
// registry; a plugin whose registry did not answer keeps its notice.
func (s *Server) checkPluginUpdates(ctx context.Context) {
	mc, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || mc == nil || mc.Plugins == nil || len(mc.Plugins.Installed) == 0 {
		s.resolvePluginNotices()
		return
	}
	found, _ := availablePlugins(ctx, mc)
	s.pluginNotices(found)
}

// pluginNotices turns the registries' answer into notices.
func (s *Server) pluginNotices(found []availablePlugin) {
	newest := map[string]string{} // checked plugin -> version to update to, "" when current
	names := map[string]string{}
	for _, a := range found {
		if !a.Installed {
			continue
		}
		if a.UpdateAvailable {
			newest[a.FullID], names[a.FullID] = a.Meta.Version, a.Meta.Name
		} else if _, ok := newest[a.FullID]; !ok {
			newest[a.FullID] = ""
		}
	}
	s.notifier.Resolve(func(e notify.Entry) bool {
		id, v, ok := pluginKey(e.Key)
		want, checked := newest[id]
		return ok && checked && want != v
	})
	s.resolvePluginNotices()
	for id, v := range newest {
		if v == "" {
			continue
		}
		name := names[id]
		if name == "" {
			name = id
		}
		s.notifier.Add(notify.Entry{
			Kind: notify.KindState, Source: notify.SourcePlugin, Level: notify.LevelInfo,
			Key: "plugin:" + id + ":" + v, Msg: notify.MsgPlugin, Link: "/plugins",
			Params: map[string]string{"plugin": name, "version": v},
		})
	}
}

// PluginsReloaded takes the versions the plugin loader loaded (full id -> version), after a
// (re)load that may have updated files: notices of versions now running are resolved.
func (s *Server) PluginsReloaded(loaded map[string]string) {
	s.resolvePluginNoticesWith(loaded)
}

// resolvePluginNotices resolves the notices of plugins no longer installed or already at
// that version; it reads only .vclu.json.
func (s *Server) resolvePluginNotices() { s.resolvePluginNoticesWith(nil) }

func (s *Server) resolvePluginNoticesWith(loaded map[string]string) {
	if s.notifier == nil {
		return
	}
	mc, err := config.ReadMasterConfig(s.serverDir)
	if err != nil {
		return
	}
	installed := map[string]string{}
	if mc != nil && mc.Plugins != nil {
		for _, p := range mc.Plugins.Installed {
			installed[p.ID] = p.Version
			if v := loaded[p.ID]; v != "" && newerVersion(v, p.Version) {
				installed[p.ID] = v
			}
		}
	}
	s.notifier.Resolve(func(e notify.Entry) bool {
		id, v, ok := pluginKey(e.Key)
		cur, there := installed[id]
		return ok && (!there || !newerVersion(v, cur))
	})
}

// pluginKey splits "plugin:<id>:<version>".
func pluginKey(key string) (id, version string, ok bool) {
	rest, ok := strings.CutPrefix(key, "plugin:")
	if !ok {
		return "", "", false
	}
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// notifyLock records a lock started for wait; ip is "" for a lock of a dashboard.
func (s *Server) notifyLock(msg, key string, params map[string]string, wait time.Duration, ip string) {
	if params == nil {
		params = map[string]string{}
	}
	if wait > 0 {
		params["until"] = time.Now().Add(wait).UTC().Truncate(time.Second).Format(time.RFC3339)
	}
	s.notifier.Add(notify.Entry{
		Kind: notify.KindEvent, Source: notify.SourceLock, Level: notify.LevelWarning,
		Key: "lock:" + key, Msg: msg, Params: params, IP: ip, Link: "/settings",
	})
}

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
//	@Description	The panel's notifications (the bell), the last changed first. `kind` "state" lasts until it is over (a new version, a module that does not load) and comes back unread never; "event" (a failed login, a lock) is merged by `key` while unread (`count`), and a new one comes once it is read. `title` and `body` are English; `msg` and `params` are for translating them. `worst` is the worst unread level, "" with none.
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
//	@Description	Nothing with this `key` is added for `for` ("24h", "7d" or "forever"), nor passed to Notifications.on handlers, and its entry is removed. Saved in .vclu.json (`notifications.muted`). Answers the list.
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
	s.muteAnswer(c, body.Key, d)
}

func (s *Server) muteAnswer(c *gin.Context, key string, d time.Duration) {
	if err := s.notifier.Mute(key, d); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "saving settings: " + err.Error()})
		return
	}
	s.notificationsJSON(c)
}

// handleAPINotificationsHideUpdate hides a version's notice
//
//	@Summary		Hide a new version
//	@Description	Mutes the notice of this vclu version for good (key `update:<version>`); a newer version comes again. Answers the list.
//	@Tags			system
//	@Accept			json
//	@Produce		json
//	@Param			body	body		object{version=string}	true	"The version"
//	@Success		200		{object}	web.notificationsList
//	@Failure		400		{object}	web.APIError
//	@Failure		401		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/notifications/hide-update [post]
func (s *Server) handleAPINotificationsHideUpdate(c *gin.Context) {
	if !s.needNotifier(c) {
		return
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Version) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": `send {"version": "1.13.0"}`})
		return
	}
	s.muteAnswer(c, "update:"+strings.TrimSpace(body.Version), 0)
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
//	@Description	Each source (update, plugin, module, log, login, lock) and whether it adds notifications (log is off until turned on), and the keys muted now: `until` is RFC 3339 or "forever".
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

// handleAPINotificationsSettingsSave turns sources off
//
//	@Summary		Turn notification sources off
//	@Description	`disabled` lists the sources that add nothing (every other one adds); saved in .vclu.json (`notifications.disabled`). Answers the settings.
//	@Tags			system
//	@Accept			json
//	@Produce		json
//	@Param			body	body		object{disabled=[]string}	true	"The sources turned off"
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
		Disabled *[]string `json:"disabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Disabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": `send {"disabled": ["log"]}`})
		return
	}
	for _, id := range *body.Disabled {
		if !isSource(id) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown source %q", id)})
			return
		}
	}
	if err := s.notifier.SetDisabled(*body.Disabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "saving settings: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, s.notifier.Settings())
}

func isSource(id string) bool {
	for _, s := range notify.Sources {
		if s == id {
			return true
		}
	}
	return false
}
