package web

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/mcp"
	"github.com/gin-gonic/gin"
)

// System files (read-only)
var systemEditorFiles = []string{
	"main.lua",
	"user.lua",
	"om.lua",
}

// Allowed extensions for user files
var allowedExtensions = []string{".lua", ".css"}

// Max file size (100KB)
const maxFileSize = 100 * 1024

// Modules directory name
const modulesDirName = "modules"

// Plugins directory name
const pluginsDirName = "plugins"

// handleEditor shows the Lua editor page
func (s *Server) handleEditor(c *gin.Context) {
	trans := s.getTranslations(c)

	data := gin.H{
		"title":           "Lua Editor",
		"currentPage":     "editor",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"lang":            trans["lang"],
		"t":               trans["t"],
		"langs":           trans["langs"],
	}

	s.renderHTML(c, http.StatusOK, "editor.html", data)
}

// handleAPIEditorFiles returns list of available Lua files (legacy endpoint)
func (s *Server) handleAPIEditorFiles(c *gin.Context) {
	cluDir := filepath.Join(s.serverDir, "a")

	var files []gin.H
	for _, filename := range systemEditorFiles {
		filePath := filepath.Join(cluDir, filename)
		info, err := os.Stat(filePath)
		if err == nil {
			files = append(files, gin.H{
				"name": filename,
				"size": info.Size(),
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"files": files,
	})
}

// FileTreeNode represents a file or directory in the tree
type FileTreeNode struct {
	Name     string          `json:"name"`
	Path     string          `json:"path,omitempty"`
	Type     string          `json:"type"` // "file" or "dir"
	Size     int64           `json:"size,omitempty"`
	Readonly bool            `json:"readonly,omitempty"`
	Children []*FileTreeNode `json:"children,omitempty"`
}

// handleAPIEditorTree returns file tree structure
func (s *Server) handleAPIEditorTree(c *gin.Context) {
	cluDir := filepath.Join(s.serverDir, "a")
	// Modules are stored outside a/ directory to avoid Grenton detection
	modulesDir := filepath.Join(s.serverDir, modulesDirName)

	// Ensure modules directory exists with default init.lua
	s.ensureModulesDir(modulesDir)

	// Build system files list
	var systemFiles []gin.H
	for _, filename := range systemEditorFiles {
		filePath := filepath.Join(cluDir, filename)
		info, err := os.Stat(filePath)
		if err == nil {
			systemFiles = append(systemFiles, gin.H{
				"name":     filename,
				"path":     filename,
				"type":     "file",
				"size":     info.Size(),
				"readonly": true,
			})
		}
	}

	// Build modules tree recursively
	modulesTree := s.buildFileTree(modulesDir, modulesDirName)

	// Build proxy files list (from imports/ directory)
	var proxyFiles []gin.H
	importsDir := config.GetOMFilesDir(s.serverDir)
	if entries, err := os.ReadDir(importsDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "proxy_") && strings.HasSuffix(entry.Name(), ".lua") {
				info, _ := entry.Info()
				size := int64(0)
				if info != nil {
					size = info.Size()
				}
				proxyFiles = append(proxyFiles, gin.H{
					"name":     entry.Name(),
					"path":     "imports/" + entry.Name(),
					"type":     "file",
					"size":     size,
					"readonly": true,
				})
			}
		}
	}

	// Build plugins tree (read-only, reuses buildFileTree)
	var pluginsTree *FileTreeNode
	pluginsDir := filepath.Join(s.serverDir, pluginsDirName)
	if _, err := os.Stat(pluginsDir); err == nil {
		pluginsTree = s.buildFileTree(pluginsDir, pluginsDirName)
		// Mark all files as readonly
		markTreeReadonly(pluginsTree)
	}

	c.JSON(http.StatusOK, gin.H{
		"system":  systemFiles,
		"modules": modulesTree,
		"proxy":   proxyFiles,
		"plugins": pluginsTree,
	})
}

// ensureModulesDir creates modules directory with default init.lua if not exists
func (s *Server) ensureModulesDir(modulesDir string) {
	if _, err := os.Stat(modulesDir); os.IsNotExist(err) {
		os.MkdirAll(modulesDir, 0755)

		// Create default init.lua
		initPath := filepath.Join(modulesDir, "init.lua")
		os.WriteFile(initPath, []byte(defaultInitLua), 0644)
	}
}

