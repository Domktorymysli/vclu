package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CompletionItem represents a single autocompletion suggestion
type CompletionItem struct {
	Label           string `json:"label"`
	Kind            string `json:"kind"`
	Detail          string `json:"detail,omitempty"`
	Documentation   string `json:"documentation,omitempty"`
	InsertText      string `json:"insertText,omitempty"`
	InsertTextRules int    `json:"insertTextRules,omitempty"` // 4 = InsertAsSnippet
}

// Completions is the root structure for lua-completions.json
type Completions struct {
	Globals  []CompletionItem            `json:"globals"`
	Methods  map[string][]CompletionItem `json:"methods"`
	Keywords []string                    `json:"keywords"`
}

// ParsedFunction represents a parsed Lua function with LDoc
type ParsedFunction struct {
	Name          string
	ClassName     string
	Description   string
	Params        []ParamInfo
	Returns       string
	IsConstructor bool
}

// ParamInfo represents a function parameter
type ParamInfo struct {
	Name     string
	Type     string
	Optional bool
	Desc     string
}

var (
	// Regex patterns
	modulePattern   = regexp.MustCompile(`@module\s+(\S+)`)
	funcPattern     = regexp.MustCompile(`function\s+(\w+)([:.])(\w+)\s*\(([^)]*)\)`)
	paramPattern    = regexp.MustCompile(`@param\s+(\w+)\s+(\S+)\s*(.*)`)
	returnPattern   = regexp.MustCompile(`@return\s+(\S+)\s*(.*)`)
	descPattern     = regexp.MustCompile(`^---\s*(.+)`)
	continueDesc    = regexp.MustCompile(`^--\s+([^@].*)`)
	inheritsPattern = regexp.MustCompile(`@inherits\s+(\w+)`)
	constantPattern = regexp.MustCompile(`^(\w+)\.([A-Z][A-Z0-9_]+)\s*=\s*(-?\d+)\s*(?:--\s*(.*))?`)

	// Internal modules that should not appear in user-facing completions
	skipModules = map[string]bool{
		"PluginSandbox":      true,
		"LuaAPI":             true,
		"RemoteSubscription": true,
		"SYSTEM":             true,
		"StateBus":           true,
		"EventBus":           true,
		"PluginAPI":          true,
		"objects":            true,
		"AccessControl":      true,
		"EventBusRecorder":   true,
		"HACommands":         true,
		"HAIntegration":      true,
		"HAStatePublisher":   true,
		"MqttHandler":        true,
		"MqttAccess":         true,
		"GATE":               true,
		"OBJECT":             true,
		"MODULE":             true,
		"CLU_REMOTE":         true,
		"ModuleData":         true,
		"TIMERS":             true,
		"UNDEFINED":          true,
		"PluginHelpers":      true,
		"TypeMap":            true,
		"GenericButton":      true,
		"GPIO":               true,
		"Logger":             true,
		"poller":             true,
	}
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: generate-completions <runtime-dir> <output-file>")
		os.Exit(1)
	}

	runtimeDir := os.Args[1]
	outputFile := os.Args[2]

	completions := Completions{
		Globals: []CompletionItem{},
		Methods: make(map[string][]CompletionItem),
		Keywords: []string{
			"local", "function", "if", "then", "else", "elseif", "end",
			"for", "while", "do", "repeat", "until", "return", "break",
			"and", "or", "not", "nil", "true", "false", "require",
			"in", "goto", "self",
		},
	}

	// Track modules for globals
	modules := make(map[string]string)

	err := filepath.Walk(runtimeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".lua") {
			return err
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		parseFile(string(content), &completions, modules)
		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking directory: %v\n", err)
		os.Exit(1)
	}

	// Add modules as globals (skip internal modules)
	for name, desc := range modules {
		if skipModules[name] {
			continue
		}
		completions.Globals = append(completions.Globals, CompletionItem{
			Label:         name,
			Kind:          "module",
			Detail:        desc,
			Documentation: desc,
		})
	}

	// Remove skipped modules from methods map
	for name := range skipModules {
		delete(completions.Methods, name)
	}

	// Add built-in globals
	addBuiltinGlobals(&completions)

	// Write output
	output, err := json.MarshalIndent(completions, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling JSON: %v\n", err)
		os.Exit(1)
	}

	err = os.WriteFile(outputFile, output, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated %s with %d globals and %d method groups\n",
		outputFile, len(completions.Globals), len(completions.Methods))
}

