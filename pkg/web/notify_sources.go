package web

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/notify"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/Domktorymysli/vclu/pkg/updatecheck"
)

// pluginCheckTimeout bounds the background check of the plugin registries.
const pluginCheckTimeout = 2 * time.Minute

// pluginUpdatesKey is the one state of every plugin with a newer version.
const pluginUpdatesKey = "plugin-updates"

// pluginPermissionsKey is the one state of every plugin asking for more than the user allowed.
const pluginPermissionsKey = "plugin-permissions"

// SetNotifier wires the panel's bell; nil leaves it empty. Notices of versions older than the
// running one are resolved at once.
func (s *Server) SetNotifier(n *notify.Store) {
	s.notifier = n
	cur := strings.TrimPrefix(s.version, "v")
	n.Resolve(func(e notify.Entry) bool { return isUpdateNotice(e) && atLeast(cur, e.Params["version"]) })
	if !config.UpdateCheckEnabled(s.serverDir) {
		s.resolveUpdateNotices()
	}
	s.resolvePluginNotices()
}

func isUpdateNotice(e notify.Entry) bool {
	return e.Source == notify.SourceUpdate && e.Msg == notify.MsgUpdate
}

func isPluginUpdates(e notify.Entry) bool {
	return e.Source == notify.SourcePlugin && e.Msg == notify.MsgPluginUpdates
}

// resolveUpdateNotices resolves the new-version notices: the check is off, nothing would
// resolve them later.
func (s *Server) resolveUpdateNotices() { s.notifier.Resolve(isUpdateNotice) }

// atLeast is whether version a is v or newer; false when they do not compare.
func atLeast(a, v string) bool {
	cmp, ok := plugins.CompareVersions(a, v)
	return ok && cmp >= 0
}

// UpdateAnswered takes a good answer of the new-version check (updatecheck.Options.OnAnswer):
// a notice of the latest version, older ones resolved; then the plugins are checked. A source
// turned off costs nothing: no notice, no request to the plugin registries.
func (s *Server) UpdateAnswered(a *updatecheck.Answer) {
	if s.notifier == nil || a == nil {
		return
	}
	if s.notifier.Enabled(notify.SourceUpdate) {
		s.updateNotice(a)
	}
	if s.notifier.Enabled(notify.SourcePlugin) && s.pluginCheck.CompareAndSwap(false, true) {
		go func() {
			defer s.pluginCheck.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), pluginCheckTimeout)
			defer cancel()
			s.checkPluginUpdates(ctx)
		}()
	}
}

func (s *Server) updateNotice(a *updatecheck.Answer) {
	latest := ""
	if a.UpdateAvailable && a.Latest != nil && !atLeast(a.Current, a.Latest.Version) {
		latest = a.Latest.Version
	}
	s.notifier.Resolve(func(e notify.Entry) bool { return isUpdateNotice(e) && e.Params["version"] != latest })
	if latest != "" {
		s.notifier.Add(notify.Entry{
			Kind: notify.KindState, Source: notify.SourceUpdate, Level: notify.LevelInfo, Key: "update:" + latest,
			Msg: notify.MsgUpdate, Params: map[string]string{"version": latest}, Link: "#update",
		})
	}
}

// pluginUpdate is an installed plugin with a newer version in its registry.
type pluginUpdate struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// checkPluginUpdates reads the registries of the installed plugins; a plugin whose registry
// did not answer keeps its place in the notice.
func (s *Server) checkPluginUpdates(ctx context.Context) {
	if !s.notifier.Enabled(notify.SourcePlugin) {
		return
	}
	mc, err := config.ReadMasterConfig(s.serverDir)
	if err != nil {
		return
	}
	if mc == nil || mc.Plugins == nil || len(mc.Plugins.Installed) == 0 {
		s.setPluginUpdates(nil)
		return
	}
	found, _ := availablePlugins(ctx, mc, s.serverDir)
	s.pluginNotices(found)
}

// pluginNotices turns the registries' answer into the notice.
func (s *Server) pluginNotices(found []availablePlugin) {
	checked := map[string]bool{}
	newest := map[string]pluginUpdate{}
	for _, a := range found {
		if !a.Installed {
			continue
		}
		checked[a.FullID] = true
		if !a.UpdateAvailable {
			continue
		}
		if old, ok := newest[a.FullID]; ok && !newerVersion(a.Meta.Version, old.Version) {
			continue
		}
		name := a.Meta.Name
		if name == "" {
			name = a.FullID
		}
		newest[a.FullID] = pluginUpdate{ID: a.FullID, Name: name, Version: a.Meta.Version}
	}
	list := make([]pluginUpdate, 0, len(newest))
	for _, u := range newest {
		list = append(list, u)
	}
	for _, u := range s.pendingPluginUpdates() {
		if !checked[u.ID] {
			list = append(list, u)
		}
	}
	list, _ = s.stillPending(list, nil)
	s.setPluginUpdates(list)
}

