package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Domktorymysli/vclu/pkg/camera"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/Domktorymysli/vclu/pkg/homekit"
	"github.com/Domktorymysli/vclu/pkg/logstream"
	"github.com/Domktorymysli/vclu/pkg/mcp"
	"github.com/Domktorymysli/vclu/pkg/metrics"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/Domktorymysli/vclu/pkg/server"
	"github.com/Domktorymysli/vclu/pkg/web"
)

var (
	version = "dev"
	commit  = "unknown"
)

// Compile-time guard: *server.Server is handed to the MCP layer as a LuaReloader,
// and reload/promote recover the module-load report through a type assertion to
// ModuleLoadReporter. That assertion fails silently — renaming or changing the
// signature of Server.LastModuleLoad would still compile and would quietly turn
// module-failure reporting back off. This line makes that a build error instead.
var (
	_ mcp.LuaReloader        = (*server.Server)(nil)
	_ mcp.ModuleLoadReporter = (*server.Server)(nil)
)

func main() {
	// Define flags
	var (
		listInterfaces = flag.Bool("list", false, "List available network interfaces and exit")
		iface          = flag.String("interface", "", "Network interface name (e.g., eth0, en0)")
		ip             = flag.String("ip", "", "IP address to bind to")
		dataDir        = flag.String("data-dir", "./data", "Directory for storing CLU data (runtime files, keys, etc.)")
		webPort        = flag.Int("web-port", 8080, "Web interface port")
		autoDetect     = flag.Bool("auto", false, "Auto-detect first available network interface")
		showVersion    = flag.Bool("version", false, "Show version information")
		verbose        = flag.Bool("verbose", false, "Enable verbose logging")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Virtual CLU - Grenton CLU Emulator\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s --list                          # List available interfaces\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --auto                          # Auto-detect interface\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --interface eth0                # Use eth0 interface\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --ip 192.168.1.100              # Bind to specific IP\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --interface eth0 --data-dir /mnt/clu  # Custom data directory\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nDocker example:\n")
		fmt.Fprintf(os.Stderr, "  docker run -v /path/to/data:/data vclu --auto\n")
	}

	flag.Parse()

	// Handle version flag
	if *showVersion {
		fmt.Printf("Virtual CLU version %s (commit: %s)\n", version, commit)
		os.Exit(0)
	}

	// Handle list flag
	if *listInterfaces {
		if err := listNetworkInterfaces(); err != nil {
			log.Fatalf("Failed to list network interfaces: %v", err)
		}
		os.Exit(0)
	}

	// Setup logging
	if !*verbose {
		log.SetFlags(log.Ldate | log.Ltime)
	} else {
		log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	}

	// Print ASCII banner
	printBanner()

	// Initialize LogStream to capture logs for debug console
	logStream := logstream.NewLogStream(os.Stdout, 1000)
	log.SetOutput(logStream)

	// Determine data directory
	serverDir := *dataDir
	if serverDir == "" {
		var err error
		serverDir, err = filepath.Abs("./clu")
		if err != nil {
			log.Fatalf("Failed to get server directory: %v", err)
		}
	}

	if err := os.MkdirAll(filepath.Join(serverDir, "clu"), 0755); err != nil {
		log.Fatalf("Failed to create server directories: %v", err)
	}

	// Get local IP and network interface
	var localIP net.IP
	var macAddr net.HardwareAddr
	var err error

	switch {
	case *autoDetect:
		log.Println("Auto-detecting network interface...")
		localIP, macAddr, err = autoDetectInterface()
		if err != nil {
			log.Fatalf("Failed to auto-detect network interface: %v", err)
		}
		log.Printf("Auto-detected interface: IP=%s, MAC=%s", localIP.String(), macAddr.String())

	case *ip != "":
		localIP, macAddr, err = getNetworkInterface(*ip)
		if err != nil {
			log.Fatalf("Failed to get interface for IP %s: %v", *ip, err)
		}

	case *iface != "":
		localIP, macAddr, err = getNetworkInterface(*iface)
		if err != nil {
			log.Fatalf("Failed to get interface %s: %v", *iface, err)
		}

	default:
		fmt.Fprintf(os.Stderr, "Error: You must specify --auto, --interface, or --ip\n\n")
		flag.Usage()
		os.Exit(1)
	}

	log.Printf("Starting Virtual CLU")
	log.Printf("  Data directory: %s", serverDir)
	log.Printf("  IP Address:     %s", localIP.String())
	log.Printf("  MAC Address:    %s", macAddr.String())
	log.Printf("  Web Port:       %d", *webPort)

	// Initialize metrics collector early — must be available before Lua modules load
	cluDir := filepath.Join(serverDir, "clu")
	metricsCollector := metrics.NewCollector(filepath.Join(cluDir, "metrics"))

	// Create CLU server
	dir := filepath.Join(serverDir, "clu")
	srv, err := server.NewServer(dir, localIP, macAddr)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	// Wire metrics collector to Lua engine BEFORE Start() —
	// otherwise modules calling metric() during init won't register in Go
	luaEngine := srv.GetLuaEngine()
	luaEngine.SetMetricsCollector(metricsCollector)

	// History store before Start(): user modules record changes while loading
	historyStore := history.New(historyConfig(cluDir), nil)
	historyStore.Start()
	luaEngine.SetHistoryStore(historyStore)

	// Initialize camera manager for snapshot proxy
	cameraManager := camera.NewManager()
	luaEngine.SetCameraManager(cameraManager)

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	// Wire up event-driven client push notifications (StateBus → client_reporter)
	luaEngine.SetClientPushHandler(srv.NotifyClientPush)

	// Get device config and keys from server
	deviceConfig, keys := srv.GetConfig()

	// Determine web directory (try multiple locations)
	webDir := findWebDir()

	// Create and start web server
	webSrv := web.NewServer(*webPort, webDir, cluDir, deviceConfig, keys, luaEngine)
	webSrv.SetCLUCommander(srv)                         // Wire up CLU commander for sending commands
	webSrv.SetBrokerController(srv)                     // Wire up MQTT broker controller
	webSrv.SetLuaReloader(srv)                          // Wire up Lua reloader for web editor
	webSrv.SetLogStream(logStream)                      // Wire up log stream for debug console
	webSrv.SetMetrics(srv.GetMetrics())                 // Wire up metrics for stats API
	webSrv.SetMQTTManager(luaEngine.GetMQTTManager())   // Wire up MQTT manager for client reconnect/test
	webSrv.SetSatelManager(luaEngine.GetSatelManager()) // Wire up Satel manager for status/scan/state (nil when disabled)

	// Initialize KV store manager for plugin persistence
	kvManager := plugins.NewKVStoreManager(cluDir)
	luaEngine.SetKVStoreManager(kvManager)

	// Start metrics collector and wire to web server
	webSrv.SetMetricsCollector(metricsCollector)
	webSrv.SetHistoryStore(historyStore)
	webSrv.SetCameraManager(cameraManager)
	metricsCollector.Start()

	// Initialize plugin loader (before callback registration so we can reload on reset)
	pluginLoader := plugins.NewLoader(cluDir)
	pluginLoader.SetLuaExecutor(luaEngine)
	webSrv.SetPluginController(pluginLoader) // Wire up plugin controller for hot loading

	// Set up plugins active provider for metrics
	if metrics := srv.GetMetrics(); metrics != nil {
		metrics.SetPluginsActiveFunc(func() int {
			return len(pluginLoader.GetLoadedPlugins())
		})
		// Set up HTTP request callback for plugin metrics
		luaEngine.SetHTTPRequestCallback(func(hasError bool) {
			metrics.RecordPluginHTTP(hasError)
		})
	}

	// Create the HomeKit server if enabled; it starts after the first Lua load,
	// when the exposed objects exist (see below)
	var homekitSrv *homekit.Server
	masterConfig, _ := config.ReadMasterConfig(cluDir)
	if masterConfig != nil && masterConfig.HomeKit != nil && masterConfig.HomeKit.Enabled {
		homekitSrv = homekit.NewServer(masterConfig.HomeKit, luaEngine, webSrv.GetAccessManager(), cluDir)
		// Wire up metrics for HomeKit
		homekitSrv.SetMetrics(srv.GetMetrics())
		// Wire up event-driven state notifications (StateBus → HomeKit)
		luaEngine.SetHomeKitStateHandler(homekitSrv.NotifyStateChange)
		// Set up HomeKit metrics provider functions
		if metrics := srv.GetMetrics(); metrics != nil {
			metrics.SetHomeKitFuncs(
				func() int { return len(homekitSrv.GetAccessories()) },
				func() int {
					status := homekitSrv.GetStatus()
					if status.PairingAvailable {
						return 0
					}
					return 1
				},
			)
		}
	}

	// Wire up HomeKit server to web interface
	webSrv.SetHomeKitServer(homekitSrv)

	// Start MCP server (always created, enabled flag gates requests at runtime)
	mcpDevices := mcp.NewDeviceProvider(luaEngine)
	mcpConfig := mcp.NewConfigProvider(cluDir)
	mcpMetrics := mcp.NewMetricsRecorder(srv.GetMetrics())
	mcpSrv := mcp.NewMCPServer(mcpDevices, webSrv.GetAccessManager(), mcpConfig, mcpMetrics)
	mcpSrv.SetFileProvider(mcp.NewFileProvider(cluDir))
	mcpSrv.SetLogProvider(logStream)
	mcpSrv.SetLuaExecutor(luaEngine)
	mcpSrv.SetMetricsCollector(metricsCollector)
	mcpSrv.SetHistoryStore(historyStore)

	mcpSrv.SetBroker(srv.GetMQTTBroker())
	mcpSrv.SetMQTTCredentialProvider(luaEngine.GetMQTTManager())

	// Initialize workspace and sandbox for MCP Layer 2 draft pipeline
	mcpSrv.InitWorkspace(cluDir, luaEngine.GetRuntimePath(), srv)
	// srv is passed as an mcp.LuaReloader; reload/promote additionally type-assert
	// it to mcp.ModuleLoadReporter to report modules that failed to load. That
	// assertion is soft — the compile-time guard at the top of this file fails the
	// build if the method is renamed, instead of silently disabling the reporting.

	if masterConfig != nil && masterConfig.MCP != nil && masterConfig.MCP.Enabled {
		mcpSrv.SetEnabled(true)
	}
	webSrv.SetMCPServer(mcpSrv)

	// Register pre-reset callback to block HomeKit callbacks BEFORE Lua reload
	srv.SetOnLuaPreReset(func() {
		if homekitSrv != nil {
			homekitSrv.SetInitializing(true)
		}
	})

	// Register pre-load callback: access sync + plugins (runs BEFORE user.lua on every load/reload)
	accessSyncFunc := webSrv.GetAccessSyncFunc()
	syncConfigFunc := webSrv.GetSyncConfigSyncFunc()
	srv.SetOnPreLuaLoad(func() {
		if accessSyncFunc != nil {
			accessSyncFunc()
		}
		if syncConfigFunc != nil {
			syncConfigFunc()
		}
		if err := pluginLoader.LoadAllEnabled(); err != nil {
			log.Printf("Warning: Some plugins failed to load: %v", err)
		}
	})

	// Register post-load callback: auto-expose + HomeKit (runs AFTER user.lua + plugins)
	srv.SetOnLuaReset(func() {
		// Auto-expose remaining registry objects (respects AccessControl + manual expose)
		if _, err := luaEngine.Execute("exposeRegistry()"); err != nil {
			log.Printf("   ⚠️  Failed to auto-expose registry: %v", err)
		}
		// Rebuild the bridge if the exposed objects changed, then let Home app
		// writes through again once the state has settled
		if homekitSrv != nil {
			gen := homekitSrv.InitGen() // this reload's pause, taken before Sync can wait
			go func() {
				if err := homekitSrv.Sync(); err != nil {
					log.Printf("[HomeKit] bridge not updated after Lua reload: %v", err)
				}
				homekitSrv.ResumeGen(gen, 3*time.Second)
			}()
		}
	})

	// Load CLU data (plugins → user.lua → om.lua → MQTT → expose)
	srv.LoadInitialData()

	// HomeKit after the first load: started earlier it published an empty bridge
	if homekitSrv != nil {
		if err := homekitSrv.Start(); err != nil {
			log.Printf("Warning: Failed to start HomeKit server: %v", err)
		} else {
			log.Printf("HomeKit bridge started on port %d", masterConfig.HomeKit.Port)
		}
	}

	if err := webSrv.Start(); err != nil {
		log.Fatalf("Failed to start web server: %v", err)
	}

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Virtual CLU is running. Press Ctrl+C to stop.")
	<-sigChan

	log.Println("Shutting down...")
	go historyStore.Flush() // early write in case a slow stop below runs into the systemd kill; Close bounds the wait
	metricsCollector.Stop() // final flush before exit
	if homekitSrv != nil {
		if err := homekitSrv.Stop(); err != nil {
			log.Printf("Error stopping HomeKit server: %v", err)
		}
	}
	if err := webSrv.Stop(); err != nil {
		log.Printf("Error stopping web server: %v", err)
	}
	if err := srv.Stop(); err != nil {
		log.Printf("Error stopping server: %v", err)
	}
	// after every producer (Lua, web, MCP, HomeKit) has stopped
	if err := historyStore.Close(10 * time.Second); err != nil {
		log.Printf("Error writing history: %v", err)
	}
}

