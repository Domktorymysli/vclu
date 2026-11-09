//go:build !linux && !darwin

package storage

import (
	"errors"
	"io/fs"
)

func statFS(string) (total, free int64, err error) {
	return 0, 0, errors.New("free space is not measured on this system")
}

func allocated(fi fs.FileInfo) int64 { return fi.Size() }