// buildFileTree recursively builds file tree structure
func (s *Server) buildFileTree(dirPath, relativePath string) *FileTreeNode {
	node := &FileTreeNode{
		Name:     filepath.Base(dirPath),
		Path:     relativePath,
		Type:     "dir",
		Children: []*FileTreeNode{},
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return node
	}

	// Sort: directories first, then files
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		childPath := filepath.Join(dirPath, entry.Name())
		childRelPath := filepath.Join(relativePath, entry.Name())

		if entry.IsDir() {
			// Recurse into subdirectory
			child := s.buildFileTree(childPath, childRelPath)
			node.Children = append(node.Children, child)
		} else {
			// Check if file has allowed extension
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			allowed := false
			for _, allowedExt := range allowedExtensions {
				if ext == allowedExt {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}

			info, _ := entry.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}

			node.Children = append(node.Children, &FileTreeNode{
				Name: entry.Name(),
				Path: childRelPath,
				Type: "file",
				Size: size,
			})
		}
	}

	return node
}

// markTreeReadonly sets readonly=true on all file nodes recursively
func markTreeReadonly(node *FileTreeNode) {
	if node == nil {
		return
	}
	if node.Type == "file" {
		node.Readonly = true
	}
	for _, child := range node.Children {
		markTreeReadonly(child)
	}
}

// handleAPIEditorFile returns content of a specific file
// Supports both system files (main.lua, etc.) and module files (modules/*)
func (s *Server) handleAPIEditorFile(c *gin.Context) {
	requestedPath := c.Param("path")
	if requestedPath == "" {
		requestedPath = c.Param("filename") // Legacy support
	}
	// Strip leading slash from wildcard path
	requestedPath = strings.TrimPrefix(requestedPath, "/")

	cluDir := filepath.Join(s.serverDir, "a")
	readonly := false
	isModule := false

	// Check if it's a system file
	cleanPath := strings.ToLower(filepath.Clean(requestedPath))
	isProxy := false
	for _, f := range systemEditorFiles {
		if cleanPath == f {
			readonly = true
			break
		}
	}

	// Check if it's a proxy file from imports/
	if !readonly && strings.HasPrefix(cleanPath, "imports/") {
		baseName := filepath.Base(cleanPath)
		if strings.HasPrefix(baseName, "proxy_") && strings.HasSuffix(baseName, ".lua") {
			readonly = true
			isProxy = true
		}
	}

	// Check if it's a plugin file
	isPlugin := false
	if !readonly && strings.HasPrefix(cleanPath, pluginsDirName+string(filepath.Separator)) {
		if strings.HasSuffix(cleanPath, ".lua") {
			readonly = true
			isPlugin = true
		}
	}

	// If not a system file, proxy, or plugin, must be in modules/
	if !readonly && !isPlugin && !strings.HasPrefix(cleanPath, modulesDirName+string(filepath.Separator)) && cleanPath != modulesDirName {
		// Try to match as system file with different case
		for _, f := range systemEditorFiles {
			if strings.EqualFold(cleanPath, f) {
				requestedPath = f
				readonly = true
				break
			}
		}
		if !readonly {
			c.JSON(http.StatusForbidden, gin.H{"error": "File not allowed"})
			return
		}
	} else if !readonly {
		isModule = true
	}

	// Security: validate path
	if !s.isPathSafe(requestedPath) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid path"})
		return
	}

	// Modules, proxy, and plugin files are stored outside a/ directory
	var filePath string
	if isModule || isProxy || isPlugin {
		filePath = filepath.Join(s.serverDir, requestedPath)
	} else {
		filePath = filepath.Join(cluDir, requestedPath)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file"})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"path":     requestedPath,
		"content":  string(content),
		"size":     len(content),
		"readonly": readonly,
	})
}

// isPathSafe validates that path doesn't escape the allowed directory
func (s *Server) isPathSafe(requestedPath string) bool {
	clean := filepath.Clean(requestedPath)

	// No path traversal
	if strings.Contains(clean, "..") {
		return false
	}

	// Must be a system file or in modules/
	for _, f := range systemEditorFiles {
		if strings.EqualFold(clean, f) {
			return true
		}
	}

	// Allow proxy files from imports/
	if strings.HasPrefix(clean, "imports/") {
		baseName := filepath.Base(clean)
		if strings.HasPrefix(baseName, "proxy_") && strings.HasSuffix(baseName, ".lua") {
			return true
		}
	}

	// Allow plugin .lua files (read-only)
	if strings.HasPrefix(clean, pluginsDirName+string(filepath.Separator)) && strings.HasSuffix(clean, ".lua") {
		return true
	}

	return strings.HasPrefix(clean, modulesDirName+string(filepath.Separator)) ||
		clean == modulesDirName
}

