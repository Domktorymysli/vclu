package web

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/dashspec"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/omchange"
	"github.com/Domktorymysli/vclu/pkg/omparser"
	"github.com/Domktorymysli/vclu/pkg/omproject"
	"github.com/gin-gonic/gin"
)

// handleAPIOMUpload handles OM file upload via API (.lua, diagnostic pack .zip, project .omp)
//
//	@Summary		Upload OM file
//	@Description	Accepts a single om.lua file, a diagnostic-pack ZIP containing CLU*/om.lua entries, or an Object Manager project (.omp, or a project ZIP with system.xml); the vCLU's own serial is skipped. Each CLU is parsed, stored as om_<serial>.lua, a proxy_<serial>.lua is generated and an entry is added to .vclu.json; then the Lua runtime reloads once (reloaded, reloadError, moduleErrors in the response). A project, also the one inside a diagnostic pack, is written for the agent to ai/om/ (event bindings, scripts); what could not be read is listed in warnings. The upload itself is not kept.
//	@Tags			grenton
//	@Accept			mpfd
//	@Produce		json
//	@Param			file	formData	file	true	"om.lua file, diagnostic-pack ZIP or Object Manager project (.omp)"
//	@Success		200		{object}	map[string]interface{}	"single file: success, filename, originalName, cluName, cluIP, objectCount, moduleCount, warnings; ZIP or project: success, isZip or isProject, originalName, imported, skipped, results, skippedList, errors, omVersion, agentDocs, warnings, brokenRefs (places in modules/ naming an object no CLU has); project also removed (CLUs not in it, taken out after an import without errors)"
//	@Failure		400		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/om/upload [post]
func (s *Server) handleAPIOMUpload(c *gin.Context) {
	// Get uploaded file
	header, err := c.FormFile("file")
	if c.Request.MultipartForm != nil {
		// An upload over the engine's MaxMultipartMemory is spooled to a
		// temporary file: remove it before answering.
		defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}
	file, err := header.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read file"})
		return
	}
	defer file.Close()

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read file"})
		return
	}

	s.omImport.Lock()
	defer s.omImport.Unlock()
	// what the import may break is looked for among the names before it too
	before := s.house()

	// An Object Manager project (.omp), or a ZIP: a diagnostic pack or a project
	lower := strings.ToLower(header.Filename)
	if strings.HasSuffix(lower, ".omp") || strings.HasSuffix(lower, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid ZIP file: %v", err)})
			return
		}
		if omproject.IsProject(zr) {
			s.handleProjectUpload(c, zr, header.Filename, before)
			return
		}
		s.handleZipUpload(c, content, header.Filename, before)
		return
	}

	// Handle single .lua file
	result, err := s.processOMFile(content, header.Filename)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result["brokenRefs"] = s.brokenRefs(before)
	s.reloadForOM(result)
	c.JSON(http.StatusOK, result)
}

