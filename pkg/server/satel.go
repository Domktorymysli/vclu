package server

import (
	"log"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/satel"
)

// parseSatelRange parses a range spec, falling back to the default on error.
func parseSatelRange(what, spec, fallback string) []int {
	if spec == "" {
		spec = fallback
	}
	numbers, err := satel.ParseRange(spec)
	if err != nil {
		log.Printf("WARNING: Satel %s range %q invalid (%v), using %q", what, spec, err, fallback)
		numbers, _ = satel.ParseRange(fallback)
	}
	return numbers
}

// setupSatel starts the Satel Integra integration when enabled in config.
// All panel I/O runs in its own goroutine; the Lua engine only sees the
// cached state and the event queue (never blocks the RunLoop).
func setupSatel(serverDir string, luaEngine *lua.LuaEngine) {
	cfg := config.GetSatelConfig(serverDir)
	if cfg == nil || !cfg.Enabled {
		return
	}
	if cfg.Host == "" {
		log.Printf("WARNING: Satel integration enabled but host is empty — skipping")
		return
	}

	defaults := config.DefaultSatelConfig()
	zones := parseSatelRange("zones", cfg.Zones, defaults.Zones)
	partitions := parseSatelRange("partitions", cfg.Partitions, defaults.Partitions)
	outputs := parseSatelRange("outputs", cfg.Outputs, defaults.Outputs)
	tempZones := parseSatelRange("tempZones", cfg.TempZones, "")

	pollInterval := time.Duration(cfg.PollInterval) * time.Millisecond
	if cfg.PollInterval <= 0 {
		pollInterval = time.Duration(defaults.PollInterval) * time.Millisecond
	}
	tempPoll := time.Duration(cfg.TempPoll) * time.Millisecond
	if cfg.TempPoll <= 0 {
		tempPoll = time.Duration(defaults.TempPoll) * time.Millisecond
	}
	readNames := cfg.ReadNames == nil || *cfg.ReadNames

	panel := satel.New(satel.Config{
		Host:          cfg.Host,
		Port:          cfg.Port,
		UserCode:      cfg.UserCode,
		EncryptionKey: cfg.EncryptionKey,
		PollInterval:  pollInterval,
		TempPoll:      tempPoll,
		Zones:         zones,
		Partitions:    partitions,
		Outputs:       outputs,
		TempZones:     tempZones,
		ReadNames:     readNames,
		AllowDisarm:   cfg.AllowDisarm,
		AllowForceArm: cfg.AllowForceArm,
	})

	manager := lua.NewSatelManager(panel, lua.SatelOptions{
		Zones:         zones,
		Partitions:    partitions,
		Outputs:       outputs,
		TempZones:     tempZones,
		AllowDisarm:   cfg.AllowDisarm,
		AllowForceArm: cfg.AllowForceArm,
		ArmMode:       cfg.ArmMode,
	})
	luaEngine.SetSatelManager(manager)
	manager.Start()

	log.Printf("Satel integration enabled: %s:%d (%d zones, %d partitions, %d outputs)",
		cfg.Host, panelPort(cfg.Port), len(zones), len(partitions), len(outputs))
}

// panelPort resolves the effective port for logging.
func panelPort(port int) int {
	if port == 0 {
		return 7094
	}
	return port
}
