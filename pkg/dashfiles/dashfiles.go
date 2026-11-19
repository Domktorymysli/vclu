// Package dashfiles keeps the dashboard files generated from the myGrenton interface (#220):
// one page_<id>.lua per page and an init.lua loading them, in modules/dashboards/imported/.
// vclu writes a file only when the user asks, and the file is theirs from then on: a header
// with the hash of what vclu wrote tells a file vclu may replace from one someone changed.
package dashfiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/gopher-lua/parse"

	"github.com/Domktorymysli/vclu/pkg/dashspec"
)

const (
	// Dir is the directory of the files, under modules/.
	Dir = "dashboards/imported"
	// Require is the line in modules/init.lua that loads them.
	Require = `require("dashboards/imported")`
	// headerPrefix starts a file vclu wrote; the hash is of what follows the header line
	headerPrefix = "-- vclu: generated, sha256:"
)

var (
	reHeader   = regexp.MustCompile(`^-- vclu: generated, sha256:([0-9a-f]{64})\n`)
	rePageFile = regexp.MustCompile(`^page_([0-9]{1,19})\.lua$`)
	// the require line at the start of a line, comments taken out first
	reRequire      = regexp.MustCompile(`(?m)^[ \t]*require[ \t]*\(?[ \t]*["']dashboards[/.]imported["'][ \t]*\)?`)
	reBlockComment = regexp.MustCompile(`(?s)--\[(=*)\[.*?\]`)
)

// State says whose a file is.
type State string

const (
	None   State = "none"   // not written
	VCLU   State = "vclu"   // written by vclu, unchanged: may be replaced after asking
	Edited State = "edited" // written by vclu, then changed: the user's
	User   State = "user"   // not written by vclu
)

// PageFile is the file of a page.
func PageFile(id string) string { return "page_" + id + ".lua" }

// module is the Lua module of a file in Dir.
func module(file string) string { return Dir + "/" + strings.TrimSuffix(file, ".lua") }

// StateOf says whose the file at path is.
func StateOf(path string) State {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return None
	}
	if err != nil {
		return User
	}
	m := reHeader.FindSubmatchIndex(b)
	if m == nil {
		return User
	}
	sum := sha256.Sum256(b[m[1]:])
	if hex.EncodeToString(sum[:]) != string(b[m[2]:m[3]]) {
		return Edited
	}
	return VCLU
}

// withHeader puts the header with the content's hash before it.
func withHeader(content string) string {
	sum := sha256.Sum256([]byte(content))
	return headerPrefix + hex.EncodeToString(sum[:]) + "\n" + content
}

