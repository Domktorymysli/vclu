package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// DiagnosticOptions controls what is included in the diagnostic ZIP
type DiagnosticOptions struct {
	IncludeModules     bool `json:"include_modules"`
	IncludeOmUser      bool `json:"include_om_user"`
	IncludeProxy       bool `json:"include_proxy"`
	IncludeDeviceNames bool `json:"include_device_names"`
	IncludeConfig      bool `json:"include_config"`
}

// handleAPIDiagnosticCreate creates a diagnostic ZIP and streams it to the client
//
//	@Summary		Create diagnostic archive
//	@Description	Builds a diagnostic ZIP (info.json, stats.json, metrics.json, logs.txt, devices.json always; Lua modules, om.lua/user.lua, proxy imports and redacted config optionally per the posted options) and streams it as an attachment named vclu-diag-<timestamp>.zip.
//	@Tags			stats
//	@Accept			json
//	@Produce		octet-stream
//	@Param			options	body		web.DiagnosticOptions	true	"Flags selecting optional ZIP contents"
//	@Success		200		{file}		file					"ZIP archive (application/zip)"
//	@Failure		400		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/diagnostic [post]
func (s *Server) handleAPIDiagnosticCreate(c *gin.Context) {
	var opts DiagnosticOptions
	if err := c.ShouldBindJSON(&opts); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid options"})
		return
	}

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// 1. info.json — always
	s.diagWriteInfo(zw)

	// 2. stats.json — always
	s.diagWriteStats(zw)

	// 3. metrics.json — always
	s.diagWriteMetrics(zw)

	// 4. logs.txt — always
	s.diagWriteLogs(zw)

	// 5. devices.json — always (redacted unless include_device_names)
	s.diagWriteDevices(zw, opts.IncludeDeviceNames)

	// 6. modules/*.lua — optional
	if opts.IncludeModules {
		s.diagWriteDir(zw, filepath.Join(s.serverDir, "modules"), "modules", ".lua")
	}

	// 7. om.lua, user.lua — optional
	if opts.IncludeOmUser {
		s.diagWriteFile(zw, filepath.Join(s.serverDir, "a", "om.lua"), "om.lua")
		s.diagWriteFile(zw, filepath.Join(s.serverDir, "a", "user.lua"), "user.lua")
	}

	// 8. imports/proxy_*.lua — optional
	if opts.IncludeProxy {
		s.diagWriteDirFiltered(zw, filepath.Join(s.serverDir, "imports"), "imports", "proxy_", ".lua")
	}

	// 9. config.json — optional, redacted
	if opts.IncludeConfig {
		s.diagWriteConfig(zw)
	}

	zw.Close()

	filename := fmt.Sprintf("vclu-diag-%s.zip", time.Now().Format("2006-01-02_15-04"))
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Data(http.StatusOK, "application/zip", buf.Bytes())
}

// diagWriteJSON is a helper that marshals v to JSON and writes it to the zip.
func diagWriteJSON(zw *zip.Writer, name string, v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return
	}
	w, err := zw.Create(name)
	if err != nil {
		return
	}
	w.Write(data)
}

// diagWriteInfo writes version/system info
func (s *Server) diagWriteInfo(zw *zip.Writer) {
	info := map[string]interface{}{
		"timestamp": time.Now().Format(time.RFC3339),
		"os":        runtime.GOOS,
		"arch":      runtime.GOARCH,
		"go":        runtime.Version(),
		"cpus":      runtime.NumCPU(),
	}
	if s.metrics != nil {
		snap := s.metrics.Snapshot()
		info["uptime_seconds"] = snap.Uptime.Seconds()
	}
	diagWriteJSON(zw, "info.json", info)
}

// diagWriteStats writes the full AppMetricsSnapshot
func (s *Server) diagWriteStats(zw *zip.Writer) {
	if s.metrics == nil {
		diagWriteJSON(zw, "stats.json", map[string]string{"error": "metrics not available"})
		return
	}
	snapshot := s.metrics.Snapshot()

	// Enrich with broker stats (same as handleAPIStats)
	if s.brokerController != nil {
		broker := s.brokerController.GetMQTTBroker()
		if broker != nil {
			bs := broker.Stats()
			snapshot.MQTT.Connected = bs.Running
			snapshot.MQTT.Published = bs.MessagesSent
			snapshot.MQTT.Received = bs.MessagesReceived
		}
	}
	if s.metricsCollector != nil {
		for _, m := range s.metricsCollector.List() {
			switch m.Path {
			case "runtime:busy_percent":
				snapshot.Lua.BusyPercent = m.Latest
			case "runtime:loop_lag_ms":
				snapshot.Lua.LoopLagMs = m.Latest
			}
		}
	}

	diagWriteJSON(zw, "stats.json", snapshot)
}

// diagWriteMetrics writes the metrics collector list
func (s *Server) diagWriteMetrics(zw *zip.Writer) {
	if s.metricsCollector == nil {
		diagWriteJSON(zw, "metrics.json", []interface{}{})
		return
	}
	diagWriteJSON(zw, "metrics.json", s.metricsCollector.List())
}

