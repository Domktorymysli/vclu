package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"

	"github.com/Domktorymysli/vclu/pkg/cloud"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/homekit"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/web"
)

// deviceName is how this vCLU is shown in the cloud when pairing.
func deviceName(cfg *config.VCLUConfig) string {
	if cfg != nil && cfg.Device != nil && cfg.Device.Name != "" {
		return cfg.Device.Name
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return "vCLU " + h
	}
	return "vCLU"
}

// cloudHealth reports the house's own state to the cloud (protocol "health"),
// which can then alert on what the house cannot report once cut off.
func cloudHealth(started time.Time, mqtt *lua.MQTTManager, satel *lua.SatelManager, hk *homekit.Server) func() proto.Health {
	return func() proto.Health {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		integrations := map[string]string{"mqtt": "off", "satel": "off", "homekit": "off"}
		if mqtt != nil {
			if st := mqtt.Stats(); st.TotalClients > 0 {
				integrations["mqtt"] = okOr(st.ConnectedClients == st.TotalClients)
			}
		}
		if satel != nil {
			integrations["satel"] = okOr(satel.Panel().Connected())
		}
		if hk != nil {
			integrations["homekit"] = okOr(hk.GetStatus().Running)
		}
		return proto.Health{
			UptimeS:      int64(time.Since(started) / time.Second),
			Integrations: integrations,
			MemMB:        int(mem.Sys >> 20),
		}
	}
}

func okOr(ok bool) string {
	if ok {
		return "ok"
	}
	return "error"
}

// cloudBridge gives the Cloud module in Lua the cloud client: notifications out, the last
// presence list in, the on/off switch.
func cloudBridge(c *cloud.Client) lua.CloudBridge {
	return lua.CloudBridge{
		Notify: func(raw string) error {
			var n proto.Notify
			if err := json.Unmarshal([]byte(raw), &n); err != nil {
				return err
			}
			return c.Notify(n)
		},
		Presence: func() string {
			p, ok := c.Presence()
			if !ok {
				return ""
			}
			b, _ := json.Marshal(p.People)
			return string(b)
		},
		SetEnabled:     c.SetEnabledLater,
		Enabled:        c.Enabled,
		ObjectsChanged: c.ObjectsChanged,
	}
}

// cloudObjects lists the devices exposed with :remote() in Lua, for the cloud's snapshot.
func cloudObjects(engine *lua.LuaEngine) func() ([]proto.Object, error) {
	return func() ([]proto.Object, error) {
		out, err := engine.Execute("JSON.encode(ExposedObjects:remoteList())")
		if err != nil {
			return nil, err
		}
		var list []proto.Object
		if err := json.Unmarshal([]byte(out), &list); err != nil {
			return nil, fmt.Errorf("remoteList: %w", err)
		}
		return list, nil
	}
}

// cloudCommand runs a command from the app in Lua (Cloud._command) as the person: history records
// origin "remote", their cloud id and the command's id. Lua checks :remote(), the roles, the PIN
// and the deadline again, since the Lua queue may hold the call.
func cloudCommand(engine *lua.LuaEngine) func(proto.Cmd, time.Time) proto.CmdResult {
	return func(cmd proto.Cmd, deadline time.Time) proto.CmdResult {
		roles := cmd.Actor.Roles
		if roles == nil {
			roles = []string{}
		}
		rolesJSON, _ := json.Marshal(roles)
		// no level is -1, which Lua only reads with set (and refuses there)
		value := -1.0
		if cmd.Value != nil {
			value = *cmd.Value
		}
		out, err := engine.CallAs("remote", cmd.Actor.User, cmd.ID, "Cloud._command",
			cmd.Path, cmd.Action, string(rolesJSON), cmd.Confirmed, cmd.Pin, float64(deadline.UnixMilli()), value)
		var res proto.CmdResult
		if err == nil {
			err = json.Unmarshal([]byte(out), &res)
		}
		if err != nil {
			log.Printf("[Cloud] command %s on %q: %v", cmd.Action, cmd.Path, err)
			return proto.CmdResult{Reason: proto.ReasonError}
		}
		return res
	}
}