func parseFile(content string, completions *Completions, modules map[string]string) {
	lines := strings.Split(content, "\n")

	var currentDesc string
	var currentParams []ParamInfo
	var currentReturn string
	var moduleName string
	var moduleDesc string

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])

		// Check for module declaration
		if match := modulePattern.FindStringSubmatch(line); match != nil {
			moduleName = match[1]
			// Get module description from previous lines
			if currentDesc != "" {
				moduleDesc = currentDesc
			}
			if moduleName != "" {
				// Extract just the class name from module path
				parts := strings.Split(moduleName, ".")
				className := parts[len(parts)-1]
				modules[className] = moduleDesc
			}
		}

		// Check for description start
		if match := descPattern.FindStringSubmatch(line); match != nil {
			currentDesc = match[1]
			currentParams = nil
			currentReturn = ""
			continue
		}

		// Check for continued description
		if match := continueDesc.FindStringSubmatch(line); match != nil {
			if currentDesc != "" {
				currentDesc += " " + match[1]
			}
			continue
		}

		// Check for @param
		if match := paramPattern.FindStringSubmatch(line); match != nil {
			paramName := match[1]
			paramType := match[2]
			paramDesc := match[3]
			optional := strings.HasSuffix(paramType, "?")
			if optional {
				paramType = strings.TrimSuffix(paramType, "?")
			}
			currentParams = append(currentParams, ParamInfo{
				Name:     paramName,
				Type:     paramType,
				Optional: optional,
				Desc:     paramDesc,
			})
			continue
		}

		// Check for @return
		if match := returnPattern.FindStringSubmatch(line); match != nil {
			currentReturn = match[1]
			continue
		}

		// Check for constant definition: CLASS.CONSTANT_NAME = number
		if match := constantPattern.FindStringSubmatch(line); match != nil {
			className := match[1]
			constName := match[2]
			constValue := match[3]
			constDoc := strings.TrimSpace(match[4])

			// Use preceding LDoc description if no inline comment
			if constDoc == "" && currentDesc != "" {
				constDoc = currentDesc
			}

			detail := "= " + constValue
			if constDoc != "" {
				detail += " — " + constDoc
			}

			item := CompletionItem{
				Label:         constName,
				Kind:          "constant",
				Detail:        detail,
				Documentation: constDoc,
				InsertText:    constName,
			}

			if completions.Methods[className] == nil {
				completions.Methods[className] = []CompletionItem{}
			}

			// Deduplicate
			found := false
			for _, existing := range completions.Methods[className] {
				if existing.Label == constName {
					found = true
					break
				}
			}
			if !found {
				completions.Methods[className] = append(completions.Methods[className], item)
			}

			currentDesc = ""
			currentParams = nil
			currentReturn = ""
			continue
		}

		// Check for function definition
		if match := funcPattern.FindStringSubmatch(line); match != nil {
			className := match[1]
			separator := match[2]
			funcName := match[3]
			// params := match[4]

			// Skip internal/private functions
			if strings.HasPrefix(funcName, "_") || strings.HasPrefix(funcName, "__") {
				currentDesc = ""
				currentParams = nil
				currentReturn = ""
				continue
			}

			// Create completion item
			item := createCompletionItem(funcName, currentDesc, currentParams, currentReturn, separator == ":")

			// Add to methods (deduplicate - prefer documented version)
			if completions.Methods[className] == nil {
				completions.Methods[className] = []CompletionItem{}
			}

			// Check if method already exists
			found := false
			for i, existing := range completions.Methods[className] {
				if existing.Label == funcName {
					found = true
					// Replace if new one has documentation
					if item.Documentation != "" && existing.Documentation == "" {
						completions.Methods[className][i] = item
					}
					break
				}
			}
			if !found {
				completions.Methods[className] = append(completions.Methods[className], item)
			}

			// Reset state
			currentDesc = ""
			currentParams = nil
			currentReturn = ""
		}
	}
}

