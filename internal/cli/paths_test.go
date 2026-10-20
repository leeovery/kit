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

// Declared for every Mac, a name comes out of the Macs' files that had it,
// in the same edit, whatever the command, refused where a Mac's line says
// otherwise; declared for this Mac, one the
// shared file has is refused, saying so.
func TestSharedTakesTheNameOutOfTheMacs(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "paths", "~/go/bin\n")
	w.writeSection(t, "studio", "checks", "dns -- dig example.com\n")
	w.writeSection(t, "laptop", "features", "remote-login\n")

	w.expectSync([]string{"laptop/declarations", "shared/declarations"}, "kit path add ~/go/bin (laptop)")
	if out, _, code := w.run(t, "path", "add", "~/go/bin", "--shared"); code != 0 || !strings.Contains(out, "~/go/bin ok declared in shared, out of laptop, last on the PATH") {
		t.Errorf("kit path add --shared printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "paths"); got != "" {
		t.Errorf("laptop's [paths] = %q, want ~/go/bin out of it", got)
	}

	w.fake.On("dig", "example.com")
	if out, _, code := w.run(t, "check", "add", "dns", "--shared", "--", "dig", "example.org"); code != 1 || !strings.Contains(out, "studio/declarations:5 has dns -- dig example.com: give the same, or take that line out first") {
		t.Errorf("kit check add --shared, another command than studio's, printed\n%s exit %d", out, code)
	}
	w.expectSync([]string{"shared/declarations", "studio/declarations"}, "kit check add dns (laptop)")
	if out, _, code := w.run(t, "check", "add", "dns", "--shared", "--", "dig", "example.com"); code != 0 || !strings.Contains(out, "dns ok declared in shared, out of studio") {
		t.Errorf("kit check add --shared printed\n%s exit %d", out, code)
	}

	w.expectSync([]string{"laptop/declarations", "shared/declarations"}, "kit feature on remote-login (laptop)")
	if out, _, code := w.run(t, "feature", "on", "remote-login", "--shared"); code != 0 || !strings.Contains(out, "remote-login ok on, in shared, out of laptop") {
		t.Errorf("kit feature on --shared printed\n%s exit %d", out, code)
	}
	if out, _, code := w.run(t, "feature", "on", "remote-login"); code != 0 || !strings.Contains(out, "remote-login ok on already, in the shared declarations") {
		t.Errorf("kit feature on, on for every Mac, printed\n%s exit %d", out, code)
	}
	if out, _, code := w.run(t, "path", "add", "~/go/bin"); code == 0 || !strings.Contains(out, "~/go/bin is in the shared declarations already (shared/declarations), for every Mac") {
		t.Errorf("kit path add of a shared path printed\n%s exit %d", out, code)
	}
}