// handleZipUpload extracts om.lua files from diagnostic pack ZIP
func (s *Server) handleZipUpload(c *gin.Context, content []byte, filename string, before omchange.House) {
	// Open ZIP from bytes
	zipReader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid ZIP file: %v", err)})
		return
	}

	ownSerial := s.ownSerial()

	// Regex to match CLU*/om.lua pattern
	cluOmPattern := regexp.MustCompile(`^CLU(\d+)/om\.lua$`)
	project, warnings := projectFromPack(zipReader)
	names := map[string]string{}
	if project != nil {
		names = project.Names()
	}

	var results []gin.H
	var errors []string
	var skipped []string

	for _, f := range zipReader.File {
		// Skip directories and non-matching files
		if f.FileInfo().IsDir() {
			continue
		}

		// Check if it matches CLU*/om.lua pattern
		matches := cluOmPattern.FindStringSubmatch(f.Name)
		if matches == nil {
			continue
		}

		// Skip vCLU's own serial
		serialStr := matches[1]
		serial, _ := strconv.ParseUint(serialStr, 10, 64)
		label := cluLabel(names, "CLU"+serialStr)
		if ownSerial > 0 && serial == ownSerial {
			skipped = append(skipped, label+" (this vCLU)")
			continue
		}

		fileContent, err := readZipFile(f)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to read om.lua: %v", label, err))
			continue
		}

		// Process this OM file
		result, err := s.processOMFile(fileContent, f.Name)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", label, err))
			continue
		}
		if e, _ := result["luaError"].(string); e != "" {
			errors = append(errors, label+": "+e)
		}
		warnings = append(warnings, resultWarnings(result)...)

		results = append(results, result)
	}

	if len(results) == 0 && len(errors) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "No CLU*/om.lua files found in ZIP archive",
		})
		return
	}

	var omVersion, agentDocs string
	if project != nil {
		omVersion = project.Version
		warnings = append(warnings, project.Warnings...)
		if err := project.WriteDocs(s.serverDir); err != nil {
			errors = append(errors, err.Error())
		} else {
			agentDocs = omproject.DocsDir
		}
		if err := dashspec.SaveInterfaces(s.myGrentonPath(), project.Interfaces); err != nil {
			errors = append(errors, err.Error())
		}
	} else {
		// The files would describe an earlier project, not the one these CLUs run.
		if err := omproject.RemoveDocs(s.serverDir); err != nil {
			errors = append(errors, err.Error())
		}
		if err := dashspec.RemoveInterfaces(s.myGrentonPath()); err != nil {
			errors = append(errors, err.Error())
		}
	}

	resp := gin.H{
		"success":      len(errors) == 0,
		"isZip":        true,
		"originalName": filename,
		"imported":     len(results),
		"skipped":      len(skipped),
		"results":      results,
		"skippedList":  skipped,
		"errors":       errors,
		"omVersion":    omVersion,
		"agentDocs":    agentDocs,
		"warnings":     warnings,
	}
	// Once for the whole pack
	resp["brokenRefs"] = s.brokenRefs(before)
	if len(results) > 0 {
		s.reloadForOM(resp)
	}
	c.JSON(http.StatusOK, resp)
}

// handleProjectUpload registers every CLU of an Object Manager project, as the
// diagnostic pack built from it would, and writes the project for the agent.
func (s *Server) handleProjectUpload(c *gin.Context, zr *zip.Reader, filename string, before omchange.House) {
	project, err := omproject.ReadZip(zr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ownSerial := s.ownSerial()
	results := []gin.H{}
	errors := []string{}
	skipped := []string{}
	var cluWarnings []string
	for _, clu := range project.CLUs {
		label := clu.Name + " (" + clu.ID + ")"
		if serial := extractSerialFromCLUID(clu.ID); ownSerial > 0 && serial == ownSerial {
			skipped = append(skipped, label+" (this vCLU)")
			continue
		}
		result, err := s.processOMFile([]byte(project.OMLua(clu)), clu.ID)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", label, err))
			continue
		}
		if e, _ := result["luaError"].(string); e != "" {
			errors = append(errors, label+": "+e)
		}
		cluWarnings = append(cluWarnings, resultWarnings(result)...)
		results = append(results, result)
	}
	// the project is the whole house: CLUs it no longer has go, as a delete would take them,
	// but only after an import without errors (a broken one must not empty vclu)
	removed := []string{}
	if len(errors) == 0 && len(results) > 0 {
		inProject := map[uint64]bool{}
		for _, clu := range project.CLUs {
			inProject[extractSerialFromCLUID(clu.ID)] = true
		}
		for _, f := range config.GetOMFiles(s.serverDir) {
			if inProject[f.Serial] || (ownSerial > 0 && f.Serial == ownSerial) {
				continue
			}
			if err := s.removeOMFile(f.Serial, f.Filename); err != nil {
				errors = append(errors, fmt.Sprintf("CLU%d is not in the project, removing it: %v", f.Serial, err))
				continue
			}
			s.forgetObjects(f.Serial)
			removed = append(removed, fmt.Sprintf("CLU%d", f.Serial))
		}
	}
	agentDocs := omproject.DocsDir
	if err := project.WriteDocs(s.serverDir); err != nil {
		errors = append(errors, err.Error())
		agentDocs = ""
	}
	if err := dashspec.SaveInterfaces(s.myGrentonPath(), project.Interfaces); err != nil {
		errors = append(errors, err.Error())
	}
	warnings := append(append([]string{}, project.Warnings...), cluWarnings...)
	resp := gin.H{
		"success":      len(errors) == 0,
		"isProject":    true,
		"originalName": filename,
		"omVersion":    project.Version,
		"imported":     len(results),
		"skipped":      len(skipped),
		"results":      results,
		"skippedList":  skipped,
		"removed":      removed,
		"errors":       errors,
		"agentDocs":    agentDocs,
		"warnings":     warnings,
	}
	// Once for the whole project
	resp["brokenRefs"] = s.brokenRefs(before)
	if len(results) > 0 {
		s.reloadForOM(resp)
	}
	c.JSON(http.StatusOK, resp)
}

