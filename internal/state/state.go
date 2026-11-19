// Package state is kit's records in its state directory: JSON files read
// whole, and changed under a lock, so two runs at once can't lose each
// other's changes.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/leeovery/kit/internal/whole"
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

// Lock takes the lock in the file at path, calling wait between tries
// while another kit holds it, till ctx is done: unlock lets it go. Its
// file's never removed, as a kit waiting for it would go on to lock a file
// no longer there.
func Lock(ctx context.Context, path string, wait func()) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return func() { _ = f.Close() }, nil
		case !errors.Is(err, syscall.EWOULDBLOCK):
			_ = f.Close()
			return nil, err
		case ctx.Err() != nil:
			_ = f.Close()
			return nil, ctx.Err()
		}
		wait()
	}
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
	if err := whole.Write(filepath.Join(dir, name), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write the %s record: %w", what, err)
	}
	return nil
}
