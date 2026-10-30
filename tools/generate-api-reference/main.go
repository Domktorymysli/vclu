package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// FuncDoc represents a parsed function with @api public tag
type FuncDoc struct {
	Module      string
	ClassName   string
	FuncName    string
	Separator   string // ":" or "."
	Description string
	Params      []ParamDoc
	Returns     string
	Usage       string
	Args        string // argument list from the source, for functions without @param
	IsGlobal    bool
	LineNumber  int
}

// ParamDoc represents a function parameter
type ParamDoc struct {
	Name string
	Type string
	Desc string
}

// ModuleDoc groups functions by module
type ModuleDoc struct {
	Name      string
	Desc      string
	Functions []FuncDoc
	Order     int
}

var (
	modulePattern  = regexp.MustCompile(`@module\s+(\S+)`)
	funcPattern    = regexp.MustCompile(`^function\s+(\w+)([:.])(\w+)\s*\(([^)]*)\)`)
	globalFunc     = regexp.MustCompile(`^function\s+(\w+)\s*\(([^)]*)\)`)
	paramPattern   = regexp.MustCompile(`@param\s+(\w+)\s+(\w+)\s*(.*)`)
	returnPattern  = regexp.MustCompile(`@return\s+(.+)`)
	usagePattern   = regexp.MustCompile(`@usage\s+(.+)`)
	apiPattern     = regexp.MustCompile(`@api\s+(\w+)`)
	descPattern    = regexp.MustCompile(`^---\s*(.+)`)
	contPattern    = regexp.MustCompile(`^--\s*(.+)`)
	tableMethodPat = regexp.MustCompile(`^(\w+)\s*=\s*function\s*\(([^)]*)\)`)
	tableStartPat  = regexp.MustCompile(`^local\s+(\w+)\s*=\s*\{`)
)

// Module ordering for output (most important first)
var moduleOrder = map[string]int{
	"utils.Helpers":                1,
	"objects.VirtualSwitch":        10,
	"objects.VirtualDimmer":        11,
	"objects.VirtualNumber":        12,
	"objects.VirtualSensor":        13,
	"objects.VirtualText":          14,
	"objects.VirtualCover":         15,
	"objects.VirtualObject":        16,
	"objects.VirtualThermostat":    17,
	"objects.VirtualPresence":      18,
	"ExposedObjects":               20,
	"core.Registry":                25,
	"core.Metrics":                 30,
	"Dashboard":                    35,
	"core.Calendar":                34,
	"lib.panel":                    36,
	"objects.RemotePanel":          37,
	"GPIO_DOUT":                    40,
	"GPIO_DIN":                     41,
	"integrations.mqtt.MqttClient": 50,
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <lua-source-dir> <output.md>\n", os.Args[0])
		os.Exit(1)
	}

	sourceDir := os.Args[1]
	outputFile := os.Args[2]

	modules := map[string]*ModuleDoc{}

	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".lua") {
			return nil
		}
		parseFile(path, modules)
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error walking directory: %v\n", err)
		os.Exit(1)
	}

	output := generateMarkdown(modules)

	if err := os.WriteFile(outputFile, []byte(output), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing output: %v\n", err)
		os.Exit(1)
	}

	// Count
	total := 0
	for _, mod := range modules {
		total += len(mod.Functions)
	}
	fmt.Printf("Generated %s: %d modules, %d public functions\n", outputFile, len(modules), total)
}

