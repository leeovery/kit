package steps_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/runner/runnertest"
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

func TestOhMyZsh(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	step := steps.OhMyZsh(fake, home)
	res := step.Check(t.Context())
	if res.State != check.Attention || res.Items[0].Action != steps.ActionInstall {
		t.Fatalf("not installed = %+v", res)
	}
	fake.On("curl", "-fsSL", "https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/HEAD/tools/install.sh").Prints("echo installing\n")
	fake.On("env", "KEEP_ZSHRC=yes", "ZSH="+filepath.Join(home, ".oh-my-zsh"), "sh", "-s", "--", "--unattended")
	if err := step.Apply(t.Context(), res); err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".oh-my-zsh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".oh-my-zsh", "oh-my-zsh.sh"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); res.State != check.OK {
		t.Errorf("installed = %+v", res)
	}
}