// isModulePath checks if path is within modules directory
func (s *Server) isModulePath(requestedPath string) bool {
	clean := filepath.Clean(requestedPath)
	return strings.HasPrefix(clean, modulesDirName+string(filepath.Separator))
}

// handleAPIEditorCreateFile creates a new file in modules/
func (s *Server) handleAPIEditorCreateFile(c *gin.Context) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Validate path is in modules/
	if !s.isModulePath(req.Path) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Can only create files in modules/"})
		return
	}

	// Validate extension
	ext := strings.ToLower(filepath.Ext(req.Path))
	validExt := false
	for _, allowed := range allowedExtensions {
		if ext == allowed {
			validExt = true
			break
		}
	}
	if !validExt {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only .lua files allowed"})
		return
	}

	// Validate size
	if len(req.Content) > maxFileSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File too large (max 100KB)"})
		return
	}

	// Security check
	if !s.isPathSafe(req.Path) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid path"})
		return
	}

	// Modules are stored outside a/ directory
	filePath := filepath.Join(s.serverDir, req.Path)

	// Check if file already exists
	if _, err := os.Stat(filePath); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "File already exists"})
		return
	}

	// Ensure parent directory exists
	parentDir := filepath.Dir(filePath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create directory"})
		return
	}

	// Create file
	if err := os.WriteFile(filePath, []byte(req.Content), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create file"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"path": req.Path,
		"size": len(req.Content),
	})
}

// handleAPIEditorSaveFile saves/updates an existing file
func (s *Server) handleAPIEditorSaveFile(c *gin.Context) {
	requestedPath := strings.TrimPrefix(c.Param("path"), "/")

	var req struct {
		Content string `json:"content"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Only allow saving files in modules/
	if !s.isModulePath(requestedPath) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Cannot modify system files"})
		return
	}

	// Validate size
	if len(req.Content) > maxFileSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File too large (max 100KB)"})
		return
	}

	// Security check
	if !s.isPathSafe(requestedPath) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid path"})
		return
	}

	// Modules are stored outside a/ directory
	filePath := filepath.Join(s.serverDir, requestedPath)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	// Save file
	if err := os.WriteFile(filePath, []byte(req.Content), 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"path": requestedPath,
		"size": len(req.Content),
	})
}

// handleAPIEditorDeleteFile deletes a file from modules/
func (s *Server) handleAPIEditorDeleteFile(c *gin.Context) {
	requestedPath := strings.TrimPrefix(c.Param("path"), "/")

	// Only allow deleting files in modules/
	if !s.isModulePath(requestedPath) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Cannot delete system files"})
		return
	}

	// Security check
	if !s.isPathSafe(requestedPath) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid path"})
		return
	}

	// Modules are stored outside a/ directory
	filePath := filepath.Join(s.serverDir, requestedPath)

	// Check if it's a file (not directory)
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}
	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Use folder endpoint to delete directories"})
		return
	}

	// Delete file
	if err := os.Remove(filePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete file"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": requestedPath})
}

// handleAPIEditorCreateFolder creates a new folder in modules/
func (s *Server) handleAPIEditorCreateFolder(c *gin.Context) {
	var req struct {
		Path string `json:"path"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Validate path is in modules/
	if !s.isModulePath(req.Path) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Can only create folders in modules/"})
		return
	}

	// Security check
	if !s.isPathSafe(req.Path) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid path"})
		return
	}

	// Modules are stored outside a/ directory
	folderPath := filepath.Join(s.serverDir, req.Path)

	// Check if already exists
	if _, err := os.Stat(folderPath); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Folder already exists"})
		return
	}

	// Create folder
	if err := os.MkdirAll(folderPath, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create folder"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"path": req.Path})
}

