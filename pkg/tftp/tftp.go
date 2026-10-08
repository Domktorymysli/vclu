package tftp

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pin/tftp/v3"
)

const (
	DefaultPort    = 69
	DefaultTimeout = 5 * time.Second
)

// ServerMode defines the TFTP server operation mode
type ServerMode int

const (
	GetOnly ServerMode = iota
	PutOnly
	GetAndPut
	GetAndReplace
)

// Server represents a TFTP server
type Server struct {
	server    *tftp.Server
	rootDir   string
	mode      ServerMode
	localAddr string
	running   bool
}

// NewServer creates a new TFTP server
func NewServer(rootDir string, localAddr string, port int, mode ServerMode) *Server {
	s := &Server{
		rootDir:   filepath.Clean(rootDir),
		mode:      mode,
		localAddr: fmt.Sprintf("%s:%d", localAddr, port),
	}

	// Create TFTP server with handlers
	s.server = tftp.NewServer(s.readHandler, s.writeHandler)
	s.server.SetTimeout(DefaultTimeout)

	return s
}

// Start starts the TFTP server
func (s *Server) Start() error {
	if s.running {
		return nil
	}

	// Ensure root directory exists
	if err := os.MkdirAll(s.rootDir, 0755); err != nil {
		return fmt.Errorf("failed to create TFTP root directory: %w", err)
	}

	log.Printf("[TFTP] starting on %s, root: %s", s.localAddr, s.rootDir)

	// Create new TFTP server instance (required after Shutdown)
	s.server = tftp.NewServer(s.readHandler, s.writeHandler)
	s.server.SetTimeout(DefaultTimeout)

	go func() {
		if err := s.server.ListenAndServe(s.localAddr); err != nil {
			log.Printf("[TFTP] server error: %v", err)
		}
	}()

	s.running = true
	return nil
}

// Stop stops the TFTP server
func (s *Server) Stop() {
	if !s.running {
		return
	}

	s.server.Shutdown()
	s.running = false
}

// IsRunning returns whether the server is running
func (s *Server) IsRunning() bool {
	return s.running
}

// readHandler handles TFTP read requests (RRQ - client wants to download a file)
func (s *Server) readHandler(filename string, rf io.ReaderFrom) error {
	// Check if reads are allowed
	if s.mode == PutOnly {
		log.Printf("[TFTP] read denied (PUT_ONLY mode): %s", filename)
		return fmt.Errorf("read not allowed by server")
	}

	// Parse and validate file path
	filePath, err := s.parsePath(filename)
	if err != nil {
		log.Printf("[TFTP] read invalid path: %s - %v", filename, err)
		return err
	}

	// Check if file exists (case-insensitive - try uppercase for DOS compatibility)
	_, err = os.Stat(filePath)
	if err != nil {
		// Try uppercase filename (DOS format)
		dir := filepath.Dir(filePath)
		base := filepath.Base(filePath)
		upperPath := filepath.Join(dir, strings.ToUpper(base))
		_, err = os.Stat(upperPath)
		if err == nil {
			filePath = upperPath
		} else {
			log.Printf("[TFTP] read file not found: %s", filePath)
			return err
		}
	}

	// Open file for reading
	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("[TFTP] read cannot open: %s - %v", filePath, err)
		return err
	}
	defer file.Close()

	// Send file to client
	n, err := rf.ReadFrom(file)
	if err != nil {
		log.Printf("[TFTP] read failed: %s - %v", filename, err)
		return err
	}

	log.Printf("[TFTP] read %s (%d bytes)", filename, n)
	return nil
}

// writeHandler handles TFTP write requests (WRQ - client wants to upload a file)
func (s *Server) writeHandler(filename string, wt io.WriterTo) error {
	// Check if writes are allowed
	if s.mode == GetOnly {
		log.Printf("[TFTP] write denied (GET_ONLY mode): %s", filename)
		return fmt.Errorf("write not allowed by server")
	}

	// Parse and validate file path
	filePath, err := s.parsePath(filename)
	if err != nil {
		log.Printf("[TFTP] write invalid path: %s - %v", filename, err)
		return err
	}

	// Check if file exists (for modes that don't allow replace)
	if s.mode == GetAndPut {
		if _, err := os.Stat(filePath); err == nil {
			log.Printf("[TFTP] write file exists (no replace): %s", filePath)
			return fmt.Errorf("file already exists")
		}
	}

	// Ensure parent directory exists
	parentDir := filepath.Dir(filePath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		log.Printf("[TFTP] write cannot create dir: %s - %v", parentDir, err)
		return err
	}

	// Remove old uppercase version if we're saving lowercase
	// (e.g., remove OM.LUA when saving om.lua)
	base := filepath.Base(filePath)
	upperBase := strings.ToUpper(base)
	if base != upperBase {
		upperPath := filepath.Join(parentDir, upperBase)
		if _, err := os.Stat(upperPath); err == nil {
			os.Remove(upperPath)
		}
	}

	// Create file for writing
	file, err := os.Create(filePath)
	if err != nil {
		log.Printf("[TFTP] write cannot create: %s - %v", filePath, err)
		return err
	}
	defer file.Close()

	// Receive file from client
	n, err := wt.WriteTo(file)
	if err != nil {
		log.Printf("[TFTP] write failed: %s - %v", filename, err)
		return err
	}

	log.Printf("[TFTP] write %s (%d bytes)", filename, n)
	return nil
}

// parsePath parses and validates a TFTP path
// Converts paths like "a:\file.txt" or "a:/file.txt" to "/a/file.txt"
// Prevents directory traversal attacks
// Note: .lua files are converted to lowercase for Linux compatibility
func (s *Server) parsePath(tftpPath string) (string, error) {
	// Normalize path separators
	tftpPath = strings.ReplaceAll(tftpPath, "\\", "/")

	// Handle drive letter format (a:\file.txt or a:/file.txt)
	var drive string
	if len(tftpPath) >= 2 && tftpPath[1] == ':' {
		drive = string(tftpPath[0])
		tftpPath = tftpPath[2:]
	}

	// Remove leading slash
	tftpPath = strings.TrimPrefix(tftpPath, "/")

	// Convert .lua/.LUA files to lowercase for Linux compatibility
	// OM uploads OM.LUA but main.lua uses require("om") which needs om.lua
	originalPath := tftpPath
	if strings.HasSuffix(strings.ToLower(tftpPath), ".lua") {
		tftpPath = strings.ToLower(tftpPath)
		if tftpPath != originalPath {
			log.Printf("[TFTP] lowercase: %s -> %s", originalPath, tftpPath)
		}
	}

	// Build absolute path
	var absPath string
	if drive != "" {
		absPath = filepath.Join(s.rootDir, drive, tftpPath)
	} else {
		absPath = filepath.Join(s.rootDir, tftpPath)
	}

	// Clean the path (removes ../ and ./)
	absPath = filepath.Clean(absPath)

	// Security check: ensure the file is within root directory
	relPath, err := filepath.Rel(s.rootDir, absPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return "", fmt.Errorf("access violation: cannot access files outside TFTP root")
	}

	return absPath, nil
}
