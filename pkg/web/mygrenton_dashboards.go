package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/luastr"
	"github.com/Domktorymysli/vclu/pkg/omproject"
)

// Dashboards from the myGrenton interface (#220): one file per page in
// modules/dashboards/grenton/, written only when the user asks, theirs from then on.

const (
	grentonDashDir = "dashboards/grenton" // under modules/
	grentonRequire = `require("dashboards/grenton")`
	// headerPrefix starts a file vclu wrote; the hash is of what follows the header line
	headerPrefix = "-- vclu: written from the myGrenton interface, sha256:"
)

var (
	reHeader = regexp.MustCompile(`^-- vclu: written from the myGrenton interface, sha256:([0-9a-f]{64})\n`)
	reIdentK = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	rePageID = regexp.MustCompile(`^[0-9]{1,19}$`)
	// the require line, not commented out
	reRequire = regexp.MustCompile(`(?m)^[ \t]*require[ \t]*\(?[ \t]*["']dashboards[/.]grenton["'][ \t]*\)?`)
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

// fileState says who a file is: none, vclu, edited or user.
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

// luaValue writes a checked interface value as a Lua literal: strings through
// luastr.Quote, numbers as numbers, tables with identifier keys.
func luaValue(b *strings.Builder, v any) {
	switch v := v.(type) {
	case string:
		b.WriteString(luastr.Quote(v))
	case float64:
		b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	case bool:
		b.WriteString(strconv.FormatBool(v))
	case []any:
		b.WriteString("{")
		for i, x := range v {
			if i > 0 {
				b.WriteString(", ")
			}
			luaValue(b, x)
		}
		b.WriteString("}")
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			if reIdentK.MatchString(k) {
				keys = append(keys, k)
			}
		}
		// type and id first, as one reads a widget; then A-Z
		rank := func(k string) int {
			switch k {
			case "type":
				return 0
			case "id":
				return 1
			case "callType":
				return 2
			}
			return 3
		}
		sort.Slice(keys, func(i, j int) bool {
			if rank(keys[i]) != rank(keys[j]) {
				return rank(keys[i]) < rank(keys[j])
			}
			return keys[i] < keys[j]
		})
		b.WriteString("{ ")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(k)
			b.WriteString(" = ")
			luaValue(b, v[k])
		}
		b.WriteString(" }")
	default:
		b.WriteString("nil")
	}
}

// pageLua is the dashboard file of one page.
func pageLua(in omproject.Interface, p omproject.Page) string {
	var b strings.Builder
	b.WriteString("-- Dashboard from the myGrenton page " + strconv.Quote(p.Label) + " of " + strconv.Quote(in.Label) + ".\n")
	b.WriteString("-- This file is yours: change, reorder or remove widgets, add your own. vclu does not\n")
	b.WriteString("-- replace it once it is changed. MyGrenton.widget: docs.vclu.pl, Grenton > myGrenton.\n")
	b.WriteString("local s = Dashboard.new(" + luastr.Quote("grenton_page_"+p.ID) + ")\n")
	b.WriteString("    :name(" + luastr.Quote(p.Label) + ")\n")
	b.WriteString("    :tabGroup(" + luastr.Quote(in.Label) + ")\n")
	b.WriteString("    :section(" + luastr.Quote(p.Label) + ")\n")
	for _, w := range p.Widgets {
		b.WriteString("MyGrenton.widget(s, ")
		luaValue(&b, map[string]any(w))
		b.WriteString(")\n")
	}
	b.WriteString("s:build()\n")
	return b.String()
}

// initLua requires the pages, in the interface's order.
func initLua(pages []string) string {
	var b strings.Builder
	b.WriteString("-- The myGrenton dashboards; remove a line to drop a page.\n")
	for _, f := range pages {
		b.WriteString("require(" + luastr.Quote(grentonDashDir+"/"+strings.TrimSuffix(f, ".lua")) + ")\n")
	}
	return b.String()
}

// The tab's view of one page.
type dashPage struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Widgets int      `json:"widgets"`
	LeftOut []string `json:"leftOut"`
	File    string   `json:"file"`
	State   string   `json:"state"`
}

type dashInterface struct {
	ID    string     `json:"id"`
	Label string     `json:"label"`
	Pages []dashPage `json:"pages"`
}

// requireState says whether modules/init.lua loads the dashboards.
func (s *Server) requireState() bool {
	b, err := os.ReadFile(filepath.Join(s.serverDir, modulesDirName, "init.lua"))
	return err == nil && reRequire.Match(b)
}

