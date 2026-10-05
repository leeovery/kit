// Package state is kit's records in its state directory: JSON files read
// whole, and changed under a lock, so two runs at once can't lose each
// other's changes.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Load reads the record in the file named name in dir into a T: a zero T
// when there's none.
func Load[T any](dir, name string) (T, error) {
	var v T
	what := strings.TrimSuffix(name, ".json")
	data, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, fmt.Errorf("read the %s record: %w", what, err)
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("read the %s record: %w", what, err)
	}
	return v, nil
}

// Update reads the record in the file named name in dir, changes it, and
// writes it back whole, holding a lock throughout.
func Update[T any](dir, name string, change func(*T)) error {
	what := strings.TrimSuffix(name, ".json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("make the state directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, what+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("lock the %s record: %w", what, err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock the %s record: %w", what, err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	v, err := Load[T](dir, name)
	if err != nil {
		return err
	}
	change(&v)
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("write the %s record: %w", what, err)
	}
	tmp, err := os.CreateTemp(dir, name+".*")
	if err != nil {
		return fmt.Errorf("write the %s record: %w", what, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write the %s record: %w", what, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the %s record: %w", what, err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("write the %s record: %w", what, err)
	}
	return nil
}
