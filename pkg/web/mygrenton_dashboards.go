package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuin/gopher-lua/parse"

	"github.com/Domktorymysli/vclu/pkg/dashspec"
)

// Dashboards from the myGrenton interface (#220): generated from vclu's model of
// it (dashspec), one file per page in modules/dashboards/imported/, written only
// when the user asks, theirs from then on.

const (
	grentonDashDir = "dashboards/imported" // under modules/
	grentonRequire = `require("dashboards/imported")`
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

// File states of a page.
const (
	fileNone   = "none"   // not written
	fileVCLU   = "vclu"   // written by vclu, unchanged: may be replaced after asking
	fileEdited = "edited" // written by vclu, then changed: the user's
	fileUser   = "user"   // not written by vclu
)

func (s *Server) grentonDashPath() string {
	return filepath.Join(s.serverDir, modulesDirName, filepath.FromSlash(grentonDashDir))
}

func pageFile(id string) string { return "page_" + id + ".lua" }

func pageModule(file string) string { return grentonDashDir + "/" + strings.TrimSuffix(file, ".lua") }

// fileState says whose a file is: none, vclu, edited or user.
func fileState(path string) string {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileNone
	}
	if err != nil {
		return fileUser
	}
	m := reHeader.FindSubmatchIndex(b)
	if m == nil {
		return fileUser
	}
	sum := sha256.Sum256(b[m[1]:])
	if hex.EncodeToString(sum[:]) != string(b[m[2]:m[3]]) {
		return fileEdited
	}
	return fileVCLU
}

// withHeader puts the header with the content's hash before it.
func withHeader(content string) string {
	sum := sha256.Sum256([]byte(content))
	return headerPrefix + hex.EncodeToString(sum[:]) + "\n" + content
}