// PluginsReloaded takes the versions the plugin loader loaded (full id -> version), after a
// (re)load that may have updated files: plugins now running that version leave the notice.
func (s *Server) PluginsReloaded(loaded map[string]string) {
	s.resolvePluginNoticesWith(loaded)
}

// resolvePluginNotices drops from the notice the plugins no longer installed or already at
// that version; it reads only .vclu.json.
func (s *Server) resolvePluginNotices() { s.resolvePluginNoticesWith(nil) }

func (s *Server) resolvePluginNoticesWith(loaded map[string]string) {
	if s.notifier == nil {
		return
	}
	s.permissionNotice()
	pending := s.pendingPluginUpdates()
	if len(pending) == 0 {
		return
	}
	if list, ok := s.stillPending(pending, loaded); ok && len(list) < len(pending) {
		s.setPluginUpdates(list)
	}
}

// stillPending keeps the updates of installed plugins older than the update; false when
// .vclu.json is not read.
func (s *Server) stillPending(list []pluginUpdate, loaded map[string]string) ([]pluginUpdate, bool) {
	mc, err := config.ReadMasterConfig(s.serverDir)
	if err != nil {
		return list, false
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
	kept := list[:0:0]
	for _, u := range list {
		if cur, ok := installed[u.ID]; ok && newerVersion(u.Version, cur) {
			kept = append(kept, u)
		}
	}
	return kept, true
}

// pendingPluginUpdates are the plugins in the notice now.
func (s *Server) pendingPluginUpdates() []pluginUpdate {
	e, ok := s.notifier.State(pluginUpdatesKey)
	if !ok {
		return nil
	}
	var list []pluginUpdate
	_ = json.Unmarshal([]byte(e.Params["updates"]), &list)
	return list
}

// setPluginUpdates makes list the one notice of plugin updates, resolved when empty. It is
// unread again only when a plugin not in it before comes.
func (s *Server) setPluginUpdates(list []pluginUpdate) {
	if len(list) == 0 {
		s.notifier.Resolve(isPluginUpdates)
		return
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].ID < list[j].ID
	})
	before := map[string]bool{}
	for _, u := range s.pendingPluginUpdates() {
		before[u.ID] = true
	}
	renew := false
	var names, detail []string
	for i, u := range list {
		renew = renew || !before[u.ID]
		if i < 3 {
			names = append(names, u.Name)
		}
		detail = append(detail, u.Name+" "+u.Version)
	}
	shown := strings.Join(names, ", ")
	if len(list) > 3 {
		shown += " +" + strconv.Itoa(len(list)-3)
	}
	js, _ := json.Marshal(list)
	s.notifier.Add(notify.Entry{
		Kind: notify.KindState, Source: notify.SourcePlugin, Level: notify.LevelInfo, Key: pluginUpdatesKey,
		Msg: notify.MsgPluginUpdates, Link: "/plugins", Detail: strings.Join(detail, ", "), Renew: renew,
		Params: map[string]string{"count": strconv.Itoa(len(list)), "plugins": shown, "updates": string(js)},
	})
}

// notifyLock records a lock started for wait.
func (s *Server) notifyLock(msg, key string, params map[string]string, wait time.Duration) {
	if params == nil {
		params = map[string]string{}
	}
	if wait > 0 {
		params["until"] = time.Now().Add(wait).UTC().Truncate(time.Second).Format(time.RFC3339)
	}
	s.notifier.Add(notify.Entry{
		Kind: notify.KindEvent, Source: notify.SourceLock, Level: notify.LevelWarning,
		Key: "lock:" + key, Msg: msg, Params: params, Link: "/settings",
	})
}

// permissionNotice is a state while enabled plugins ask for permissions the user has not
// allowed (an update asked for more; it runs without them); it goes away once allowed.
func (s *Server) permissionNotice() {
	if !s.notifier.Enabled(notify.SourcePlugin) {
		return
	}
	mc, err := config.ReadMasterConfig(s.serverDir)
	if err != nil {
		return
	}
	var ids []string
	if mc != nil && mc.Plugins != nil {
		loader := plugins.NewLoader(s.serverDir)
		for _, p := range mc.Plugins.Installed {
			if p.Enabled && len(loader.PendingPermissions(p)) > 0 {
				ids = append(ids, p.ID)
			}
		}
	}
	if len(ids) == 0 {
		s.notifier.Resolve(func(e notify.Entry) bool { return e.Key == pluginPermissionsKey })
		return
	}
	sort.Strings(ids)
	shown := strings.Join(ids[:min(3, len(ids))], ", ")
	if len(ids) > 3 {
		shown += " +" + strconv.Itoa(len(ids)-3)
	}
	s.notifier.Add(notify.Entry{
		Kind: notify.KindState, Source: notify.SourcePlugin, Level: notify.LevelInfo, Key: pluginPermissionsKey,
		Msg: notify.MsgPluginPerms, Params: map[string]string{"count": strconv.Itoa(len(ids)), "plugins": shown}, Link: "/plugins",
	})
}