// diagWriteLogs writes formatted log entries
func (s *Server) diagWriteLogs(zw *zip.Writer) {
	w, err := zw.Create("logs.txt")
	if err != nil {
		return
	}
	if s.logStream == nil {
		w.Write([]byte("Log stream not available\n"))
		return
	}
	entries := s.logStream.GetHistory()
	for _, e := range entries {
		line := fmt.Sprintf("[%s] [%s] [%s] %s\n",
			e.Time.Format("2006-01-02 15:04:05"),
			e.Level,
			e.Source,
			e.Message,
		)
		w.Write([]byte(line))
	}
}

// diagWriteDevices writes device list from Lua ExposedObjects
func (s *Server) diagWriteDevices(zw *zip.Writer, includeNames bool) {
	if s.luaExecutor == nil {
		diagWriteJSON(zw, "devices.json", map[string]string{"error": "lua not available"})
		return
	}

	result, err := s.luaExecutor.Execute(`
		local list = {}
		if ExposedObjects and ExposedObjects.list then
			for _, obj in ipairs(ExposedObjects:list()) do
				table.insert(list, {
					path = obj.path or "",
					type = obj.type or "",
					name = obj.name or "",
					area = obj.area or "",
					description = obj.description or ""
				})
			end
		end
		return require("json").encode(list)
	`)
	if err != nil {
		diagWriteJSON(zw, "devices.json", map[string]string{"error": err.Error()})
		return
	}

	if !includeNames {
		// Parse and strip name/area/description
		var devices []map[string]interface{}
		if json.Unmarshal([]byte(result), &devices) == nil {
			for _, d := range devices {
				delete(d, "name")
				delete(d, "area")
				delete(d, "description")
			}
			diagWriteJSON(zw, "devices.json", devices)
			return
		}
	}

	// Write raw result
	w, err := zw.Create("devices.json")
	if err != nil {
		return
	}
	w.Write([]byte(result))
}

// diagWriteFile writes a single file to the ZIP
func (s *Server) diagWriteFile(zw *zip.Writer, srcPath string, zipName string) {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return // skip missing files silently
	}
	w, err := zw.Create(zipName)
	if err != nil {
		return
	}
	w.Write(data)
}

// diagWriteDir writes all files with given extension from a directory
func (s *Server) diagWriteDir(zw *zip.Writer, dir string, zipDir string, ext string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		s.diagWriteFile(zw, filepath.Join(dir, e.Name()), filepath.Join(zipDir, e.Name()))
	}
}

// diagWriteDirFiltered writes files matching prefix and extension
func (s *Server) diagWriteDirFiltered(zw *zip.Writer, dir string, zipDir string, prefix string, ext string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		s.diagWriteFile(zw, filepath.Join(dir, e.Name()), filepath.Join(zipDir, e.Name()))
	}
}

// diagWriteConfig writes redacted .vclu.json
func (s *Server) diagWriteConfig(zw *zip.Writer) {
	configPath := filepath.Join(s.serverDir, ".vclu.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}

	redacted := redactConfig(data)
	w, err := zw.Create("config.json")
	if err != nil {
		return
	}
	w.Write(redacted)
}

// redactConfig removes secrets from .vclu.json content
func redactConfig(raw []byte) []byte {
	var cfg map[string]interface{}
	if json.Unmarshal(raw, &cfg) != nil {
		return []byte("{}")
	}

	// Redact auth secrets
	if auth, ok := cfg["auth"].(map[string]interface{}); ok {
		delete(auth, "passwordHash")
		delete(auth, "apiKeyHash")
	}

	// Redact all encryption keys
	delete(cfg, "keys")

	// Redact pairing secrets
	delete(cfg, "projectKey")
	delete(cfg, "projectIv")

	// Redact MQTT broker password
	if mqtt, ok := cfg["mqttBroker"].(map[string]interface{}); ok {
		delete(mqtt, "password")
	}

	// Redact HomeKit PIN
	if hk, ok := cfg["homeKit"].(map[string]interface{}); ok {
		delete(hk, "pin")
	}

	// Redact MCP bot API key hashes
	if mcp, ok := cfg["mcp"].(map[string]interface{}); ok {
		if bots, ok := mcp["bots"].([]interface{}); ok {
			for _, b := range bots {
				if bot, ok := b.(map[string]interface{}); ok {
					delete(bot, "apiKeyHash")
				}
			}
		}
	}

	// Redact metrics password
	if met, ok := cfg["metrics"].(map[string]interface{}); ok {
		delete(met, "password")
	}

	// Redact plugin configs that may contain secrets
	if plugins, ok := cfg["plugins"].(map[string]interface{}); ok {
		if installed, ok := plugins["installed"].([]interface{}); ok {
			for _, p := range installed {
				if plugin, ok := p.(map[string]interface{}); ok {
					if pluginCfg, ok := plugin["config"].(map[string]interface{}); ok {
						for k := range pluginCfg {
							kLower := strings.ToLower(k)
							if strings.Contains(kLower, "password") ||
								strings.Contains(kLower, "secret") ||
								strings.Contains(kLower, "token") ||
								strings.Contains(kLower, "apikey") ||
								strings.Contains(kLower, "api_key") {
								pluginCfg[k] = "[REDACTED]"
							}
						}
					}
				}
			}
		}
	}

	result, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return result
}