func parseFile(path string, modules map[string]*ModuleDoc) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var (
		currentModule string
		moduleDesc    string
		docBlock      []string
		lineNum       int
		currentTable  string
		tableDepth    int
	)

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Detect @module
		if m := modulePattern.FindStringSubmatch(trimmed); m != nil {
			currentModule = m[1]
		}

		// Detect module description (first --- line before any function)
		if currentModule == "" {
			if m := descPattern.FindStringSubmatch(trimmed); m != nil {
				if !strings.Contains(m[1], "@") {
					moduleDesc = m[1]
				}
			}
		}

		// Track table context for table-defined methods
		if currentTable == "" {
			if m := tableStartPat.FindStringSubmatch(trimmed); m != nil {
				currentTable = m[1]
				tableDepth = strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			}
		} else {
			tableDepth += strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			if tableDepth <= 0 {
				currentTable = ""
				tableDepth = 0
			}
		}

		// Collect doc comment blocks
		if strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "-- @") {
			docBlock = append(docBlock, trimmed)
			continue
		}
		if strings.HasPrefix(trimmed, "--") && len(docBlock) > 0 {
			docBlock = append(docBlock, trimmed)
			continue
		}

		// Check if this line is a function definition
		if strings.HasPrefix(trimmed, "function ") && len(docBlock) > 0 {
			fn := parseDocBlock(docBlock, trimmed, lineNum)
			if fn != nil && fn.Module == "" {
				if currentModule != "" {
					fn.Module = currentModule
				} else {
					fn.Module = inferModule(path)
				}
			}

			if fn != nil {
				modName := fn.Module
				if _, ok := modules[modName]; !ok {
					order := 99
					if o, found := moduleOrder[modName]; found {
						order = o
					}
					modules[modName] = &ModuleDoc{
						Name:  modName,
						Desc:  moduleDesc,
						Order: order,
					}
				}
				modules[modName].Functions = append(modules[modName].Functions, *fn)
			}
		}

		// Check for table-defined methods: name = function(args)
		if currentTable != "" && len(docBlock) > 0 && !strings.HasPrefix(trimmed, "function ") {
			if m := tableMethodPat.FindStringSubmatch(trimmed); m != nil {
				methodName := m[1]
				args := m[2]
				args = strings.TrimPrefix(args, "self, ")
				args = strings.TrimPrefix(args, "self")
				className := strings.TrimSuffix(currentTable, "_methods")
				funcLine := fmt.Sprintf("function %s:%s(%s)", className, methodName, args)
				fn := parseDocBlock(docBlock, funcLine, lineNum)
				if fn != nil && fn.Module == "" {
					if currentModule != "" {
						fn.Module = currentModule
					} else {
						fn.Module = inferModule(path)
					}
				}
				if fn != nil {
					// Filter out "self" parameter (implicit with : syntax)
					filtered := make([]ParamDoc, 0, len(fn.Params))
					for _, p := range fn.Params {
						if p.Name != "self" {
							filtered = append(filtered, p)
						}
					}
					fn.Params = filtered

					modName := fn.Module
					if _, ok := modules[modName]; !ok {
						order := 99
						if o, found := moduleOrder[modName]; found {
							order = o
						}
						modules[modName] = &ModuleDoc{
							Name:  modName,
							Desc:  moduleDesc,
							Order: order,
						}
					}
					modules[modName].Functions = append(modules[modName].Functions, *fn)
				}
			}
		}

		// Reset doc block if we hit a non-comment, non-function line
		if !strings.HasPrefix(trimmed, "--") && !strings.HasPrefix(trimmed, "function ") {
			docBlock = nil
		}
	}
}

func parseDocBlock(block []string, funcLine string, lineNum int) *FuncDoc {
	// Check for @api public
	isPublic := false
	for _, line := range block {
		if m := apiPattern.FindStringSubmatch(line); m != nil {
			if m[1] == "public" {
				isPublic = true
			}
		}
	}
	if !isPublic {
		return nil
	}

	fn := &FuncDoc{LineNumber: lineNum}

	// Parse function signature
	trimmed := strings.TrimSpace(funcLine)
	if m := funcPattern.FindStringSubmatch(trimmed); m != nil {
		fn.ClassName = m[1]
		fn.Separator = m[2]
		fn.FuncName = m[3]
		fn.Args = strings.TrimSpace(m[4])
	} else if m := globalFunc.FindStringSubmatch(trimmed); m != nil {
		fn.FuncName = m[1]
		fn.Args = strings.TrimSpace(m[2])
		fn.IsGlobal = true
	} else {
		return nil
	}

	// Parse doc comments
	for _, line := range block {
		if m := descPattern.FindStringSubmatch(line); m != nil {
			text := m[1]
			// Skip lines that are just tags
			if !strings.HasPrefix(text, "@") {
				if fn.Description == "" {
					fn.Description = text
				} else {
					fn.Description += " " + text
				}
			}
		} else if m := contPattern.FindStringSubmatch(line); m != nil {
			text := strings.TrimSpace(m[1])
			if strings.HasPrefix(text, "@param") {
				if pm := paramPattern.FindStringSubmatch(text); pm != nil {
					fn.Params = append(fn.Params, ParamDoc{
						Name: pm[1],
						Type: pm[2],
						Desc: strings.TrimSpace(pm[3]),
					})
				}
			} else if strings.HasPrefix(text, "@return") {
				if rm := returnPattern.FindStringSubmatch(text); rm != nil {
					fn.Returns = rm[1]
				}
			} else if strings.HasPrefix(text, "@usage") {
				if um := usagePattern.FindStringSubmatch(text); um != nil {
					fn.Usage = um[1]
				}
			} else if !strings.HasPrefix(text, "@") {
				// Continuation of description
				if fn.Description != "" {
					fn.Description += " " + text
				}
			}
		}
	}

	return fn
}