func createCompletionItem(name, desc string, params []ParamInfo, returns string, isMethod bool) CompletionItem {
	// Build detail string (signature)
	var paramStrs []string
	for _, p := range params {
		if p.Name == "self" {
			continue // Skip self parameter
		}
		paramStr := p.Name
		if p.Type != "" {
			paramStr += ": " + p.Type
		}
		if p.Optional {
			paramStr += "?"
		}
		paramStrs = append(paramStrs, paramStr)
	}
	detail := "(" + strings.Join(paramStrs, ", ") + ")"
	if returns != "" {
		detail += ": " + returns
	}

	// Build insert text (snippet)
	var snippetParams []string
	snippetIdx := 1
	for _, p := range params {
		if p.Name == "self" {
			continue
		}
		snippetParams = append(snippetParams, fmt.Sprintf("${%d:%s}", snippetIdx, p.Name))
		snippetIdx++
	}
	insertText := name + "(" + strings.Join(snippetParams, ", ") + ")"

	return CompletionItem{
		Label:           name,
		Kind:            "method",
		Detail:          detail,
		Documentation:   desc,
		InsertText:      insertText,
		InsertTextRules: 4, // InsertAsSnippet
	}
}

func addBuiltinGlobals(completions *Completions) {
	builtins := []CompletionItem{
		{Label: "print", Kind: "function", Detail: "(...)", Documentation: "Prints arguments to stdout"},
		{Label: "type", Kind: "function", Detail: "(v): string", Documentation: "Returns type of value"},
		{Label: "tostring", Kind: "function", Detail: "(v): string", Documentation: "Converts value to string"},
		{Label: "tonumber", Kind: "function", Detail: "(v): number?", Documentation: "Converts value to number"},
		{Label: "pairs", Kind: "function", Detail: "(t): iterator", Documentation: "Iterate over table key-value pairs"},
		{Label: "ipairs", Kind: "function", Detail: "(t): iterator", Documentation: "Iterate over array indices"},
		{Label: "next", Kind: "function", Detail: "(t, k): k, v", Documentation: "Next key-value pair in table"},
		{Label: "pcall", Kind: "function", Detail: "(f, ...): ok, result", Documentation: "Protected call"},
		{Label: "xpcall", Kind: "function", Detail: "(f, err, ...): ok, result", Documentation: "Protected call with error handler"},
		{Label: "error", Kind: "function", Detail: "(message)", Documentation: "Raises an error"},
		{Label: "assert", Kind: "function", Detail: "(v, message?)", Documentation: "Assert condition is truthy"},
		{Label: "select", Kind: "function", Detail: "(n, ...)", Documentation: "Returns arguments after nth"},
		{Label: "setmetatable", Kind: "function", Detail: "(t, mt): t", Documentation: "Sets metatable for table"},
		{Label: "getmetatable", Kind: "function", Detail: "(t): mt", Documentation: "Gets metatable of table"},
		{Label: "rawget", Kind: "function", Detail: "(t, k): v", Documentation: "Raw table access"},
		{Label: "rawset", Kind: "function", Detail: "(t, k, v)", Documentation: "Raw table assignment"},
		{Label: "unpack", Kind: "function", Detail: "(t): ...", Documentation: "Unpack table to values"},
		{Label: "string", Kind: "module", Detail: "String manipulation", Documentation: "String library"},
		{Label: "table", Kind: "module", Detail: "Table manipulation", Documentation: "Table library"},
		{Label: "math", Kind: "module", Detail: "Math functions", Documentation: "Math library"},
		{Label: "os", Kind: "module", Detail: "OS functions", Documentation: "OS library"},
		{Label: "io", Kind: "module", Detail: "I/O functions", Documentation: "I/O library"},
		{Label: "_", Kind: "module", Detail: "Registry alias", Documentation: "Global registry for accessing CLU objects by path or name"},
	}
	completions.Globals = append(completions.Globals, builtins...)

	// Add registerObject global function
	completions.Globals = append(completions.Globals, CompletionItem{
		Label:           "registerObject",
		Kind:            "function",
		Detail:          "(path: string, obj: table)",
		Documentation:   "Registers an object in the global registry",
		InsertText:      "registerObject(${1:path}, ${2:obj})",
		InsertTextRules: 4,
	})

	// Timer helpers
	completions.Globals = append(completions.Globals, []CompletionItem{
		{Label: "after", Kind: "function", Detail: "(ms: number, fn: function): id", Documentation: "Execute function once after delay in milliseconds", InsertText: "after(${1:ms}, ${2:fn})", InsertTextRules: 4},
		{Label: "every", Kind: "function", Detail: "(ms: number, fn: function): id", Documentation: "Execute function repeatedly at interval in milliseconds", InsertText: "every(${1:ms}, ${2:fn})", InsertTextRules: 4},
		{Label: "cancel", Kind: "function", Detail: "(id: number)", Documentation: "Cancel a timer created by after() or every()", InsertText: "cancel(${1:id})", InsertTextRules: 4},
	}...)

	// Scene helpers
	completions.Globals = append(completions.Globals, []CompletionItem{
		{Label: "scene", Kind: "function", Detail: "(name: string, fn: function)", Documentation: "Register a named scene function", InsertText: "scene(${1:name}, ${2:fn})", InsertTextRules: 4},
		{Label: "runScene", Kind: "function", Detail: "(name: string, ...)", Documentation: "Execute a named scene with optional arguments", InsertText: "runScene(${1:name})", InsertTextRules: 4},
		{Label: "getScene", Kind: "function", Detail: "(name: string): function?", Documentation: "Get a scene function by name", InsertText: "getScene(${1:name})", InsertTextRules: 4},
		{Label: "getAllScenes", Kind: "function", Detail: "(): table", Documentation: "Get table of all registered scenes"},
	}...)

	// Expose helpers
	completions.Globals = append(completions.Globals, []CompletionItem{
		{Label: "expose", Kind: "function", Detail: "(obj: table, type: string, opts?: table)", Documentation: "Expose an object for MQTT/HomeKit integration", InsertText: "expose(${1:obj}, ${2:type})", InsertTextRules: 4},
		{Label: "exposeRegistry", Kind: "function", Detail: "(opts?: table)", Documentation: "Auto-expose all registry objects based on AccessControl settings", InsertText: "exposeRegistry()", InsertTextRules: 4},
	}...)

	// Sequence helpers
	completions.Globals = append(completions.Globals, []CompletionItem{
		{Label: "sequence", Kind: "function", Detail: "(): Sequence", Documentation: "Create a new step sequence builder"},
		{Label: "cascade", Kind: "function", Detail: "(group: RemoteGroup, delayMs: number, fn: function)", Documentation: "Execute function on each group member with staggered delay", InsertText: "cascade(${1:group}, ${2:delayMs}, ${3:fn})", InsertTextRules: 4},
	}...)

	// Math helpers
	completions.Globals = append(completions.Globals, []CompletionItem{
		{Label: "clamp", Kind: "function", Detail: "(value: number, min: number, max: number): number", Documentation: "Clamp value between min and max", InsertText: "clamp(${1:value}, ${2:min}, ${3:max})", InsertTextRules: 4},
		{Label: "lerp", Kind: "function", Detail: "(a: number, b: number, t: number): number", Documentation: "Linear interpolation between a and b by factor t", InsertText: "lerp(${1:a}, ${2:b}, ${3:t})", InsertTextRules: 4},
		{Label: "map", Kind: "function", Detail: "(value: number, inMin: number, inMax: number, outMin: number, outMax: number): number", Documentation: "Map value from input range to output range", InsertText: "map(${1:value}, ${2:inMin}, ${3:inMax}, ${4:outMin}, ${5:outMax})", InsertTextRules: 4},
		{Label: "round", Kind: "function", Detail: "(value: number, decimals?: number): number", Documentation: "Round number to given decimal places", InsertText: "round(${1:value}, ${2:decimals})", InsertTextRules: 4},
	}...)

	// Debug helpers
	completions.Globals = append(completions.Globals, []CompletionItem{
		{Label: "inspect", Kind: "function", Detail: "(value: any, depth?: number): string", Documentation: "Return human-readable string representation of a value", InsertText: "inspect(${1:value})", InsertTextRules: 4},
		{Label: "dump", Kind: "function", Detail: "(value: any, label?: string)", Documentation: "Print value with optional label for debugging", InsertText: "dump(${1:value}, ${2:label})", InsertTextRules: 4},
	}...)

	// Module-style globals
	completions.Globals = append(completions.Globals, []CompletionItem{
		{Label: "log", Kind: "module", Detail: "Logging module", Documentation: "Structured logging with debug/info/warn/error levels"},
		{Label: "gpio", Kind: "module", Detail: "GPIO module", Documentation: "Low-level GPIO pin access (setup, read, write, toggle, release)"},
		{Label: "scenes", Kind: "module", Detail: "Scene manager", Documentation: "Scene management module"},
	}...)

	// Add _ (registry alias) methods
	completions.Methods["_"] = []CompletionItem{
		{Label: "get", Kind: "method", Detail: "(pathOrName: string): RemoteObject?", Documentation: "Get object by path or name from registry", InsertText: "get(${1:pathOrName})", InsertTextRules: 4},
		{Label: "byTag", Kind: "method", Detail: "(tag: string): RemoteGroup", Documentation: "Get all objects with a specific tag", InsertText: "byTag(${1:tag})", InsertTextRules: 4},
		{Label: "byTags", Kind: "method", Detail: "(...tags: string): RemoteGroup", Documentation: "Get all objects matching any of the tags", InsertText: "byTags(${1:tags})", InsertTextRules: 4},
		{Label: "sync", Kind: "method", Detail: "(pathOrName: string, value: any)", Documentation: "Synchronize object state and emit event", InsertText: "sync(${1:pathOrName}, ${2:value})", InsertTextRules: 4},
		{Label: "event", Kind: "method", Detail: "(pathOrName: string)", Documentation: "Emit event on object", InsertText: "event(${1:pathOrName})", InsertTextRules: 4},
		{Label: "list", Kind: "method", Detail: "(): table", Documentation: "List all registered objects", InsertText: "list()", InsertTextRules: 4},
		{Label: "find", Kind: "method", Detail: "(pattern: string): table", Documentation: "Find objects matching Lua pattern", InsertText: "find(${1:pattern})", InsertTextRules: 4},
		{Label: "inspect", Kind: "method", Detail: "(pathOrName: string): string", Documentation: "Get detailed info about an object", InsertText: "inspect(${1:pathOrName})", InsertTextRules: 4},
		{Label: "info", Kind: "method", Detail: "(): string", Documentation: "Get registry statistics", InsertText: "info()", InsertTextRules: 4},
		{Label: "tags", Kind: "method", Detail: "(): table", Documentation: "Get list of all tags", InsertText: "tags()", InsertTextRules: 4},
		{Label: "toJSON", Kind: "method", Detail: "(): string", Documentation: "Export registry as JSON", InsertText: "toJSON()", InsertTextRules: 4},
	}

	// Add log methods
	completions.Methods["log"] = []CompletionItem{
		{Label: "debug", Kind: "method", Detail: "(msg: string, ...)", Documentation: "Log debug message", InsertText: "debug(${1:msg})", InsertTextRules: 4},
		{Label: "info", Kind: "method", Detail: "(msg: string, ...)", Documentation: "Log info message", InsertText: "info(${1:msg})", InsertTextRules: 4},
		{Label: "warn", Kind: "method", Detail: "(msg: string, ...)", Documentation: "Log warning message", InsertText: "warn(${1:msg})", InsertTextRules: 4},
		{Label: "error", Kind: "method", Detail: "(msg: string, ...)", Documentation: "Log error message", InsertText: "error(${1:msg})", InsertTextRules: 4},
	}

	// Add gpio methods (lowercase — user-facing API)
	completions.Methods["gpio"] = []CompletionItem{
		{Label: "setup", Kind: "method", Detail: "(pin: number, mode: string, pull?: string, safeState?: boolean): boolean", Documentation: "Setup a GPIO pin with mode (input/output) and optional pull resistor", InsertText: "setup(${1:pin}, ${2:mode})", InsertTextRules: 4},
		{Label: "read", Kind: "method", Detail: "(pin: number): number", Documentation: "Read pin value (0 or 1)", InsertText: "read(${1:pin})", InsertTextRules: 4},
		{Label: "write", Kind: "method", Detail: "(pin: number, value: boolean)", Documentation: "Write to an output pin (true=HIGH, false=LOW)", InsertText: "write(${1:pin}, ${2:value})", InsertTextRules: 4},
		{Label: "toggle", Kind: "method", Detail: "(pin: number)", Documentation: "Toggle an output pin", InsertText: "toggle(${1:pin})", InsertTextRules: 4},
		{Label: "release", Kind: "method", Detail: "(pin: number)", Documentation: "Release a pin and clear its registry entry", InsertText: "release(${1:pin})", InsertTextRules: 4},
		{Label: "stats", Kind: "method", Detail: "(): table", Documentation: "Get GPIO manager statistics", InsertText: "stats()", InsertTextRules: 4},
	}
}