// handleAPIEditorDeleteFolder deletes an empty folder from modules/
func (s *Server) handleAPIEditorDeleteFolder(c *gin.Context) {
	requestedPath := strings.TrimPrefix(c.Param("path"), "/")

	// Only allow deleting folders in modules/
	if !s.isModulePath(requestedPath) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Cannot delete system folders"})
		return
	}

	// Security check
	if !s.isPathSafe(requestedPath) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid path"})
		return
	}

	// Modules are stored outside a/ directory
	folderPath := filepath.Join(s.serverDir, requestedPath)

	// Check if it's a directory
	info, err := os.Stat(folderPath)
	if os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Folder not found"})
		return
	}
	if !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Not a folder"})
		return
	}

	// Check if folder is empty
	entries, err := os.ReadDir(folderPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read folder"})
		return
	}
	if len(entries) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Folder is not empty"})
		return
	}

	// Delete folder
	if err := os.Remove(folderPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete folder"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": requestedPath})
}

// handleAPIEditorValidate validates Lua content for syntax errors and blocklisted functions
func (s *Server) handleAPIEditorValidate(c *gin.Context) {
	var req struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	report := mcp.ValidateDraft(req.Content)
	c.JSON(http.StatusOK, report)
}

// handleAPIEditorSearch searches for text in .lua files under modules/
func (s *Server) handleAPIEditorSearch(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusOK, []gin.H{})
		return
	}

	modulesDir := filepath.Join(s.serverDir, modulesDirName)
	queryLower := strings.ToLower(query)

	type searchResult struct {
		Path       string `json:"path"`
		Line       string `json:"line"`
		LineNumber int    `json:"lineNumber"`
	}

	var results []searchResult
	const maxResults = 100

	filepath.Walk(modulesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		nameLC := strings.ToLower(info.Name())
		if !strings.HasSuffix(nameLC, ".lua") && !strings.HasSuffix(nameLC, ".css") {
			return nil
		}
		if len(results) >= maxResults {
			return filepath.SkipAll
		}

		relPath, _ := filepath.Rel(s.serverDir, path)

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if strings.Contains(strings.ToLower(line), queryLower) {
				results = append(results, searchResult{
					Path:       relPath,
					Line:       strings.TrimSpace(line),
					LineNumber: lineNum,
				})
				if len(results) >= maxResults {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})

	c.JSON(http.StatusOK, results)
}

// handleAPIEditorRename renames a file within modules/
func (s *Server) handleAPIEditorRename(c *gin.Context) {
	var req struct {
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Both must be in modules/
	if !s.isModulePath(req.OldPath) || !s.isModulePath(req.NewPath) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Can only rename files in modules/"})
		return
	}

	// Must have allowed extension
	renameExt := strings.ToLower(filepath.Ext(req.NewPath))
	renameExtOk := false
	for _, ae := range allowedExtensions {
		if renameExt == ae {
			renameExtOk = true
			break
		}
	}
	if !renameExtOk {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only .lua and .css files allowed"})
		return
	}

	// Security check
	if !s.isPathSafe(req.OldPath) || !s.isPathSafe(req.NewPath) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid path"})
		return
	}

	oldFull := filepath.Join(s.serverDir, req.OldPath)
	newFull := filepath.Join(s.serverDir, req.NewPath)

	// Old must exist
	if _, err := os.Stat(oldFull); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	// New must not exist
	if _, err := os.Stat(newFull); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Target file already exists"})
		return
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(newFull), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create directory"})
		return
	}

	if err := os.Rename(oldFull, newFull); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to rename file"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"oldPath": req.OldPath,
		"newPath": req.NewPath,
	})
}

// handleAPIEditorRun executes a Lua script in the running VM and returns the result
func (s *Server) handleAPIEditorRun(c *gin.Context) {
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua runtime not available"})
		return
	}

	var req struct {
		Script string `json:"script"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Script == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Script is required"})
		return
	}

	result, err := s.executeAs(c, "user:unknown", req.Script)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": result})
}

// handleAPILuaReload reloads the Lua runtime
//
//	@Summary		Reload Lua runtime
//	@Description	Tears down and re-initializes the embedded Lua runtime, re-reading om.lua, user.lua and modules from disk. Returns 503 when no Lua reloader is wired into the server.
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"success: true, message: \"Lua runtime reloaded\""
//	@Failure		500	{object}	web.APIError
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/lua/reload [post]
func (s *Server) handleAPILuaReload(c *gin.Context) {
	if s.luaReloader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua reloader not available"})
		return
	}

	if err := s.luaReloader.Reload(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reload: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Lua runtime reloaded",
	})
}