// write writes next to the file and renames, so a failed write leaves the old one.
func write(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Files are the dashboard files under one modules/ directory.
type Files struct {
	modules string
}

// New gives the files under the modules directory.
func New(modules string) Files { return Files{modules: modules} }

// Path is the directory of the files.
func (f Files) Path() string { return filepath.Join(f.modules, filepath.FromSlash(Dir)) }

// State says whose a file of the directory is.
func (f Files) State(file string) State { return StateOf(filepath.Join(f.Path(), file)) }

// PageFiles are the page files in the directory, by name.
func (f Files) PageFiles() []string {
	entries, _ := os.ReadDir(f.Path())
	var files []string
	for _, e := range entries {
		if !e.IsDir() && rePageFile.MatchString(e.Name()) {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files
}

// Flat is modules/dashboards/imported.lua, which require would load instead of the directory.
func (f Files) Flat() bool {
	_, err := os.Stat(filepath.Join(f.modules, "dashboards", "imported.lua"))
	return err == nil
}

// Page is a page file to write.
type Page struct {
	File    string
	Content string // without the header
}

// Conflicts sorts the pages about to be written: kept are the user's and are never replaced,
// ask are vclu's and differ from what would be written (the same file again is no change).
func (f Files) Conflicts(pages []Page) (ask, kept []string) {
	for _, p := range pages {
		switch f.State(p.File) {
		case VCLU:
			if b, err := os.ReadFile(filepath.Join(f.Path(), p.File)); err != nil || string(b) != withHeader(p.Content) {
				ask = append(ask, p.File)
			}
		case Edited, User:
			kept = append(kept, p.File)
		}
	}
	return ask, kept
}

// WritePages writes the pages with their headers; written are the files written before an error.
func (f Files) WritePages(pages []Page) (written []string, err error) {
	if err := os.MkdirAll(f.Path(), 0755); err != nil {
		return nil, err
	}
	for _, p := range pages {
		if err := write(filepath.Join(f.Path(), p.File), withHeader(p.Content)); err != nil {
			return written, err
		}
		written = append(written, p.File)
	}
	return written, nil
}

// Modules are the modules init.lua loads: the page files there in the order given (the
// interfaces' pages), then the rest by name.
func (f Files) Modules(order []string) []string {
	files := f.PageFiles()
	present := map[string]bool{}
	for _, file := range files {
		present[file] = true
	}
	var modules []string
	seen := map[string]bool{}
	for _, file := range order {
		if present[file] && !seen[file] {
			seen[file] = true
			modules = append(modules, module(file))
		}
	}
	for _, file := range files {
		if !seen[file] {
			modules = append(modules, module(file))
		}
	}
	return modules
}

// WriteInit writes init.lua loading the modules when it is vclu's (or missing). When it is the
// user's it is left as it is and lines are what to add to it for the pages it does not load.
func (f Files) WriteInit(modules, pages []string) (written bool, lines []string, err error) {
	path := filepath.Join(f.Path(), "init.lua")
	switch StateOf(path) {
	case None, VCLU:
		if err := write(path, withHeader(dashspec.InitLua(modules))); err != nil {
			return false, nil, err
		}
		return true, nil, nil
	}
	b, _ := os.ReadFile(path)
	code := luaCode(string(b))
	for _, file := range pages {
		// the quoted name, so page_1 is not found in page_12
		if m := module(file); !strings.Contains(code, `"`+m+`"`) && !strings.Contains(code, `'`+m+`'`) {
			lines = append(lines, dashspec.LoadLine(m))
		}
	}
	return false, lines, nil
}

// Required says whether modules/init.lua loads the dashboards.
func (f Files) Required() bool {
	b, err := os.ReadFile(filepath.Join(f.modules, "init.lua"))
	if err != nil {
		return false
	}
	return reRequire.MatchString(blockComments(string(b)))
}

// AddRequire appends the require line to modules/init.lua, creating it when missing.
// It leaves the file as it is (false) when the line would not compile there.
func (f Files) AddRequire() (bool, error) {
	path := filepath.Join(f.modules, "init.lua")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("modules/init.lua: %v", err)
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	b = append(b, []byte("\n-- Imported dashboards (Grenton page > Dashboards)\n"+Require+"\n")...)
	if _, err := parse.Parse(bytes.NewReader(b), "init.lua"); err != nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, fmt.Errorf("modules/init.lua: %v", err)
	}
	if err := write(path, string(b)); err != nil {
		return false, fmt.Errorf("modules/init.lua: %v", err)
	}
	return true, nil
}

// luaCode is code without its comments, near enough to look for a module name in it.
func luaCode(code string) string {
	lines := strings.Split(blockComments(code), "\n")
	for i, l := range lines {
		if j := strings.Index(l, "--"); j >= 0 {
			lines[i] = l[:j]
		}
	}
	return strings.Join(lines, "\n")
}

// blockComments takes out --[[ ]] and --[==[ ]==] comments.
func blockComments(code string) string {
	for {
		m := reBlockComment.FindStringSubmatchIndex(code)
		if m == nil {
			return code
		}
		closing := "]" + code[m[2]:m[3]] + "]"
		end := strings.Index(code[m[1]-1:], closing)
		if end < 0 {
			return code[:m[0]]
		}
		code = code[:m[0]] + code[m[1]-1+end+len(closing):]
	}
}