// projectFromPack reads the Object Manager project a diagnostic pack carries.
// A pack can hold the project zip and a backup .omp; the one with the newest
// system.xml is what the CLUs got. Nil without a project.
func projectFromPack(pack *zip.Reader) (*omproject.Project, []string) {
	var newest *zip.Reader
	var newestName string
	var at time.Time
	for _, f := range pack.File {
		lower := strings.ToLower(f.Name)
		if strings.Contains(f.Name, "/") || !strings.HasSuffix(lower, ".zip") && !strings.HasSuffix(lower, ".omp") {
			continue
		}
		data, err := readZipFile(f)
		if err != nil {
			continue
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue
		}
		for _, pf := range zr.File {
			if pf.Name == "system.xml" && (newest == nil || pf.Modified.After(at)) {
				newest, newestName, at = zr, f.Name, pf.Modified
			}
		}
	}
	if newest == nil {
		return nil, nil
	}
	project, err := omproject.ReadZip(newest)
	if err != nil {
		return nil, []string{fmt.Sprintf("Project %s: %v", newestName, err)}
	}
	return project, nil
}

// ownSerial is this vCLU's serial, 0 before it has one.
func (s *Server) ownSerial() uint64 {
	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err == nil && masterConfig != nil && masterConfig.Device != nil {
		return masterConfig.Device.SerialNumber
	}
	return 0
}

// maxZipEntry caps what is unpacked from one file of an uploaded ZIP.
const maxZipEntry = 64 << 20

// readZipFile reads one file of a ZIP, up to maxZipEntry.
func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, maxZipEntry+1))
	if err == nil && len(b) > maxZipEntry {
		return nil, fmt.Errorf("over %d MB unpacked", maxZipEntry>>20)
	}
	return b, err
}

// resultWarnings are the warnings processOMFile gave a CLU.
func resultWarnings(result gin.H) []string {
	w, _ := result["warnings"].([]string)
	return w
}

// cluLabel is "Name (CLU…)" when the project names the CLU, else the bare ID.
func cluLabel(names map[string]string, id string) string {
	if name := names[id]; name != "" {
		return name + " (" + id + ")"
	}
	return id
}

// parseOMUpload is an uploaded om.lua as vCLU keeps it, parsed, or why it is not taken; the
// preview takes files by it too.
func parseOMUpload(content []byte) (text string, stripped int, parsed *omparser.ParsedOM, err error) {
	// Object Manager writes empty files for a CLU it could not read
	if len(bytes.TrimSpace(content)) == 0 {
		return "", 0, nil, fmt.Errorf("om.lua is empty, Object Manager did not download this CLU's configuration")
	}

	// A GATE's own objects are set up with the alarm panel's passwords, codes and key
	// (SAT0001:set(4, "...")): vCLU needs none of them, so neither the copy it keeps nor
	// anything after this sees those lines.
	text, stripped = omparser.StripGateSettings(string(content))

	// Parse OM file to validate it
	parsed, err = omparser.Parse(text)
	if err != nil {
		return "", 0, nil, fmt.Errorf("invalid OM file: %v", err)
	}

	// Extract serial from CLU ID (e.g., "CLU212345678" -> 212345678)
	if extractSerialFromCLUID(parsed.CLU.ID) == 0 {
		return "", 0, nil, fmt.Errorf("could not extract serial number from CLU ID")
	}
	return text, stripped, parsed, nil
}

