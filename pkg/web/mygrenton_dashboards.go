package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/dashfiles"
	"github.com/Domktorymysli/vclu/pkg/dashspec"
)

// The Grenton page's Dashboards tab (#220): the pages of the stored myGrenton interfaces
// and the dashboard files made from them (dashfiles), written only when the user asks.

// The tab's view.
type dashWidget struct {
	Kind  string   `json:"kind"`
	Label string   `json:"label"`
	Reads []string `json:"reads"`
	Does  []string `json:"does"`
	// CLUs it names that are not imported (deleted since): the widget shows nothing there
	Missing []string `json:"missing"`
}

// missingCLUs are the CLUs a widget reads or commands that are not among the imported ones.
func missingCLUs(w dashspec.Widget, imported map[string]bool) []string {
	var out []string
	for _, clu := range w.CLUs() {
		if !imported[clu] {
			out = append(out, clu)
		}
	}
	return out
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
	list, err := dashspec.LoadInterfaces(s.myGrentonPath())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	files := s.dashFiles()
	known := map[string]bool{}
	imported := map[string]bool{}
	for _, f := range config.GetOMFiles(s.serverDir) {
		imported[fmt.Sprintf("CLU%d", f.Serial)] = true
	}
	// this vCLU's own objects are local, never an imported file
	if own := s.ownSerial(); own != 0 {
		imported[fmt.Sprintf("CLU%d", own)] = true
	}
	out := []dashInterface{}
	for _, in := range list {
		di := dashInterface{ID: in.ID, Label: in.Label, Pages: []dashPage{}}
		for _, p := range in.Pages {
			left := p.LeftOut
			if left == nil {
				left = []string{}
			}
			dp := dashPage{ID: p.ID, Label: p.Label, Widgets: []dashWidget{}, LeftOut: left,
				File: dashfiles.PageFile(p.ID), State: string(files.State(dashfiles.PageFile(p.ID)))}
			for _, w := range p.Widgets {
				if w.Check() != nil {
					continue
				}
				reads, does := dashspec.Describe(w)
				dp.Widgets = append(dp.Widgets, dashWidget{Kind: w.Kind, Label: w.Label, Reads: nonNil(reads), Does: nonNil(does),
					Missing: nonNil(missingCLUs(w, imported))})
			}
			known[dp.File] = true
			di.Pages = append(di.Pages, dp)
		}
		out = append(out, di)
	}
	orphans := []gin.H{}
	for _, f := range files.PageFiles() {
		if !known[f] {
			orphans = append(orphans, gin.H{"file": f, "state": files.State(f)})
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"interfaces": out,
		"orphans":    orphans,
		"dir":        modulesDirName + "/" + dashfiles.Dir,
		"required":   files.Required(),
		"initState":  files.State("init.lua"),
		"flatFile":   flatFile(files),
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

	files := s.dashFiles()
	if f := flatFile(files); f != "" {
		c.JSON(http.StatusConflict, gin.H{"error": f + " exists: " + dashfiles.Require + " would load it instead of the directory. Rename or remove it first.", "flatFile": f})
		return
	}
	list, err := dashspec.LoadInterfaces(s.myGrentonPath())
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

	var pages []dashspec.Page
	var toWrite []dashfiles.Page
	var pageNames []string
	for _, p := range in.Pages {
		if chosen[p.ID] {
			pages = append(pages, p)
			toWrite = append(toWrite, dashfiles.Page{File: dashfiles.PageFile(p.ID), Content: dashspec.PageLua(*in, p)})
			pageNames = append(pageNames, dashfiles.PageFile(p.ID))
		}
	}
	if len(pages) != len(chosen) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a chosen page is not in the interface"})
		return
	}
	ask, kept := files.Conflicts(toWrite)
	if len(kept) > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "these files are yours (not written by vclu, or changed since): they are not replaced", "kept": kept})
		return
	}
	if len(ask) > 0 && !req.Replace {
		c.JSON(http.StatusConflict, gin.H{"error": "these dashboards were created before: replace them?", "ask": ask})
		return
	}

	written, err := files.WritePages(toWrite)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "written": written})
		return
	}
	// init.lua loads every page file there, of every interface: the interfaces' order, then the rest
	var order []string
	for _, i := range list {
		for _, p := range i.Pages {
			order = append(order, dashfiles.PageFile(p.ID))
		}
	}
	resp := gin.H{"success": true, "dir": modulesDirName + "/" + dashfiles.Dir, "dashboardIds": dashIDs(pages)}
	initWritten, lines, err := files.WriteInit(files.Modules(order), pageNames)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "written": written})
		return
	}
	if initWritten {
		written = append(written, "init.lua")
	} else {
		// the user's: they add what is missing
		resp["initKept"] = true
		resp["initLines"] = nonNil(lines)
	}
	resp["written"] = written

	added := false
	if req.AddRequire && !files.Required() {
		ok, err := files.AddRequire()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "written": written})
			return
		}
		if !ok {
			// it would not compile there (a return at the end): the user adds it
			resp["requireLine"] = dashfiles.Require
		}
		added = ok
	}
	resp["requireAdded"] = added
	resp["required"] = files.Required()
	if files.Required() {
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

// flatFile is modules/dashboards/imported.lua when it exists: require would load it instead of the directory.
func flatFile(files dashfiles.Files) string {
	if files.Flat() {
		return modulesDirName + "/dashboards/imported.lua"
	}
	return ""
}
