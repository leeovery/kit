package steps_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/steps"
)

func TestFullDiskAccess(t *testing.T) {
	home := t.TempDir()
	for _, p := range []string{"Library/Safari", "Library/Mail", "Library/Messages"} {
		if err := os.MkdirAll(filepath.Join(home, p), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if state, text, _ := checked(t, steps.FullDiskAccess(home)); state != check.OK || text != "granted" {
		t.Errorf("readable = %s %q", state, text)
	}
	for _, p := range []string{"Library/Safari", "Library/Mail", "Library/Messages"} {
		if err := os.Chmod(filepath.Join(home, p), 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(home, p), 0o700) })
	}
	if state, _, ids := checked(t, steps.FullDiskAccess(home)); state != check.Attention || !slices.Equal(ids, []string{"full-disk-access:denied"}) {
		t.Errorf("denied = %s %q", state, ids)
	}
}