// fileExists checks if a file or directory exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// findWebDir finds the web directory by checking multiple locations
func findWebDir() string {
	// Get executable path
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		// Try web directory next to binary (e.g., /opt/vclu/web)
		webDir := filepath.Join(execDir, "web")
		if fileExists(filepath.Join(webDir, "templates")) {
			log.Printf("Found web directory at: %s", webDir)
			return webDir
		}
	}

	// Try relative to working directory
	if fileExists(filepath.Join("./web", "templates")) {
		log.Printf("Found web directory at: ./web")
		return "./web"
	}

	// Try relative to project root (development)
	if fileExists(filepath.Join("../../web", "templates")) {
		log.Printf("Found web directory at: ../../web")
		return "../../web"
	}

	// Fallback - will fail later if web dir is needed
	log.Printf("Warning: Web directory not found, web interface may not work")
	return "./web"
}

// getNetworkInterface gets the local IP and MAC address from interface name or IP
func getNetworkInterface(nameOrIP string) (net.IP, net.HardwareAddr, error) {
	// Try to parse as IP address first
	ip := net.ParseIP(nameOrIP)
	if ip != nil {
		// Find interface with this IP
		ifaces, err := net.Interfaces()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to list interfaces: %w", err)
		}

		for _, iface := range ifaces {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}

			for _, addr := range addrs {
				var ifaceIP net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ifaceIP = v.IP
				case *net.IPAddr:
					ifaceIP = v.IP
				}

				if ifaceIP != nil && ifaceIP.Equal(ip) {
					return ip, iface.HardwareAddr, nil
				}
			}
		}

		return nil, nil, fmt.Errorf("no interface found with IP: %s", nameOrIP)
	}

	// Try as interface name
	iface, err := net.InterfaceByName(nameOrIP)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get interface %s: %w", nameOrIP, err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get addresses for interface %s: %w", nameOrIP, err)
	}

	// Find first IPv4 address
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}

		// Check if it's IPv4
		if ip != nil && ip.To4() != nil {
			return ip, iface.HardwareAddr, nil
		}
	}

	return nil, nil, fmt.Errorf("no IPv4 address found for interface %s", nameOrIP)
}