// flatGrenton is modules/dashboards/grenton.lua, which require would load instead of the directory.
func (s *Server) flatGrenton() string {
	p := filepath.Join(s.serverDir, modulesDirName, "dashboards", "grenton.lua")
	if _, err := os.Stat(p); err == nil {
		return modulesDirName + "/dashboards/grenton.lua"
	}
	return ""
}

// handleAPIGrentonDashboards lists the imported interfaces' pages and their files.
//
//	@Summary		myGrenton pages
//	@Description	The pages of the myGrenton interfaces from the imported Object Manager project, with their widget counts, what was left out and the state of their dashboard file (none, vclu, edited, user).
//	@Tags			grenton
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Security		ApiKeyAuth
//	@Router			/api/grenton/dashboards [get]
func (s *Server) handleAPIGrentonDashboards(c *gin.Context) {
	list, err := s.readInterfaces()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	dir := s.grentonDashPath()
	out := []dashInterface{}
	for _, in := range list {
		di := dashInterface{ID: in.ID, Label: in.Label, Pages: []dashPage{}}
		for _, p := range in.Pages {
			left := p.LeftOut
			if left == nil {
				left = []string{}
			}
			di.Pages = append(di.Pages, dashPage{ID: p.ID, Label: p.Label, Widgets: len(p.Widgets), LeftOut: left,
				File: pageFile(p.ID), State: fileState(filepath.Join(dir, pageFile(p.ID)))})
		}
		out = append(out, di)
	}
	c.JSON(http.StatusOK, gin.H{
		"interfaces": out,
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
//	@Description	Writes modules/dashboards/grenton/page_<id>.lua for each chosen page and init.lua requiring them. A file vclu did not write, or wrote and someone changed, is never replaced; a file vclu wrote is replaced only with replace. With addRequire, require("dashboards/grenton") is added to modules/init.lua when missing. Lua reloads when the dashboards are required.
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
		c.JSON(http.StatusConflict, gin.H{"error": f + " exists: require(\"dashboards/grenton\") would load it instead of the directory. Rename or remove it first.", "flatFile": f})
		return
	}
	list, err := s.readInterfaces()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var in *omproject.Interface
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
		if !rePageID.MatchString(id) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad page id"})
			return
		}
		chosen[id] = true
	}

	dir := s.grentonDashPath()
	var pages []omproject.Page
	var ask, kept []string
	for _, p := range in.Pages {
		if !chosen[p.ID] {
			continue
		}
		pages = append(pages, p)
		switch fileState(filepath.Join(dir, pageFile(p.ID))) {
		case fileVCLU:
			ask = append(ask, pageFile(p.ID))
		case fileEdited, fileUser:
			kept = append(kept, pageFile(p.ID))
		}
	}
	if len(pages) != len(chosen) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a chosen page is not in the interface"})
		return
	}
	initPath := filepath.Join(dir, "init.lua")
	initState := fileState(initPath)
	if initState == fileEdited || initState == fileUser {
		kept = append(kept, "init.lua")
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
		if err := os.WriteFile(filepath.Join(dir, pageFile(p.ID)), []byte(withHeader(pageLua(*in, p))), 0644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "written": written})
			return
		}
		written = append(written, pageFile(p.ID))
	}
	// init.lua requires every page vclu has a file for, not only the ones written now
	var all []string
	for _, p := range in.Pages {
		if st := fileState(filepath.Join(dir, pageFile(p.ID))); st != fileNone {
			all = append(all, pageFile(p.ID))
		}
	}
	if err := os.WriteFile(initPath, []byte(withHeader(initLua(all))), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "written": written})
		return
	}
	written = append(written, "init.lua")

	added := false
	if req.AddRequire && !s.requireState() {
		if err := s.addRequire(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "written": written})
			return
		}
		added = true
	}
	resp := gin.H{
		"success":       true,
		"written":       written,
		"dir":           modulesDirName + "/" + grentonDashDir,
		"requireAdded":  added,
		"required":      s.requireState(),
		"dashboardsIds": dashIDs(pages),
	}
	if s.requireState() {
		s.reloadForOM(resp)
	}
	c.JSON(http.StatusOK, resp)
}

func dashIDs(pages []omproject.Page) []string {
	ids := make([]string, len(pages))
	for i, p := range pages {
		ids[i] = "grenton_page_" + p.ID
	}
	return ids
}

// addRequire appends the require line to modules/init.lua, creating it when missing.
func (s *Server) addRequire() error {
	path := filepath.Join(s.serverDir, modulesDirName, "init.lua")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("modules/init.lua: %v", err)
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	b = append(b, []byte("\n-- myGrenton dashboards (Grenton page > Dashboards)\n"+grentonRequire+"\n")...)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("modules/init.lua: %v", err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		return fmt.Errorf("modules/init.lua: %v", err)
	}
	return nil
}