func inferModule(path string) string {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, ".lua")

	// Check parent directory for grouping
	dir := filepath.Base(filepath.Dir(path))
	switch dir {
	case "objects":
		return "objects." + name
	case "core":
		return "core." + name
	case "utils":
		return "utils." + name
	case "integrations":
		return name
	case "gpio":
		return name
	case "mqtt":
		return "integrations.mqtt." + name
	}
	return name
}

func generateMarkdown(modules map[string]*ModuleDoc) string {
	var sb strings.Builder

	sb.WriteString("# vCLU Lua API Reference\n\n")
	sb.WriteString("Auto-generated from `@api public` tags in Lua sources.\n")
	sb.WriteString("Only public API is listed — internal/private functions are omitted.\n\n")

	// Sort modules by order
	sorted := make([]*ModuleDoc, 0, len(modules))
	for _, mod := range modules {
		sorted = append(sorted, mod)
	}
	// by name within an order: the output does not change from run to run
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Order != sorted[j].Order {
			return sorted[i].Order < sorted[j].Order
		}
		return sorted[i].Name < sorted[j].Name
	})

	// TOC
	sb.WriteString("## Table of Contents\n\n")
	for _, mod := range sorted {
		anchor := strings.ReplaceAll(strings.ToLower(mod.Name), ".", "")
		sb.WriteString(fmt.Sprintf("- [%s](#%s) (%d functions)\n", mod.Name, anchor, len(mod.Functions)))
	}
	sb.WriteString("\n---\n\n")

	// Each module
	for _, mod := range sorted {
		sb.WriteString(fmt.Sprintf("## %s\n\n", mod.Name))

		for _, fn := range mod.Functions {
			// Function signature
			sig := formatSignature(fn)
			sb.WriteString(fmt.Sprintf("### `%s`\n\n", sig))

			if fn.Description != "" {
				sb.WriteString(fn.Description + "\n\n")
			}

			if len(fn.Params) > 0 {
				for _, p := range fn.Params {
					desc := ""
					if p.Desc != "" {
						desc = " — " + p.Desc
					}
					sb.WriteString(fmt.Sprintf("- `%s` (%s)%s\n", p.Name, p.Type, desc))
				}
				sb.WriteString("\n")
			}

			if fn.Returns != "" {
				sb.WriteString(fmt.Sprintf("**Returns:** %s\n\n", fn.Returns))
			}

			if fn.Usage != "" {
				sb.WriteString(fmt.Sprintf("```lua\n%s\n```\n\n", fn.Usage))
			}
		}

		sb.WriteString("---\n\n")
	}

	return sb.String()
}

func formatSignature(fn FuncDoc) string {
	if fn.IsGlobal {
		params := formatParams(fn)
		ret := ""
		if fn.Returns != "" {
			ret = " → " + fn.Returns
		}
		return fmt.Sprintf("%s(%s)%s", fn.FuncName, params, ret)
	}

	params := formatParams(fn)
	ret := ""
	if fn.Returns != "" {
		ret = " → " + fn.Returns
	}
	return fmt.Sprintf("%s%s%s(%s)%s", fn.ClassName, fn.Separator, fn.FuncName, params, ret)
}

// formatParams names the documented parameters, or the source's argument list when none are
func formatParams(fn FuncDoc) string {
	if len(fn.Params) == 0 {
		return fn.Args
	}
	names := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}