// autoDetectInterface automatically detects the first available network interface
func autoDetectInterface() (net.IP, net.HardwareAddr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list interfaces: %w", err)
	}

	for _, iface := range ifaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		// Find first IPv4 address
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			// Return first IPv4 address found
			if ip != nil && ip.To4() != nil {
				return ip, iface.HardwareAddr, nil
			}
		}
	}

	return nil, nil, fmt.Errorf("no suitable network interface found")
}

// listNetworkInterfaces lists all available network interfaces with IPv4 addresses
func listNetworkInterfaces() error {
	ifaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("failed to list interfaces: %w", err)
	}

	fmt.Println("Available network interfaces:")
	fmt.Println("========================================")

	foundAny := false
	for _, iface := range ifaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		// Find IPv4 addresses
		var ipv4Addrs []string
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			// Only show IPv4 addresses
			if ip != nil && ip.To4() != nil {
				ipv4Addrs = append(ipv4Addrs, ip.String())
			}
		}

		// Only show interfaces with IPv4 addresses
		if len(ipv4Addrs) > 0 {
			foundAny = true
			fmt.Printf("\nInterface: %s\n", iface.Name)
			fmt.Printf("  MAC:     %s\n", iface.HardwareAddr.String())
			for _, ip := range ipv4Addrs {
				fmt.Printf("  IPv4:    %s\n", ip)
			}
		}
	}

	if !foundAny {
		fmt.Println("\nNo suitable network interfaces found (need UP interface with IPv4)")
	}

	fmt.Println("\n========================================")
	fmt.Printf("Usage: %s <interface-name-or-ip>\n", os.Args[0])

	return nil
}

// printBanner prints the ASCII art banner
func printBanner() {
	banner := `
    ██╗   ██╗ ██████╗██╗     ██╗   ██╗
    ██║   ██║██╔════╝██║     ██║   ██║
    ██║   ██║██║     ██║     ██║   ██║
    ╚██╗ ██╔╝██║     ██║     ██║   ██║
     ╚████╔╝ ╚██████╗███████╗╚██████╔╝
      ╚═══╝   ╚═════╝╚══════╝ ╚═════╝  v%s
    ───────────────────────────────────
    vCLU by Tytus
    ───────────────────────────────────
`
	fmt.Printf(banner, version)
}

// historyConfig falls back to defaults when .vclu.json cannot be read.
func historyConfig(cluDir string) history.Config {
	cfg, err := history.LoadConfig(cluDir)
	if err != nil {
		log.Printf("History settings: %v (using defaults)", err)
	}
	return cfg
}
