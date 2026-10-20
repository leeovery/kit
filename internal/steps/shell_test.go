package steps_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/steps"
)

func TestPathFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state", "path")
	step := steps.PathFile([]string{"/home/someone/bin", "node_modules/.bin", "/opt/homebrew/bin"}, file)
	res := step.Check(t.Context())
	if res.State != check.Attention || len(res.Items) != 1 || res.Items[0].State != "missing" || res.Items[0].Action != steps.ActionWrite {
		t.Fatalf("before writing = %+v", res)
	}
	if err := step.Apply(t.Context(), res); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(file); string(data) != "/home/someone/bin:node_modules/.bin:/opt/homebrew/bin\n" {
		t.Errorf("the file holds %q", data)
	}
	if res := step.Check(t.Context()); res.State != check.OK || res.Summary != "3 directories" {
		t.Errorf("written = %s %q", res.State, res.Summary)
	}
	changed := steps.PathFile([]string{"/opt/homebrew/bin"}, file).Check(t.Context())
	if changed.State != check.Attention || changed.Items[0].State != "changed" {
		t.Errorf("after [paths] changed = %+v", changed)
	}
}
