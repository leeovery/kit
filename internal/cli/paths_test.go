package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A folder added for this Mac goes last in its [paths], and this Mac's come
// first in the shell's PATH, ahead of every Mac's.
func TestAddPathDeclaresItLastOfTheMacsAndWritesTheShellsPath(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "paths", "~/go/bin\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit path add ~/tools/bin (laptop): the tools")
	out, _, code := w.run(t, "path", "add", filepath.Join(w.home, "tools/bin"), "--note", "the tools")
	if code != 0 || !strings.Contains(out, "~/tools/bin ok declared in laptop, last of this Mac's on the PATH, ahead of every Mac's") {
		t.Errorf("kit path add printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "paths"); got != "~/go/bin\n~/tools/bin   # the tools\n" {
		t.Errorf("[paths] = %q", got)
	}
	want := filepath.Join(w.home, "go/bin") + ":" + filepath.Join(w.home, "tools/bin") + ":" + filepath.Join(w.home, ".local/bin") + ":/opt/homebrew/bin\n"
	if got := w.read(t, ".local/state/kit/path"); got != want {
		t.Errorf("the shell's PATH = %q, want %q", got, want)
	}
}

// A Mac may declare a folder every Mac has, which raises it on that Mac:
// first in the shell's PATH. Taken off this Mac's PATH, it comes out of
// this Mac's [paths] alone, back in every Mac's place.
func TestAddPathRaisesOneEveryMacHas(t *testing.T) {
	w := laptopWorld(t)
	local := filepath.Join(w.home, ".local/bin")
	w.expectSync(t, []string{"laptop/declarations"}, "kit path add /opt/homebrew/bin (laptop)")
	if out, _, code := w.run(t, "path", "add", "/opt/homebrew/bin"); code != 0 || !strings.Contains(out, "/opt/homebrew/bin ok declared in laptop, last of this Mac's on the PATH, ahead of every Mac's") {
		t.Errorf("kit path add of every Mac's folder printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "paths"); got != "/opt/homebrew/bin\n" {
		t.Errorf("laptop's [paths] = %q", got)
	}
	if got, want := w.read(t, ".local/state/kit/path"), "/opt/homebrew/bin:"+local+"\n"; got != want {
		t.Errorf("the shell's PATH = %q, want %q", got, want)
	}

	w.expectSync(t, []string{"laptop/declarations"}, "kit path remove /opt/homebrew/bin (laptop)")
	if out, _, code := w.run(t, "path", "remove", "/opt/homebrew/bin"); code != 0 || !strings.Contains(out, "/opt/homebrew/bin ok out of laptop") {
		t.Errorf("kit path remove of a raised folder printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "shared", "paths"); !strings.Contains(got, "/opt/homebrew/bin") {
		t.Errorf("shared's [paths] = %q, want /opt/homebrew/bin kept", got)
	}
	if got, want := w.read(t, ".local/state/kit/path"), local+":/opt/homebrew/bin\n"; got != want {
		t.Errorf("the shell's PATH = %q, want %q", got, want)
	}
}

func TestRemovePathOnEveryMacNeedsShared(t *testing.T) {
	w := laptopWorld(t)
	out, _, code := w.run(t, "path", "remove", "/opt/homebrew/bin")
	if code == 0 || !strings.Contains(out, "on every Mac's PATH, in shared/declarations:3: --shared takes it out of every Mac's") {
		t.Errorf("kit path remove printed\n%s exit %d", out, code)
	}
	w.expectSync(t, []string{"shared/declarations"}, "kit path remove /opt/homebrew/bin (laptop)")
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
// otherwise, and moved as written given its name alone; declared for this
// Mac, one the
// shared file has is refused, saying so.
func TestSharedTakesTheNameOutOfTheMacs(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "paths", "~/go/bin\n")
	w.writeSection(t, "studio", "checks", "dns -- dig example.com\n")
	w.writeSection(t, "laptop", "features", "remote-login\n")

	w.expectSync(t, []string{"laptop/declarations", "shared/declarations"}, "kit path add ~/go/bin (laptop)")
	if out, _, code := w.run(t, "path", "add", "~/go/bin", "--shared"); code != 0 || !strings.Contains(out, "~/go/bin ok declared in shared, out of laptop, last on the PATH") {
		t.Errorf("kit path add --shared printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "paths"); got != "" {
		t.Errorf("laptop's [paths] = %q, want ~/go/bin out of it", got)
	}

	w.fake.On("dig", "example.com")
	if out, _, code := w.run(t, "check", "add", "dns", "--shared", "--", "dig", "example.org"); code != 1 || !strings.Contains(out, "studio/declarations:5 has dns -- dig example.com, where this gives dns -- dig example.org: give only its name to move it as written, or take that line out first") {
		t.Errorf("kit check add --shared, another command than studio's, printed\n%s exit %d", out, code)
	}
	w.expectSync(t, []string{"shared/declarations", "studio/declarations"}, "kit check add dns (laptop)")
	if out, _, code := w.run(t, "check", "add", "dns", "--shared"); code != 0 || !strings.Contains(out, "dns ok declared in shared, out of studio; it passes now") {
		t.Errorf("kit check add dns --shared printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "shared", "checks"); got != "dns -- dig example.com\n" {
		t.Errorf("shared's [checks] = %q, want studio's line as written", got)
	}
	if got := w.readSection(t, "studio", "checks"); got != "" {
		t.Errorf("studio's [checks] = %q, want dns out of it", got)
	}

	w.expectSync(t, []string{"laptop/declarations", "shared/declarations"}, "kit feature on remote-login (laptop)")
	if out, _, code := w.run(t, "feature", "on", "remote-login", "--shared"); code != 0 || !strings.Contains(out, "remote-login ok on, in shared, out of laptop") {
		t.Errorf("kit feature on --shared printed\n%s exit %d", out, code)
	}
	if out, _, code := w.run(t, "feature", "on", "remote-login"); code != 0 || !strings.Contains(out, "remote-login ok on already, in the shared declarations") {
		t.Errorf("kit feature on, on for every Mac, printed\n%s exit %d", out, code)
	}
	if out, _, code := w.run(t, "check", "add", "dns", "--", "dig", "example.com"); code == 0 || !strings.Contains(out, "dns is in the shared declarations already (shared/declarations), for every Mac") {
		t.Errorf("kit check add of a shared check printed\n%s exit %d", out, code)
	}
}

// kit path add's --json has the shell's PATH written as a step: one that
// failed shows as attention, and kit exits 1 for it.
func TestAddPathJSONSaysTheShellsPathFailed(t *testing.T) {
	w := laptopWorld(t)
	path := filepath.Join(w.home, ".local/state/kit/path")
	if err := errors.Join(os.Remove(path), os.Mkdir(path, 0o700)); err != nil {
		t.Fatal(err)
	}
	w.expectSync(t, []string{"laptop/declarations"}, "kit path add ~/tools/bin (laptop)")
	out, _, code := w.run(t, "path", "add", "~/tools/bin", "--json")
	if code != 1 || !strings.Contains(out, `"attention": true`) || !strings.Contains(out, `"id": "path",`) {
		t.Errorf("kit path add --json, the PATH not written, printed\n%s exit %d; want the path step failed, attention", out, code)
	}
}
