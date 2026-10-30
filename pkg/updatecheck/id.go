package updatecheck

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// IDFile is the instance id's file in the data directory; deleting it gives a new id.
const IDFile = "instance-id"

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// LoadOrCreateID returns the instance id saved in dir, creating a random UUID v4 the
// first time. When the file cannot be written the new id is still returned, with the error.
func LoadOrCreateID(dir string) (string, error) {
	path := filepath.Join(dir, IDFile)
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); uuidRe.MatchString(id) {
			return id, nil
		}
	}
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	return id, writeFileAtomic(path, []byte(id+"\n"))
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
