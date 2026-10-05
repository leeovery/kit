package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAddPathDeclaresItLastAndWritesTheShellsPath(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync([]string{"laptop/declarations"}, "kit add path ~/tools/bin (laptop): the tools")
	out, _, code := w.run(t, "add", "path", filepath.Join(w.home, "tools/bin"), "--note", "the tools")
	if code != 0 || !strings.Contains(out, "~/tools/bin ok declared in laptop, last on the PATH") {
		t.Errorf("kit add path printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "paths"); got != "~/tools/bin   # the tools\n" {
		t.Errorf("[paths] = %q", got)
	}
	want := filepath.Join(w.home, ".local/bin") + ":/opt/homebrew/bin:" + filepath.Join(w.home, "tools/bin") + "\n"
	if got := w.read(t, ".local/state/kit/path"); got != want {
		t.Errorf("the shell's PATH = %q, want %q", got, want)
	}
}

func TestRemovePathOnEveryMacNeedsShared(t *testing.T) {
	w := laptopWorld(t)
	out, _, code := w.run(t, "remove", "path", "/opt/homebrew/bin")
	if code == 0 || !strings.Contains(out, "on every Mac's PATH, in shared/declarations:3: --shared takes it out of every Mac's") {
		t.Errorf("kit remove path printed\n%s exit %d", out, code)
	}
	w.expectSync([]string{"shared/declarations"}, "kit remove path /opt/homebrew/bin (laptop)")
	if _, _, code := w.run(t, "remove", "path", "/opt/homebrew/bin", "--shared"); code != 0 {
		t.Errorf("kit remove path --shared exit %d", code)
	}
	if got := w.read(t, ".local/state/kit/path"); got != filepath.Join(w.home, ".local/bin")+"\n" {
		t.Errorf("the shell's PATH = %q", got)
	}
}

func TestOhMyZshAsAFeature(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "features", "oh-my-zsh\n")
	out, _, _ := w.run(t, "status")
	if !strings.Contains(out, "oh-my-zsh ok not installed\noh-my-zsh missing:new Oh My Zsh (to install)\n") {
		t.Errorf("kit status printed\n%s", out)
	}
	w.fake.On("curl", "-fsSL", "https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/HEAD/tools/install.sh").Prints("echo installing\n")
	w.fake.On("env", "KEEP_ZSHRC=yes", "ZSH="+filepath.Join(w.home, ".oh-my-zsh"), "sh", "-s", "--", "--unattended")
	if out, _, _ := w.run(t, "apply", "oh-my-zsh"); !strings.Contains(out, "oh-my-zsh") {
		t.Errorf("kit apply printed\n%s", out)
	}
	ran := false
	for _, c := range w.fake.Commands() {
		ran = ran || c.Name == "env" && strings.Contains(c.Input, "echo installing")
	}
	if !ran {
		t.Error("the installer didn't run, given what curl downloaded")
	}
}
