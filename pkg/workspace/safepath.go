package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CheckNoSymlinks fails when rel or any directory on the way to it below root
// is a symlink, so a link in modules/ cannot redirect a read or write outside
// the data directory. Missing components are fine (a file about to be created).
func CheckNoSymlinks(root, rel string) error {
	cur := root
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed: %s", rel)
		}
	}
	return nil
}