// processOMFile validates and saves a single OM file
func (s *Server) processOMFile(content []byte, originalName string) (gin.H, error) {
	text, stripped, parsed, err := parseOMUpload(content)
	if err != nil {
		return nil, err
	}
	serial := extractSerialFromCLUID(parsed.CLU.ID)
	if stripped > 0 {
		log.Printf("[OM] %s: %d settings of the GATE's objects left out of the stored copy", parsed.CLU.ID, stripped)
	}
	warnings := []string{}
	for _, w := range parsed.Warnings {
		warnings = append(warnings, parsed.CLU.ID+": "+w)
	}

	// Ensure imports directory exists
	importsDir := config.GetOMFilesDir(s.serverDir)
	if err := os.MkdirAll(importsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create imports directory")
	}

	// Save file as om_<serial>.lua
	filename := fmt.Sprintf("om_%d.lua", serial)
	filePath := filepath.Join(importsDir, filename)
	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		return nil, fmt.Errorf("failed to save file")
	}

	// Generate proxy.lua
	proxyCode := omparser.GenerateProxy(parsed, omparser.DefaultConfig())
	proxyFilename := fmt.Sprintf("proxy_%d.lua", serial)
	proxyPath := filepath.Join(importsDir, proxyFilename)
	if err := os.WriteFile(proxyPath, []byte(proxyCode), 0644); err != nil {
		return nil, fmt.Errorf("failed to save proxy file")
	}

	// Without a reloader only the proxy runs; the caller reloads otherwise
	var luaErr string
	if s.luaReloader == nil && s.luaExecutor != nil {
		if _, err := s.luaExecutor.Execute(proxyCode); err != nil {
			// The files stay: the next start runs the proxy again
			log.Printf("[OM] %s: proxy failed in Lua: %v", parsed.CLU.ID, err)
			luaErr = fmt.Sprintf("objects not registered, Lua: %v", err)
		}
	}

	// Create config entry
	omFileConfig := config.OMFileConfig{
		Filename:    filename,
		Serial:      serial,
		CLUName:     parsed.CLU.Name,
		CLUIP:       omparser.IPToString(parsed.CLU.IP),
		ObjectCount: len(parsed.Objects),
		ModuleCount: len(parsed.Modules),
		UploadedAt:  time.Now().Format(time.RFC3339),
	}

	// Save to config
	if err := config.AddOMFile(s.serverDir, omFileConfig); err != nil {
		return nil, fmt.Errorf("failed to save config")
	}

	return gin.H{
		"success":      true,
		"filename":     filename,
		"originalName": originalName,
		"cluName":      parsed.CLU.Name,
		"cluIP":        omparser.IPToString(parsed.CLU.IP),
		"objectCount":  len(parsed.Objects),
		"moduleCount":  len(parsed.Modules),
		"luaError":     luaErr,
		"warnings":     warnings,
	}, nil
}

// handleAPIOMList returns list of OM files
//
//	@Summary		List OM files
//	@Description	Returns all imported OM files registered in .vclu.json with their serial, CLU name/IP and object/module counts.
//	@Tags			grenton
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"files: array of OM file entries"
//	@Security		ApiKeyAuth
//	@Router			/api/om/list [get]
func (s *Server) handleAPIOMList(c *gin.Context) {
	omFiles := config.GetOMFiles(s.serverDir)
	c.JSON(http.StatusOK, gin.H{
		"files": omFiles,
	})
}