// cloudStates reads what remote devices are doing for a person (Cloud._states), for the cloud's
// states: a device Lua leaves out, or a list it cannot give, is not in the answer.
func cloudStates(engine *lua.LuaEngine) func(paths, roles []string) []proto.State {
	return func(paths, roles []string) []proto.State {
		pathsJSON, _ := json.Marshal(paths)
		rolesJSON, _ := json.Marshal(roles)
		out, err := engine.Call("Cloud._states", string(pathsJSON), string(rolesJSON))
		var list []proto.State
		if err == nil {
			err = json.Unmarshal([]byte(out), &list)
		}
		if err != nil {
			log.Printf("[Cloud] states: %v", err)
			return nil
		}
		return list
	}
}

// cloudDashboards lists the dashboards marked :remote() in Lua, for the cloud's tiles.
func cloudDashboards(engine *lua.LuaEngine) func() []proto.DashboardInfo {
	return func() []proto.DashboardInfo {
		out, err := engine.Execute("Dashboard.API.remoteList()")
		var list []proto.DashboardInfo
		if err == nil {
			err = json.Unmarshal([]byte(out), &list)
		}
		if err != nil {
			log.Printf("[Cloud] remote dashboards: %v", err)
			return nil
		}
		return list
	}
}

// cloudPresence hands each presence list to Cloud._presence, which tells the scripts who came
// and who left.
func cloudPresence(engine *lua.LuaEngine) func(proto.Presence) {
	return func(p proto.Presence) {
		b, _ := json.Marshal(p.People)
		if _, err := engine.Call("Cloud._presence", string(b)); err != nil {
			log.Printf("[Cloud] presence: %v", err)
		}
	}
}

// cloudNotifyAction runs a pressed notification button in Lua (Cloud._action) as the person
// who pressed it: history records origin "remote" and their cloud id.
func cloudNotifyAction(engine *lua.LuaEngine) func(proto.NotifyAction) proto.NotifyActionResult {
	return func(a proto.NotifyAction) proto.NotifyActionResult {
		out, err := engine.CallAs("remote", a.Actor.User, a.ID, "Cloud._action", a.Notify, a.Action, a.Actor.User, a.Actor.Name, a.Actor.Role)
		var res proto.NotifyActionResult
		if err == nil {
			err = json.Unmarshal([]byte(out), &res)
		}
		if err != nil {
			log.Printf("[Cloud] notification button %s/%s: %v", a.Notify, a.Action, err)
			return proto.NotifyActionResult{Reason: proto.ReasonError}
		}
		return res
	}
}

// startLocalAccess turns on local tokens for the app (pkg/web/local.go) and starts the LAN
// listener for them: at once when vCLU is paired or has clients of the device API, otherwise
// with the first token or client. nil when cloud.localPort is -1 or the certificate cannot be made.
func startLocalAccess(webSrv *web.Server, dir string, cfg *config.VCLUConfig) *web.LocalAccess {
	var cc *config.VCLUCloudConfig
	if cfg != nil {
		cc = cfg.Cloud
	}
	port := cc.LocalListenPort()
	if port == 0 {
		return nil
	}
	la, tlsCfg, err := web.NewLocalAccess(dir, port)
	if err != nil {
		log.Printf("[Cloud] no app access on the LAN: %v", err)
		return nil
	}
	var once sync.Once
	listen := func() {
		once.Do(func() {
			srv := &http.Server{
				Addr: ":" + strconv.Itoa(port), Handler: webSrv.LocalHandler(), TLSConfig: tlsCfg,
				ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
			}
			go func() {
				log.Printf("[Cloud] app access on the LAN: %s", la)
				if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
					log.Printf("[Cloud] LAN listener for the app: %v", err)
				}
			}()
		})
	}
	la.OnIssue = listen
	webSrv.SetLocalAccess(la)
	if cc.IsPaired() || (cfg != nil && len(cfg.Clients) > 0) {
		listen() // the device API's clients need no pairing
	}
	return la
}

// cloudMembers hands the cloud's members lists to the local tokens; nil, offering none, when
// they are off.
// cloudSeen tells local access the cloud is there each time a health report goes out on a working
// connection: local tokens stop after a week without it.
func cloudSeen(la *web.LocalAccess, health func() proto.Health) func() proto.Health {
	if la == nil {
		return health
	}
	return func() proto.Health {
		la.CloudSeen()
		return health()
	}
}

func cloudMembers(la *web.LocalAccess) func(proto.Members) {
	if la == nil {
		return nil
	}
	return la.SetMembers
}