// writeFile writes next to the file and renames, so a failed write leaves the old one.
func writeFile(path, content string) error {
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

// requireState says whether modules/init.lua loads the dashboards.
func (s *Server) requireState() bool {
	b, err := os.ReadFile(filepath.Join(s.serverDir, modulesDirName, "init.lua"))
	if err != nil {
		return false
	}
	code := blockComments(string(b))
	return reRequire.MatchString(code)
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

// flatGrenton is modules/dashboards/imported.lua, which require would load instead of the directory.
func (s *Server) flatGrenton() string {
	p := filepath.Join(s.serverDir, modulesDirName, "dashboards", "imported.lua")
	if _, err := os.Stat(p); err == nil {
		return modulesDirName + "/dashboards/imported.lua"
	}
	return ""
}

// The tab's view.
type dashWidget struct {
	Kind  string   `json:"kind"`
	Label string   `json:"label"`
	Reads []string `json:"reads"`
	Does  []string `json:"does"`
}

type dashPage struct {
	ID      string       `json:"id"`
	Label   string       `json:"label"`
	Widgets []dashWidget `json:"widgets"`
	LeftOut []string     `json:"leftOut"`
	File    string       `json:"file"`
	State   string       `json:"state"`
}

type dashInterface struct {
	ID    string     `json:"id"`
	Label string     `json:"label"`
	Pages []dashPage `json:"pages"`
}

// interfaces reads the stored interfaces, those whose IDs may name files.
func (s *Server) interfaces() ([]dashspec.Interface, error) {
	list, err := s.readInterfaces()
	if err != nil {
		return nil, err
	}
	out := list[:0]
	for _, in := range list {
		if !dashspec.ValidID(in.ID) {
			continue
		}
		pages := in.Pages[:0]
		for _, p := range in.Pages {
			if dashspec.ValidPageID(p.ID) {
				pages = append(pages, p)
			}
		}
		in.Pages = pages
		out = append(out, in)
	}
	return out, nil
}

// pageFiles are the page files in the directory, by name.
func (s *Server) pageFiles() []string {
	entries, _ := os.ReadDir(s.grentonDashPath())
	var files []string
	for _, e := range entries {
		if !e.IsDir() && rePageFile.MatchString(e.Name()) {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files
}

// handleAPIGrentonDashboards lists the imported interfaces' pages and their files.
//
//	@Summary		myGrenton pages
//	@Description	The pages of the myGrenton interfaces from the imported Object Manager project: per page its widgets (kind, label, what they read and do), what was left out and the state of its dashboard file (none, vclu, edited, user); page files of pages no longer imported (orphans); whether modules/init.lua loads the dashboards.
//	@Tags			grenton
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Security		ApiKeyAuth
//	@Router			/api/grenton/dashboards [get]
func (s *Server) handleAPIGrentonDashboards(c *gin.Context) {
	s.omImport.Lock()
	defer s.omImport.Unlock()
	list, err := s.interfaces()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	dir := s.grentonDashPath()
	known := map[string]bool{}
	out := []dashInterface{}
	for _, in := range list {
		di := dashInterface{ID: in.ID, Label: in.Label, Pages: []dashPage{}}
		for _, p := range in.Pages {
			left := p.LeftOut
			if left == nil {
				left = []string{}
			}
			dp := dashPage{ID: p.ID, Label: p.Label, Widgets: []dashWidget{}, LeftOut: left,
				File: pageFile(p.ID), State: fileState(filepath.Join(dir, pageFile(p.ID)))}
			for _, w := range p.Widgets {
				if w.Check() != nil {
					continue
				}
				reads, does := dashspec.Describe(w)
				dp.Widgets = append(dp.Widgets, dashWidget{Kind: w.Kind, Label: w.Label, Reads: nonNil(reads), Does: nonNil(does)})
			}
			known[dp.File] = true
			di.Pages = append(di.Pages, dp)
		}
		out = append(out, di)
	}
	orphans := []gin.H{}
	for _, f := range s.pageFiles() {
		if !known[f] {
			orphans = append(orphans, gin.H{"file": f, "state": fileState(filepath.Join(dir, f))})
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"interfaces": out,
		"orphans":    orphans,
		"dir":        modulesDirName + "/" + grentonDashDir,
		"required":   s.requireState(),
		"initState":  fileState(filepath.Join(dir, "init.lua")),
		"flatFile":   s.flatGrenton(),
	})
}

type dashCreate struct {
	Interface string   `json:"interface"`
	Pages     []string `json:"pages"`
	// Replace lets files vclu wrote and nobody changed be written again.
	Replace bool `json:"replace"`
	// AddRequire adds the require line to modules/init.lua when it is missing.
	AddRequire bool `json:"addRequire"`
}

// handleAPIGrentonDashboardsCreate writes the dashboard files of the chosen pages.
//
//	@Summary		Create dashboards from myGrenton pages
//	@Description	Writes modules/dashboards/imported/page_<id>.lua for each chosen page and init.lua loading every page file there. A file vclu did not write, or wrote and someone changed, is never replaced (kept); a file vclu wrote is replaced only with replace (ask). When init.lua is the user's, the pages are written and the lines to add are returned (initLines). With addRequire, require("dashboards/imported") is added to modules/init.lua when missing, or returned as requireLine when it would not compile there. Lua reloads when the dashboards are required; pages that failed to load are in pageErrors.
//	@Tags			grenton
//	@Accept			json
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Failure		400	{object}	web.APIError
//	@Failure		409	{object}	map[string]interface{}	"files to ask about (ask) or that stay the user's (kept)"
//	@Security		ApiKeyAuth
//	@Router			/api/grenton/dashboards [post]
func (s *Server) handleAPIGrentonDashboardsCreate(c *gin.Context) {
	var req dashCreate
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Pages) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "choose an interface and its pages"})
		return
	}
	s.omImport.Lock()
	defer s.omImport.Unlock()

	if f := s.flatGrenton(); f != "" {
		c.JSON(http.StatusConflict, gin.H{"error": f + " exists: require(\"dashboards/imported\") would load it instead of the directory. Rename or remove it first.", "flatFile": f})
		return
	}
	list, err := s.interfaces()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var in *dashspec.Interface
	for i := range list {
		if list[i].ID == req.Interface {
			in = &list[i]
		}
	}
	if in == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no such interface: import the Object Manager project first"})
		return
	}
	chosen := map[string]bool{}
	for _, id := range req.Pages {
		chosen[id] = true
	}

	dir := s.grentonDashPath()
	var pages []dashspec.Page
	var ask, kept []string
	for _, p := range in.Pages {
		if !chosen[p.ID] {
			continue
		}
		pages = append(pages, p)
		switch fileState(filepath.Join(dir, pageFile(p.ID))) {
		case fileVCLU:
			// the same file again is no change to ask about
			if b, err := os.ReadFile(filepath.Join(dir, pageFile(p.ID))); err != nil || string(b) != withHeader(dashspec.PageLua(*in, p)) {
				ask = append(ask, pageFile(p.ID))
			}
		case fileEdited, fileUser:
			kept = append(kept, pageFile(p.ID))
		}
	}
	if len(pages) != len(chosen) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a chosen page is not in the interface"})
		return
	}
	if len(kept) > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "these files are yours (not written by vclu, or changed since): they are not replaced", "kept": kept})
		return
	}
	if len(ask) > 0 && !req.Replace {
		c.JSON(http.StatusConflict, gin.H{"error": "these dashboards were created before: replace them?", "ask": ask})
		return
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var written []string
	for _, p := range pages {
		if err := writeFile(filepath.Join(dir, pageFile(p.ID)), withHeader(dashspec.PageLua(*in, p))); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "written": written})
			return
		}
		written = append(written, pageFile(p.ID))
	}

	// init.lua loads every page file there, of every interface: the interfaces' order, then the rest
	var modules []string
	seen := map[string]bool{}
	files := s.pageFiles()
	present := map[string]bool{}
	for _, f := range files {
		present[f] = true
	}
	for _, i := range list {
		for _, p := range i.Pages {
			if f := pageFile(p.ID); present[f] && !seen[f] {
				seen[f] = true
				modules = append(modules, pageModule(f))
			}
		}
	}
	for _, f := range files {
		if !seen[f] {
			modules = append(modules, pageModule(f))
		}
	}
	resp := gin.H{"success": true, "dir": modulesDirName + "/" + grentonDashDir, "dashboardIds": dashIDs(pages)}
	initPath := filepath.Join(dir, "init.lua")
	switch fileState(initPath) {
	case fileNone, fileVCLU:
		if err := writeFile(initPath, withHeader(dashspec.InitLua(modules))); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "written": written})
			return
		}
		written = append(written, "init.lua")
	default:
		// the user's: they add what is missing
		b, _ := os.ReadFile(initPath)
		code := luaCode(string(b))
		var lines []string
		for _, p := range pages {
			// the quoted name, so page_1 is not found in page_12
			if m := pageModule(pageFile(p.ID)); !strings.Contains(code, `"`+m+`"`) && !strings.Contains(code, `'`+m+`'`) {
				lines = append(lines, dashspec.LoadLine(m))
			}
		}
		resp["initKept"] = true
		resp["initLines"] = nonNil(lines)
	}
	resp["written"] = written

	added := false
	if req.AddRequire && !s.requireState() {
		ok, err := s.addRequire()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "written": written})
			return
		}
		if !ok {
			// it would not compile there (a return at the end): the user adds it
			resp["requireLine"] = grentonRequire
		}
		added = ok
	}
	resp["requireAdded"] = added
	resp["required"] = s.requireState()
	if s.requireState() {
		s.reloadForOM(resp)
		if errs := s.pageErrors(); len(errs) > 0 {
			resp["pageErrors"] = errs
		}
	}
	c.JSON(http.StatusOK, resp)
}