// handleAPIOMDelete deletes an OM file
//
//	@Summary		Delete OM file
//	@Description	Deletes the stored OM file and its generated proxy file, removes the entry (and any stale sync object paths) from .vclu.json, and reloads the Lua runtime without the CLU's objects. brokenRefs: the places in modules/ that name an object no CLU has any more.
//	@Tags			grenton
//	@Produce		json
//	@Param			serial	path		integer	true	"CLU serial number"
//	@Success		200		{object}	web.APIOK
//	@Failure		400		{object}	web.APIError
//	@Failure		404		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/om/{serial} [delete]
func (s *Server) handleAPIOMDelete(c *gin.Context) {
	serialStr := c.Param("serial")
	serial, err := strconv.ParseUint(serialStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid serial number"})
		return
	}

	// one import or delete at a time: what is the last CLU is decided under the lock
	s.omImport.Lock()
	locked := true
	defer func() {
		if locked {
			s.omImport.Unlock()
		}
	}()

	// Find the file in config
	omFiles := config.GetOMFiles(s.serverDir)
	var filename string
	for _, f := range omFiles {
		if f.Serial == serial {
			filename = f.Filename
			break
		}
	}

	if filename == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	before := s.house()
	if err := s.removeOMFile(serial, filename); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{"success": true}
	// what the project import wrote about this CLU goes with it; with the last CLU, all of it
	// (this vCLU's own om file, if one is stored, is not a CLU of the house)
	last := true
	for _, f := range config.GetOMFiles(s.serverDir) {
		if own := s.ownSerial(); own == 0 || f.Serial != own {
			last = false
		}
	}
	if err := s.forgetCLU(fmt.Sprintf("CLU%d", serial), last); err != nil {
		log.Printf("[OM] CLU%d deleted, its project files not all removed: %v", serial, err)
		resp["cleanupError"] = err.Error()
	}
	resp["brokenRefs"] = s.brokenRefs(before)
	s.omImport.Unlock()
	locked = false
	s.forgetObjects(serial)
	s.reloadForOM(resp)
	c.JSON(http.StatusOK, resp)
}

// removeOMFile removes a CLU's om file, its proxy and its entry in the config with its sync objects.
func (s *Server) removeOMFile(serial uint64, filename string) error {
	importsDir := config.GetOMFilesDir(s.serverDir)
	if err := os.Remove(filepath.Join(importsDir, filename)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file")
	}
	// the proxy may not exist
	_ = os.Remove(filepath.Join(importsDir, fmt.Sprintf("proxy_%d.lua", serial)))
	if err := config.DeleteOMFileAndCleanSync(s.serverDir, serial); err != nil {
		return fmt.Errorf("failed to update config")
	}
	return nil
}

// forgetObjects takes a removed CLU's objects out of a runtime that is not reloaded from the files.
func (s *Server) forgetObjects(serial uint64) {
	if s.luaReloader == nil && s.luaExecutor != nil {
		_, _ = s.luaExecutor.Execute(fmt.Sprintf(`removeByPrefix("CLU%d.")`, serial))
	}
}

// handleAPIOMDetails returns details of a specific OM file
//
//	@Summary		OM file details
//	@Description	Re-reads and parses the stored OM file for the given CLU serial and returns its metadata together with the parsed CLU info, modules and objects.
//	@Tags			grenton
//	@Produce		json
//	@Param			serial	path		integer	true	"CLU serial number"
//	@Success		200		{object}	map[string]interface{}	"file, clu, modules, objects"
//	@Failure		400		{object}	web.APIError
//	@Failure		404		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/om/{serial} [get]
func (s *Server) handleAPIOMDetails(c *gin.Context) {
	serialStr := c.Param("serial")
	serial, err := strconv.ParseUint(serialStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid serial number"})
		return
	}

	// Find the file in config
	omFiles := config.GetOMFiles(s.serverDir)
	var omFile *config.OMFileConfig
	for _, f := range omFiles {
		if f.Serial == serial {
			omFile = &f
			break
		}
	}

	if omFile == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	// Read and parse the file
	filePath := filepath.Join(config.GetOMFilesDir(s.serverDir), omFile.Filename)
	content, err := os.ReadFile(filePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file"})
		return
	}

	parsed, err := omparser.Parse(string(content))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse file"})
		return
	}

	// Return detailed info
	c.JSON(http.StatusOK, gin.H{
		"file":    omFile,
		"clu":     parsed.CLU,
		"modules": parsed.Modules,
		"objects": parsed.Objects,
	})
}

