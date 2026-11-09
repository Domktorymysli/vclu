package storage

import (
	"io/fs"
	"syscall"
)

// statFS returns the size and the space free for unprivileged users (df's Avail).
func statFS(path string) (total, free int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bs := int64(st.Frsize)
	if bs <= 0 {
		bs = int64(st.Bsize)
	}
	return int64(st.Blocks) * bs, int64(st.Bavail) * bs, nil
}

// allocated is what a file takes on disk, as du counts it.
func allocated(fi fs.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return fi.Size()
}
