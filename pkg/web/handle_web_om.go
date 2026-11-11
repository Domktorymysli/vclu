package web

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/omparser"
	"github.com/Domktorymysli/vclu/pkg/omproject"
	"github.com/gin-gonic/gin"
)

// handleAPIOMUpload handles OM file upload via API (.lua, diagnostic pack .zip, project .omp)
//
//	@Summary		Upload OM file
//	@Description	Accepts a single om.lua file, a diagnostic-pack ZIP containing CLU*/om.lua entries, or an Object Manager project (.omp, or a project ZIP with system.xml); the vCLU's own serial is skipped. Each CLU is parsed, stored as om_<serial>.lua, a proxy_<serial>.lua is generated and executed in the Lua runtime to register the remote devices, and an entry is added to .vclu.json. A project, also the one inside a diagnostic pack, is written for the agent to ai/om/ (event bindings, scripts); what could not be read is listed in warnings. The upload itself is not kept.
//	@Tags			grenton
//	@Accept			mpfd
//	@Produce		json
//	@Param			file	formData	file	true	"om.lua file, diagnostic-pack ZIP or Object Manager project (.omp)"
//	@Success		200		{object}	map[string]interface{}	"single file: success, filename, originalName, cluName, cluIP, objectCount, moduleCount; ZIP or project: success, isZip or isProject, originalName, imported, skipped, results, skippedList, errors, omVersion, agentDocs, warnings"
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

	// An Object Manager project (.omp), or a ZIP: a diagnostic pack or a project
	lower := strings.ToLower(header.Filename)
	if strings.HasSuffix(lower, ".omp") || strings.HasSuffix(lower, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid ZIP file: %v", err)})
			return
		}
		if omproject.IsProject(zr) {
			s.handleProjectUpload(c, zr, header.Filename)
			return
		}
		s.handleZipUpload(c, content, header.Filename)
		return
	}

	// Handle single .lua file
	result, err := s.processOMFile(content, header.Filename)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// handleZipUpload extracts om.lua files from diagnostic pack ZIP
func (s *Server) handleZipUpload(c *gin.Context, content []byte, filename string) {
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
		if err := s.writeProjectDocs(project); err != nil {
			errors = append(errors, err.Error())
		} else {
			agentDocs = omproject.DocsDir
		}
	} else if err := s.removeProjectDocs(); err != nil {
		// The files would describe an earlier project, not the one these CLUs run.
		errors = append(errors, err.Error())
	}

	c.JSON(http.StatusOK, gin.H{
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
	})
}

