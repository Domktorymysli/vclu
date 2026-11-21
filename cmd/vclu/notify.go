package main

import (
	"log"
	"path/filepath"
	"time"

	"github.com/Domktorymysli/vclu/pkg/logstream"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/notify"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/Domktorymysli/vclu/pkg/web"
)

// notifications is the panel's bell with its sources, passed on to Notifications.on handlers.
type notifications struct {
	store   *notify.Store
	toLua   *notify.LuaBridge
	stopLog func()
}

func startNotifications(cluDir string, engine *lua.LuaEngine, logs *logstream.LogStream, webSrv *web.Server) *notifications {
	n := &notifications{store: notify.New(notify.Options{Path: notify.Path(cluDir), ConfigDir: cluDir})}
	n.toLua = notify.NewLuaBridge(func(js string) error {
		_, err := engine.Call("Notifications._deliver", js)
		return err
	})
	n.store.Subscribe(n.toLua.Deliver)
	n.stopLog = logs.AddHook(notify.LogHook(n.store))
	engine.SetModuleLoadHandler(lua.ModuleNotices(n.store, filepath.Join(cluDir, "modules")))
	webSrv.SetNotifier(n.store)
	return n
}

// deviceLocked is CloudBridge.PINLocked.
func (n *notifications) deviceLocked(path string, wait time.Duration) {
	notify.DeviceLocked(n.store, path, wait)
}

// pluginsLoaded resolves the update notices of plugins that are now at a newer version.
func pluginsLoaded(webSrv *web.Server, loader *plugins.Loader) {
	loaded := map[string]string{}
	for id, p := range loader.GetLoadedPlugins() {
		loaded[id] = p.Manifest.Version
	}
	webSrv.PluginsReloaded(loaded)
}

// Close stops the sources and writes what is left.
func (n *notifications) Close() {
	n.stopLog()
	n.toLua.Close()
	if err := n.store.Close(); err != nil {
		log.Printf("Error writing notifications: %v", err)
	}
}
