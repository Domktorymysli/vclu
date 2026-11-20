package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// omBackupsDir holds copies of the stored om files from before an import or a delete.
const omBackupsDir = "workspace/backups/om"

// omBackupsKept is how many copies are kept, the newest.
const omBackupsKept = 10

// omFilesList is the name of the copied .vclu.json entries in a backup.
const omFilesList = "om-files.json"

// storedOMFiles are the om and proxy files in imports/ and their .vclu.json entries.
func (s *Server) storedOMFiles() (map[string][]byte, error) {
	files := map[string][]byte{}
	dir := config.GetOMFilesDir(s.serverDir)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		n := e.Name()
		if !e.Type().IsRegular() || !strings.HasSuffix(n, ".lua") || !strings.HasPrefix(n, "om_") && !strings.HasPrefix(n, "proxy_") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		files[n] = b
	}
	if om := config.GetOMFiles(s.serverDir); len(om) > 0 {
		// without the time, an import of the same files makes no new copy
		for i := range om {
			om[i].UploadedAt = ""
		}
		b, err := json.MarshalIndent(om, "", "  ")
		if err != nil {
			return nil, err
		}
		files[omFilesList] = b
	}
	return files, nil
}

// backupOMFiles copies the stored om files to workspace/backups/om/<time>/, unless the newest
// copy has them already, and returns the copy's path; "" with nothing stored.
func (s *Server) backupOMFiles() (string, error) {
	files, err := s.storedOMFiles()
	if err != nil {
		return "", fmt.Errorf("failed to back up the OM files: %v", err)
	}
	if len(files) == 0 {
		return "", nil
	}
	root := filepath.Join(s.serverDir, filepath.FromSlash(omBackupsDir))
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("failed to back up the OM files: %v", err)
	}
	names := omBackups(root)
	if len(names) > 0 && sameFiles(filepath.Join(root, names[len(names)-1]), files) {
		return omBackupsDir + "/" + names[len(names)-1], nil
	}
	name, err := s.writeOMBackup(root, files)
	if err != nil {
		return "", fmt.Errorf("failed to back up the OM files: %v", err)
	}
	names = omBackups(root)
	for i := 0; i < len(names)-omBackupsKept; i++ {
		_ = os.RemoveAll(filepath.Join(root, names[i]))
	}
	return omBackupsDir + "/" + name, nil
}

// writeOMBackup writes files to a new directory of root, named by the time.
func (s *Server) writeOMBackup(root string, files map[string][]byte) (string, error) {
	// to the microsecond: names sort as the copies were made, a trimmed one never comes back
	var name string
	for i := 0; ; i++ {
		name = time.Now().Format("20060102_150405.000000")
		err := os.Mkdir(filepath.Join(root, name), 0o755)
		if err == nil {
			break
		}
		if !os.IsExist(err) || i > 100 {
			return "", err
		}
		time.Sleep(time.Microsecond)
	}
	dir := filepath.Join(root, name)
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
	return name, nil
}

// omBackups are the backup directories under root, oldest first.
func omBackups(root string) []string {
	entries, _ := os.ReadDir(root)
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// sameFiles tells whether dir holds exactly files.
func sameFiles(dir string, files map[string][]byte) bool {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != len(files) {
		return false
	}
	for _, e := range entries {
		want, ok := files[e.Name()]
		if !ok {
			return false
		}
		got, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || !bytes.Equal(got, want) {
			return false
		}
	}
	return true
}

// writeFileAtomic writes next to path and renames, so a failed write leaves the old file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
