package plugins

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/luastr"
)

// DiscoverTimeout: how long the wizard waits for a plugin's discover.
const DiscoverTimeout = 30 * time.Second

const maxModuleCode = 256 << 10

// Form reads the wizard of an installed plugin (nil when it has none).
func (l *Loader) Form(pluginID string) (*Form, json.RawMessage, error) {
	return ReadForm(l.resolvePluginDir(pluginID))
}

// Discover runs the plugin's discover and returns the rows that pass its device fields,
// with the reasons for rows dropped.
func (l *Loader) Discover(ctx context.Context, pluginID string) ([]map[string]any, []string, error) {
	form, _, err := l.Form(pluginID)
	if err != nil {
		return nil, nil, err
	}
	if form == nil || form.Devices == nil || !form.Devices.Discover {
		return nil, nil, fmt.Errorf("the plugin does not discover devices")
	}
	if !l.IsLoaded(pluginID) {
		return nil, nil, fmt.Errorf("the plugin is not running: save its settings first")
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	req := hex.EncodeToString(b)
	if _, err := l.executeAsPlugin(pluginID, fmt.Sprintf(`PluginSandbox:discover(%s, %s)`, luastr.Quote(pluginID), luastr.Quote(req))); err != nil {
		return nil, nil, fmt.Errorf("discover: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, DiscoverTimeout)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		out, err := l.luaExecutor.Execute(fmt.Sprintf(`return PluginSandbox:takeDiscovered(%s)`, luastr.Quote(req)))
		if err != nil {
			return nil, nil, err
		}
		if out != "" && out != "nil" {
			var r struct {
				Rows json.RawMessage `json:"rows"`
				Err  string          `json:"err"`
			}
			if err := json.Unmarshal([]byte(out), &r); err != nil {
				return nil, nil, fmt.Errorf("discover: %w", err)
			}
			if r.Err != "" {
				return nil, nil, errors.New(r.Err)
			}
			var rows []map[string]any
			if t := bytes.TrimSpace(r.Rows); len(t) > 0 && !bytes.Equal(t, []byte("{}")) {
				if err := json.Unmarshal(t, &rows); err != nil {
					return nil, nil, fmt.Errorf("discover: rows are a list of tables")
				}
			}
			ok, dropped := form.Devices.DiscoveredRows(rows)
			return ok, dropped, nil
		}
		select {
		case <-ctx.Done():
			_, _ = l.luaExecutor.Execute(fmt.Sprintf(`PluginSandbox:forgetDiscover(%s)`, luastr.Quote(req)))
			return nil, nil, fmt.Errorf("discover gave no answer in %s", DiscoverTimeout)
		case <-tick.C:
		}
	}
}

// ModuleCode returns the Lua the plugin proposes for its saved devices.
func (l *Loader) ModuleCode(pluginID string) (string, error) {
	if !l.IsLoaded(pluginID) {
		return "", fmt.Errorf("the plugin is not running")
	}
	out, err := l.executeAsPlugin(pluginID, fmt.Sprintf(`return PluginSandbox:moduleCode(%s)`, luastr.Quote(pluginID)))
	if err != nil {
		return "", err
	}
	var r struct {
		Code string `json:"code"`
		Err  string `json:"err"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return "", fmt.Errorf("moduleCode: %w", err)
	}
	if r.Err != "" {
		return "", errors.New(r.Err)
	}
	if len(r.Code) > maxModuleCode {
		return "", fmt.Errorf("moduleCode: longer than %d KiB", maxModuleCode>>10)
	}
	code := strings.ReplaceAll(r.Code, "\r", "")
	if err := checkModuleCode(code); err != nil {
		return "", err
	}
	return code, nil
}

// checkModuleCode: the code is read in a diff before it runs, so nothing in it may hide or
// reorder what the diff shows.
func checkModuleCode(code string) error {
	if !utf8.ValidString(code) {
		return fmt.Errorf("moduleCode: not UTF-8")
	}
	for i, line := range strings.Split(code, "\n") {
		if strings.ContainsFunc(line, hiddenRune) {
			return fmt.Errorf("moduleCode: line %d has control or bidi characters", i+1)
		}
	}
	return nil
}

// ModuleName: the plugin's module under modules/plugins/, from its full id. Ids have no
// "_", so "." → "_" and "__" between namespace and id never make two ids one name.
func ModuleName(pluginID string) string {
	ns, id := parseFullId(pluginID)
	name := strings.ReplaceAll(id, ".", "_")
	if ns != "" {
		name = strings.ReplaceAll(ns, ".", "_") + "__" + name
	}
	return name
}

// ModuleNameTaken: another of ids whose module file name is pluginID's ("" when none). Names are
// kept as they are (modules written already), so two such plugins are not installed together.
func ModuleNameTaken(pluginID string, ids []string) string {
	name := ModuleName(pluginID)
	for _, id := range ids {
		if id != pluginID && ModuleName(id) == name {
			return id
		}
	}
	return ""
}

// moduleOwner: the plugin whose wizard wrote the module at path, from its header; "" if none.
func moduleOwner(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(data), "\n")
	rest, ok := strings.CutPrefix(first, "-- Written by the wizard of ")
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(rest, ". ")
	return id
}

// ModulePath: modules/plugins/<name>.lua under serverDir.
func ModulePath(serverDir, pluginID string) string {
	return filepath.Join(serverDir, "modules", "plugins", ModuleName(pluginID)+".lua")
}

// WriteModuleCode writes the plugin's module and the require lines that load it:
// modules/plugins/init.lua requires each plugin's module, modules/init.lua requires plugins.
func WriteModuleCode(serverDir, pluginID, code string) error {
	moduleMu.Lock()
	defer moduleMu.Unlock()
	if !ValidPluginID(pluginID) || ModuleName(pluginID) == "init" { // init.lua is the list
		return fmt.Errorf("invalid plugin id")
	}
	// another plugin's module under the same file name is not written over
	if owner := moduleOwner(ModulePath(serverDir, pluginID)); owner != "" && owner != pluginID {
		return fmt.Errorf("%s is the module of %s", "modules/plugins/"+ModuleName(pluginID)+".lua", owner)
	}
	if mc, err := config.ReadMasterConfig(serverDir); err == nil && mc != nil && mc.Plugins != nil {
		var ids []string
		for _, inst := range mc.Plugins.Installed {
			ids = append(ids, inst.ID)
		}
		if other := ModuleNameTaken(pluginID, ids); other != "" {
			return fmt.Errorf("%s and %s would share modules/plugins/%s.lua", pluginID, other, ModuleName(pluginID))
		}
	}
	dir := filepath.Join(serverDir, "modules", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(ModulePath(serverDir, pluginID), []byte(ModuleText(pluginID, code)), 0o644); err != nil {
		return err
	}
	line := requireLine(pluginID)
	if _, err := turnOnLine(filepath.Join(dir, "init.lua"), line); err != nil {
		return err
	}
	if err := ensureLine(filepath.Join(dir, "init.lua"),
		"-- Modules written by plugin wizards. Comment out a line to turn one off.\n", line); err != nil {
		return err
	}
	return ensureLine(filepath.Join(serverDir, "modules", "init.lua"), "", `require("plugins")`)
}

func requireLine(pluginID string) string {
	return fmt.Sprintf("require(%q)", "plugins/"+ModuleName(pluginID))
}

// offMark ends a require line turned off when its plugin was uninstalled; installing it again
// or running its wizard turns it back on. A line the user turned off has no mark and stays off.
const offMark = " -- off: plugin uninstalled"

// moduleMu: one change at a time to the modules a wizard writes and their list.
var moduleMu sync.Mutex

// TurnOffModule comments out the require line of the plugin's wizard module, so the module,
// kept as it is, does not load without its plugin; also when the file is gone. Nothing when
// another plugin's wizard wrote the file. changed: the list was written.
func TurnOffModule(serverDir, pluginID string) (changed bool, err error) {
	if !ValidPluginID(pluginID) || ModuleName(pluginID) == "init" {
		return false, nil
	}
	moduleMu.Lock()
	defer moduleMu.Unlock()
	path := ModulePath(serverDir, pluginID)
	if owner := moduleOwner(path); owner != pluginID {
		if _, err := os.Stat(path); owner != "" || !errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
	}
	line := requireLine(pluginID)
	return editLines(filepath.Join(serverDir, "modules", "plugins", "init.lua"), func(l string) (string, bool) {
		return "-- " + line + offMark, strings.TrimSpace(l) == line
	})
}

// TurnOnModule: the plugin's wizard module turned off at uninstall loads again (installing it again).
func TurnOnModule(serverDir, pluginID string) (changed bool, err error) {
	if !ValidPluginID(pluginID) || ModuleName(pluginID) == "init" {
		return false, nil
	}
	moduleMu.Lock()
	defer moduleMu.Unlock()
	if moduleOwner(ModulePath(serverDir, pluginID)) != pluginID {
		return false, nil
	}
	return turnOnLine(filepath.Join(serverDir, "modules", "plugins", "init.lua"), requireLine(pluginID))
}

// turnOnLine: a require line TurnOffModule commented out is plain again.
func turnOnLine(path, line string) (bool, error) {
	return editLines(path, func(l string) (string, bool) {
		return line, strings.TrimSpace(l) == "-- "+line+offMark
	})
}

// editLines writes the file with the lines match picks replaced, through a temporary file so a
// reload never reads half of it; a missing file is left so.
func editLines(path string, match func(string) (string, bool)) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	lines := strings.Split(string(data), "\n")
	changed := false
	for i, l := range lines {
		if n, ok := match(strings.TrimSuffix(l, "\r")); ok {
			lines[i], changed = n, true
		}
	}
	if !changed {
		return false, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// ModuleText: the module file for the code a plugin proposes, with its header.
func ModuleText(pluginID, code string) string {
	code = "-- Written by the wizard of " + pluginID + ". Running the wizard again shows a diff.\n" + code
	if !strings.HasSuffix(code, "\n") {
		code += "\n"
	}
	return code
}

// ensureLine adds line to the file unless the file already has it, commented out or not:
// a line the user turned off stays off.
func ensureLine(path, header, line string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-"))
		if l == line {
			return nil
		}
	}
	text := string(data)
	if text == "" {
		text = header
	} else if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	// a chunk may end with return: the line goes before it
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}
		if t == "return" || strings.HasPrefix(t, "return ") || strings.HasPrefix(t, "return(") {
			lines = append(lines[:i], append([]string{line}, lines[i:]...)...)
			return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		}
		break
	}
	return os.WriteFile(path, []byte(text+line+"\n"), 0o644)
}

// ReadModuleCode returns the plugin's module as it is now ("" when there is none).
func ReadModuleCode(serverDir, pluginID string) (string, error) {
	data, err := os.ReadFile(ModulePath(serverDir, pluginID))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}
