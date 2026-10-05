package state_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/leeovery/kit/internal/state"
)

type counter struct {
	N int `json:"n"`
}

func TestLoadWithoutARecord(t *testing.T) {
	got, err := state.Load[counter](t.TempDir(), "count.json")
	if err != nil || got.N != 0 {
		t.Errorf("Load() = %+v, %v; want a zero record", got, err)
	}
}

// Updates at once never lose each other's changes.
func TestUpdatesAtOnce(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := state.Update(dir, "count.json", func(c *counter) { c.N++ }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, err := state.Load[counter](dir, "count.json")
	if err != nil || got.N != 20 {
		t.Errorf("after 20 updates, n = %d, %v", got.N, err)
	}
}

func TestLoadRefusesWhatDoesntRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "count.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Load[counter](dir, "count.json"); err == nil || err.Error()[:22] != "read the count record:" {
		t.Errorf("Load() = %v", err)
	}
}