// pageErrors are the pages Wire.load could not load after the reload.
func (s *Server) pageErrors() []string {
	if s.luaExecutor == nil {
		return nil
	}
	out, err := s.luaExecutor.Execute(`return Wire.errorsJSON()`)
	if err != nil {
		return nil
	}
	var errs []string
	_ = json.Unmarshal([]byte(out), &errs)
	return errs
}

func dashIDs(pages []dashspec.Page) []string {
	ids := make([]string, len(pages))
	for i, p := range pages {
		ids[i] = dashspec.DashboardID(p)
	}
	return ids
}

// addRequire appends the require line to modules/init.lua, creating it when missing.
// It leaves the file as it is (false) when the line would not compile there.
func (s *Server) addRequire() (bool, error) {
	path := filepath.Join(s.serverDir, modulesDirName, "init.lua")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("modules/init.lua: %v", err)
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	b = append(b, []byte("\n-- Imported dashboards (Grenton page > Dashboards)\n"+grentonRequire+"\n")...)
	if _, err := parse.Parse(bytes.NewReader(b), "init.lua"); err != nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, fmt.Errorf("modules/init.lua: %v", err)
	}
	if err := writeFile(path, string(b)); err != nil {
		return false, fmt.Errorf("modules/init.lua: %v", err)
	}
	return true, nil
}
