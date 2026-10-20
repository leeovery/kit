package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAddPathDeclaresItLastAndWritesTheShellsPath(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync([]string{"laptop/declarations"}, "kit path add ~/tools/bin (laptop): the tools")
	out, _, code := w.run(t, "path", "add", filepath.Join(w.home, "tools/bin"), "--note", "the tools")
	if code != 0 || !strings.Contains(out, "~/tools/bin ok declared in laptop, last on the PATH") {
		t.Errorf("kit path add printed\n%s exit %d", out, code)
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
	out, _, code := w.run(t, "path", "remove", "/opt/homebrew/bin")
	if code == 0 || !strings.Contains(out, "on every Mac's PATH, in shared/declarations:3: --shared takes it out of every Mac's") {
		t.Errorf("kit path remove printed\n%s exit %d", out, code)
	}
	w.expectSync([]string{"shared/declarations"}, "kit path remove /opt/homebrew/bin (laptop)")
	if _, _, code := w.run(t, "path", "remove", "/opt/homebrew/bin", "--shared"); code != 0 {
		t.Errorf("kit path remove --shared exit %d", code)
	}
	if got := w.read(t, ".local/state/kit/path"); got != filepath.Join(w.home, ".local/bin")+"\n" {
		t.Errorf("the shell's PATH = %q", got)
	}
}

func TestApplyWritesTheShellsPath(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "paths", "~/tools/bin\n")
	out, _, code := w.run(t, "apply", "path")
	if code != 0 || !strings.Contains(out, "path wrote the shell's PATH\n") {
		t.Errorf("kit apply path printed\n%s exit %d", out, code)
	}
}
