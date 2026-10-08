package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/workspace"
)

// FileProvider reads files from the data directory with path safety checks.
type FileProvider struct {
	serverDir string
}

// NewFileProvider creates a new FileProvider rooted at the data directory.
func NewFileProvider(serverDir string) *FileProvider {
	return &FileProvider{serverDir: serverDir}
}

// mcpAllowedPath checks if a relative path is safe for MCP file reading.
// Allowed paths:
//   - a/main.lua, a/user.lua, a/om.lua (system Lua files)
//   - modules/**/*.lua (user modules)
//   - imports/proxy_*.lua (OM proxy files)
//   - docs/ai/*.md (agent documentation)
func mcpAllowedPath(relPath string) bool {
	clean := filepath.Clean(relPath)

	// No path traversal
	if strings.Contains(clean, "..") {
		return false
	}

	// Block secrets
	base := filepath.Base(clean)
	if base == ".vclu.json" || base == "keys.json" {
		return false
	}

	// System Lua files: a/main.lua, a/user.lua, a/om.lua
	systemFiles := []string{"a/main.lua", "a/user.lua", "a/om.lua"}
	for _, f := range systemFiles {
		if clean == f {
			return true
		}
	}

	// User modules: modules/**/*.lua
	if strings.HasPrefix(clean, "modules/") && strings.HasSuffix(clean, ".lua") {
		return true
	}

	// OM proxy files: imports/proxy_*.lua
	if strings.HasPrefix(clean, "imports/") {
		if strings.HasPrefix(base, "proxy_") && strings.HasSuffix(base, ".lua") {
			return true
		}
	}

	// Agent documentation: docs/ai/*.md, ai/*.md
	if strings.HasPrefix(clean, "docs/ai/") && strings.HasSuffix(clean, ".md") {
		return true
	}
	if strings.HasPrefix(clean, "ai/") && strings.HasSuffix(clean, ".md") {
		return true
	}

	// Plugin source: plugins/**/*.lua, plugins/**/plugin.json
	if strings.HasPrefix(clean, "plugins/") {
		if strings.HasSuffix(clean, ".lua") || base == "plugin.json" {
			return true
		}
	}

	return false
}

// ReadFile reads a file from the data directory.
func (fp *FileProvider) ReadFile(relPath string) (string, error) {
	if !mcpAllowedPath(relPath) {
		return "", fmt.Errorf("access denied: %s", relPath)
	}

	if err := workspace.CheckNoSymlinks(fp.serverDir, relPath); err != nil {
		return "", err
	}
	absPath := filepath.Join(fp.serverDir, filepath.Clean(relPath))
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", relPath)
		}
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	// Size limit: 100KB
	if len(data) > 100*1024 {
		return "", fmt.Errorf("file too large: %s (%d bytes, max 102400)", relPath, len(data))
	}

	return string(data), nil
}

// FileInfo describes a file for listing.
type FileInfo struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modified"` // Unix timestamp
}

// ListFiles returns files matching a glob pattern within allowed paths.
func (fp *FileProvider) ListFiles(pattern string) ([]FileInfo, error) {
	clean := filepath.Clean(pattern)
	if strings.Contains(clean, "..") {
		return nil, fmt.Errorf("invalid pattern: %s", pattern)
	}

	absPattern := filepath.Join(fp.serverDir, clean)
	matches, err := filepath.Glob(absPattern)
	if err != nil {
		return nil, fmt.Errorf("invalid glob pattern: %w", err)
	}

	var result []FileInfo
	for _, absPath := range matches {
		relPath, err := filepath.Rel(fp.serverDir, absPath)
		if err != nil {
			continue
		}

		if !mcpAllowedPath(relPath) || workspace.CheckNoSymlinks(fp.serverDir, relPath) != nil {
			continue
		}

		info, err := os.Stat(absPath)
		if err != nil || info.IsDir() {
			continue
		}

		result = append(result, FileInfo{
			Path:    relPath,
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
		})
	}

	return result, nil
}

// GrepResult describes a single match from grep.
type GrepResult struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// Grep searches for a regex pattern in files matching a glob filter.
func (fp *FileProvider) Grep(pattern string, glob string, maxResults int) ([]GrepResult, error) {
	if maxResults <= 0 {
		maxResults = 50
	}
	if maxResults > 200 {
		maxResults = 200
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}

	if glob == "" {
		glob = "*.lua"
	}
	globRe, err := globRegexp(glob)
	if err != nil {
		return nil, fmt.Errorf("invalid glob: %w", err)
	}

	// Walk allowed directories and match files
	var files []string
	walkDirs := []string{"a", "modules", "imports", "docs", "plugins", "ai"}
	for _, dir := range walkDirs {
		absDir := filepath.Join(fp.serverDir, dir)
		filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			relPath, err := filepath.Rel(fp.serverDir, path)
			if err != nil {
				return nil
			}
			if !mcpAllowedPath(relPath) || workspace.CheckNoSymlinks(fp.serverDir, relPath) != nil {
				return nil
			}
			if globRe.MatchString(filepath.Base(relPath)) || globRe.MatchString(filepath.ToSlash(relPath)) {
				files = append(files, relPath)
			}
			return nil
		})
	}

	var results []GrepResult
	for _, relPath := range files {
		if len(results) >= maxResults {
			break
		}

		absPath := filepath.Join(fp.serverDir, relPath)
		data, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if len(results) >= maxResults {
				break
			}
			if re.MatchString(line) {
				results = append(results, GrepResult{
					File:    relPath,
					Line:    i + 1,
					Content: truncate(line, 200),
				})
			}
		}
	}

	return results, nil
}

// globRegexp compiles a glob: * and ? stay within a path segment, ** spans
// directories ("modules/**/*.lua" matches modules/a.lua and modules/x/y/a.lua).
func globRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch c := glob[i]; {
		case strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(glob[i : i+1])) // byte by byte keeps UTF-8 intact
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// mcpDeleteAllowedPath checks if a relative path is safe for MCP file deletion.
// Only modules/*.lua files are deletable — never system files, docs, or plugins.
func mcpDeleteAllowedPath(relPath string) bool {
	clean := filepath.Clean(relPath)

	if strings.Contains(clean, "..") {
		return false
	}

	// Only modules/ directory
	if !strings.HasPrefix(clean, "modules/") {
		return false
	}

	// Must be a .lua file
	if !strings.HasSuffix(clean, ".lua") {
		return false
	}

	return true
}

// DeleteFile deletes a file from the data directory (modules/*.lua only).
func (fp *FileProvider) DeleteFile(relPath string) error {
	if !mcpDeleteAllowedPath(relPath) {
		return fmt.Errorf("access denied: can only delete modules/*.lua files, not %s", relPath)
	}

	if err := workspace.CheckNoSymlinks(fp.serverDir, relPath); err != nil {
		return err
	}
	absPath := filepath.Join(fp.serverDir, filepath.Clean(relPath))

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", relPath)
	}

	if err := os.Remove(absPath); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
