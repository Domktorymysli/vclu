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
	"github.com/gin-gonic/gin"
)

// handleAPIOMUpload handles OM file upload via API (supports both .lua and .zip)
//
//	@Summary		Upload OM file
//	@Description	Accepts a single om.lua file or a diagnostic-pack ZIP containing CLU*/om.lua entries (the vCLU's own serial is skipped). Each file is parsed, stored as om_<serial>.lua, a proxy_<serial>.lua is generated and executed in the Lua runtime to register the remote devices, and an entry is added to .vclu.json.
//	@Tags			grenton
//	@Accept			mpfd
//	@Produce		json
//	@Param			file	formData	file	true	"om.lua file or diagnostic-pack ZIP"
//	@Success		200		{object}	map[string]interface{}	"single file: success, filename, originalName, cluName, cluIP, objectCount, moduleCount; ZIP: success, isZip, originalName, imported, skipped, results, skippedList, errors"
//	@Failure		400		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/om/upload [post]
func (s *Server) handleAPIOMUpload(c *gin.Context) {
	// Get uploaded file
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}
	defer file.Close()

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read file"})
		return
	}

	// Check if it's a ZIP file (diagnostic pack)
	if strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
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

	// Get vCLU's own serial number to skip it
	var ownSerial uint64
	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err == nil && masterConfig != nil && masterConfig.Device != nil {
		ownSerial = masterConfig.Device.SerialNumber
	}

	// Regex to match CLU*/om.lua pattern
	cluOmPattern := regexp.MustCompile(`^CLU(\d+)/om\.lua$`)
	names := cluNamesFromProject(zipReader)

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

		// Read file content
		rc, err := f.Open()
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to open om.lua", label))
			continue
		}

		fileContent, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to read om.lua", label))
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

	c.JSON(http.StatusOK, gin.H{
		"success":      len(errors) == 0,
		"isZip":        true,
		"originalName": filename,
		"imported":     len(results),
		"skipped":      len(skipped),
		"results":      results,
		"skippedList":  skipped,
		"errors":       errors,
	})
}

// Project CLU entry in system.xml: <name>Dom1</name> <ipAddress>…</ipAddress> <nameOnCLU>CLU220000000</nameOnCLU>
var reProjectCLU = regexp.MustCompile(`<name>([^<]*)</name>\s*(?:<ipAddress>[^<]*</ipAddress>\s*)?<nameOnCLU>(CLU\d+)</nameOnCLU>`)

// cluNamesFromProject maps CLU IDs to their project names, read from the Object
// Manager project zip that diagnostic packs carry (any nested zip with system.xml).
func cluNamesFromProject(pack *zip.Reader) map[string]string {
	names := map[string]string{}
	for _, f := range pack.File {
		if strings.Contains(f.Name, "/") || !strings.HasSuffix(strings.ToLower(f.Name), ".zip") {
			continue
		}
		data, err := readZipFile(f)
		if err != nil {
			continue
		}
		project, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue
		}
		for _, pf := range project.File {
			if pf.Name != "system.xml" {
				continue
			}
			xml, err := readZipFile(pf)
			if err != nil {
				break
			}
			for _, m := range reProjectCLU.FindAllSubmatch(xml, -1) {
				names[string(m[2])] = string(m[1])
			}
			return names
		}
	}
	return names
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
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