// handleProjectUpload registers every CLU of an Object Manager project, as the
// diagnostic pack built from it would, and writes the project for the agent.
func (s *Server) handleProjectUpload(c *gin.Context, zr *zip.Reader, filename string) {
	project, err := omproject.ReadZip(zr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ownSerial := s.ownSerial()
	results := []gin.H{}
	errors := []string{}
	skipped := []string{}
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
		results = append(results, result)
	}
	agentDocs := omproject.DocsDir
	if err := s.writeProjectDocs(project); err != nil {
		errors = append(errors, err.Error())
		agentDocs = ""
	}
	warnings := project.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success":      len(errors) == 0,
		"isProject":    true,
		"originalName": filename,
		"omVersion":    project.Version,
		"imported":     len(results),
		"skipped":      len(skipped),
		"results":      results,
		"skippedList":  skipped,
		"errors":       errors,
		"agentDocs":    agentDocs,
		"warnings":     warnings,
	})
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
		if strings.Contains(f.Name, "/") || !(strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".omp")) {
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

// writeProjectDocs replaces the agent's files about the project in ai/om/. They
// are written next to it first, so a failed import leaves the old ones whole.
func (s *Server) writeProjectDocs(p *omproject.Project) error {
	dir := filepath.Join(s.serverDir, filepath.FromSlash(omproject.DocsDir))
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return fmt.Errorf("failed to create %s: %v", omproject.DocsDir, err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".om-")
	if err != nil {
		return fmt.Errorf("failed to create %s: %v", omproject.DocsDir, err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := os.Chmod(tmp, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %v", omproject.DocsDir, err)
	}
	for name, content := range p.Docs() {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write %s/%s: %v", omproject.DocsDir, name, err)
		}
	}
	if err := s.removeProjectDocs(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fmt.Errorf("failed to replace %s: %v", omproject.DocsDir, err)
	}
	return nil
}

// removeProjectDocs removes the agent's files about the project.
func (s *Server) removeProjectDocs() error {
	dir := filepath.Join(s.serverDir, filepath.FromSlash(omproject.DocsDir))
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("failed to replace %s: %v", omproject.DocsDir, err)
	}
	return nil
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

// cluLabel is "Name (CLU…)" when the project names the CLU, else the bare ID.
func cluLabel(names map[string]string, id string) string {
	if name := names[id]; name != "" {
		return name + " (" + id + ")"
	}
	return id
}

// processOMFile validates and saves a single OM file
func (s *Server) processOMFile(content []byte, originalName string) (gin.H, error) {
	// Object Manager writes empty files for a CLU it could not read
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, fmt.Errorf("om.lua is empty, Object Manager did not download this CLU's configuration")
	}

	// Parse OM file to validate it
	parsed, err := omparser.Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("invalid OM file: %v", err)
	}

	// Extract serial from CLU ID (e.g., "CLU212345678" -> 212345678)
	serial := extractSerialFromCLUID(parsed.CLU.ID)
	if serial == 0 {
		return nil, fmt.Errorf("could not extract serial number from CLU ID")
	}

	// Ensure imports directory exists
	importsDir := config.GetOMFilesDir(s.serverDir)
	if err := os.MkdirAll(importsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create imports directory")
	}

	// Save file as om_<serial>.lua
	filename := fmt.Sprintf("om_%d.lua", serial)
	filePath := filepath.Join(importsDir, filename)
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		return nil, fmt.Errorf("failed to save file")
	}

	// Generate proxy.lua
	proxyCode := omparser.GenerateProxy(parsed, omparser.DefaultConfig())
	proxyFilename := fmt.Sprintf("proxy_%d.lua", serial)
	proxyPath := filepath.Join(importsDir, proxyFilename)
	if err := os.WriteFile(proxyPath, []byte(proxyCode), 0644); err != nil {
		return nil, fmt.Errorf("failed to save proxy file")
	}

	// Execute proxy in Lua runtime to register devices
	if s.luaExecutor != nil {
		if _, err := s.luaExecutor.Execute(proxyCode); err != nil {
			// Log error but don't fail the upload - device will be registered on restart
			fmt.Printf("Warning: Failed to execute proxy in Lua: %v\n", err)
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
//	@Description	Deletes the stored OM file and its generated proxy file, removes the entry (and any stale sync object paths) from .vclu.json, and unregisters the CLU's objects from the Lua registry.
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

	// Delete the OM file
	importsDir := config.GetOMFilesDir(s.serverDir)
	filePath := filepath.Join(importsDir, filename)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete file"})
		return
	}

	// Delete the proxy file
	proxyPath := filepath.Join(importsDir, fmt.Sprintf("proxy_%d.lua", serial))
	os.Remove(proxyPath) // ignore error — proxy may not exist

	// Remove from config and clean sync objects
	if err := config.DeleteOMFileAndCleanSync(s.serverDir, serial); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update config"})
		return
	}

	// Remove objects from Lua registry
	if s.luaExecutor != nil {
		script := fmt.Sprintf(`removeByPrefix("CLU%d.")`, serial)
		s.luaExecutor.Execute(script)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
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
//	@Description	Re-parses every imported OM file, rewrites its proxy_<serial>.lua and executes the proxy in the Lua runtime to re-register the remote devices. Per-file failures are collected and reported; the call always returns 200.
//	@Tags			grenton
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"success, regenerated, errors"
//	@Security		ApiKeyAuth
//	@Router			/api/om/regenerate [post]
func (s *Server) handleAPIOMRegenerate(c *gin.Context) {
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

		// Parse OM file
		parsed, err := omparser.Parse(string(content))
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

		// Execute in Lua runtime
		if s.luaExecutor != nil {
			if _, err := s.luaExecutor.Execute(proxyCode); err != nil {
				errors = append(errors, fmt.Sprintf("%s: lua error: %v", proxyFilename, err))
				// Continue anyway - file is saved
			}
		}

		regenerated++
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     len(errors) == 0,
		"regenerated": regenerated,
		"errors":      errors,
	})
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