// handleAPIOMRegenerate regenerates proxy files for all OM files
//
//	@Summary		Regenerate proxy files
//	@Description	Re-parses every imported OM file, rewrites its proxy_<serial>.lua and reloads the Lua runtime once (reloaded, reloadError, moduleErrors in the response). Per-file failures are collected and reported; the call always returns 200.
//	@Tags			grenton
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"success, regenerated, errors"
//	@Security		ApiKeyAuth
//	@Router			/api/om/regenerate [post]
func (s *Server) handleAPIOMRegenerate(c *gin.Context) {
	// it may rewrite om files, which imports and previews read
	s.omImport.Lock()
	defer s.omImport.Unlock()
	importsDir := config.GetOMFilesDir(s.serverDir)
	omFiles := config.GetOMFiles(s.serverDir)

	regenerated := 0
	errors := []string{}

	for _, omFile := range omFiles {
		// Read OM file
		filePath := filepath.Join(importsDir, omFile.Filename)
		content, err := os.ReadFile(filePath)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", omFile.Filename, err))
			continue
		}

		// A copy kept before vCLU removed the GATE's settings loses them now
		text, stripped := omparser.StripGateSettings(string(content))
		if stripped > 0 {
			if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
				errors = append(errors, fmt.Sprintf("%s: write error: %v", omFile.Filename, err))
				continue
			}
		}

		// Parse OM file
		parsed, err := omparser.Parse(text)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: parse error: %v", omFile.Filename, err))
			continue
		}

		// Generate proxy
		proxyCode := omparser.GenerateProxy(parsed, omparser.DefaultConfig())
		proxyFilename := fmt.Sprintf("proxy_%d.lua", omFile.Serial)
		proxyPath := filepath.Join(importsDir, proxyFilename)
		if err := os.WriteFile(proxyPath, []byte(proxyCode), 0644); err != nil {
			errors = append(errors, fmt.Sprintf("%s: write error: %v", proxyFilename, err))
			continue
		}

		if s.luaReloader == nil && s.luaExecutor != nil {
			if _, err := s.luaExecutor.Execute(proxyCode); err != nil {
				errors = append(errors, fmt.Sprintf("%s: lua error: %v", proxyFilename, err))
				// Continue anyway - file is saved
			}
		}

		regenerated++
	}

	resp := gin.H{
		"success":     len(errors) == 0,
		"regenerated": regenerated,
		"errors":      errors,
	}
	if regenerated > 0 {
		s.reloadForOM(resp)
	}
	c.JSON(http.StatusOK, resp)
}

// moduleLoadReporter reports the user modules the last reload loaded.
type moduleLoadReporter interface {
	LastModuleLoad() []lua.ModuleLoadResult
}

// proxyLoadReporter reports the OM proxy files that failed in the last reload.
type proxyLoadReporter interface {
	LastProxyLoad() []string
}

// reloadForOM reloads the Lua runtime after an OM change, so subscriptions, expose() and
// dashboards take the new proxies (#226), and adds the outcome to resp: "reloaded", and
// "reloadError", "proxyErrors" (success false: the CLU's objects are missing) or
// "moduleErrors" when it went wrong.
func (s *Server) reloadForOM(resp gin.H) {
	if s.luaReloader == nil {
		return
	}
	if err := s.luaReloader.Reload(); err != nil {
		resp["reloaded"] = false
		resp["reloadError"] = err.Error()
		return
	}
	resp["reloaded"] = true
	if r, ok := s.luaReloader.(proxyLoadReporter); ok {
		if errs := r.LastProxyLoad(); len(errs) > 0 {
			resp["proxyErrors"] = errs
			resp["success"] = false
		}
	}
	if r, ok := s.luaReloader.(moduleLoadReporter); ok {
		var errs []string
		for _, m := range r.LastModuleLoad() {
			if !m.OK {
				errs = append(errs, fmt.Sprintf("modules/%s: %s", m.Name, m.Error))
			}
		}
		if len(errs) > 0 {
			resp["moduleErrors"] = errs
		}
	}
}

// extractSerialFromCLUID extracts the numeric serial from a CLU ID like "CLU212345678"
func extractSerialFromCLUID(cluID string) uint64 {
	// Remove "CLU" prefix
	if len(cluID) < 4 {
		return 0
	}
	numStr := cluID[3:]
	serial, err := strconv.ParseUint(numStr, 10, 64)
	if err != nil {
		return 0
	}
	return serial
}
