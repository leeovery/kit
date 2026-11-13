// Package whole writes files whole or not at all, so nothing reads one
// half written: each into a file beside it, then renamed into its place.
package whole

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Write writes data to path whole or not at all: into a hidden file beside
// it (.<name>.<random>.tmp), its mode set to mode before anything's
// written, then renamed into place, replacing a file or a link there.
func Write(path string, data []byte, mode fs.FileMode) error {
	return write(path, data, mode, false)
}

// Synced is Write with the file synced to the disk before it's renamed, so
// a power loss leaves it whole too.
func Synced(path string, data []byte, mode fs.FileMode) error {
	return write(path, data, mode, true)
}

func write(path string, data []byte, mode fs.FileMode, sync bool) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	err = f.Chmod(mode)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil && sync {
		err = f.Sync()
	}
	if closed := f.Close(); err == nil {
		err = closed
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
