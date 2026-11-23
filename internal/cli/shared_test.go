package cli_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// soloWorld is laptopWorld without a shared folder, as a kit-config starts
// with nothing declared for every Mac: the shell's PATH as kit writes it,
// Homebrew's folders on it all the same.
func soloWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	if err := os.RemoveAll(filepath.Join(w.home, ".config", "kit", "shared")); err != nil {
		t.Fatal(err)
	}
	w.write(t, filepath.Join(".local", "state", "kit", "path"), "/opt/homebrew/bin:/opt/homebrew/sbin\n")
	return w
}

// noShared fails t when kit-config has a shared folder.
func (w *world) noShared(t *testing.T) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(w.home, ".config", "kit", "shared")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the shared folder: %v; want none", err)
	}
}

// A new kit-config declares no [paths], and the shell's PATH is to write
// all the same, as kit hasn't written it: the first kit apply writes
// Homebrew's folders into it.
func TestTheFirstApplyWritesHomebrewsFoldersForTheShell(t *testing.T) {
	w := soloWorld(t)
	if err := os.Remove(filepath.Join(w.home, ".local", "state", "kit", "path")); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := w.run(t, "status"); !strings.Contains(out, "path ok kit hasn't written it\n") || !strings.Contains(out, "path missing:new the shell's PATH (kit hasn't written it) (to write)\n") {
		t.Errorf("kit status, the shell's PATH never written, printed\n%s", out)
	}
	w.fake.On("brew", "uninstall", "--formula", "ffmpeg")
	if out, errOut, code := w.run(t, "apply"); code != 0 || !strings.Contains(out, "path wrote the shell's PATH\n") {
		t.Errorf("kit apply printed\n%s%s exit %d", out, errOut, code)
	}
	if got, want := w.read(t, filepath.Join(".local", "state", "kit", "path")), "/opt/homebrew/bin:/opt/homebrew/sbin\n"; got != want {
		t.Errorf("the shell's PATH = %q, want %q", got, want)
	}
}

// A kit-config with no shared folder is read as one whose shared file
// declares nothing: kit status, kit apply and kit machine add, with and
// without --from, work as ever, and none of them makes the folder.
func TestAConfigWithoutASharedFolder(t *testing.T) {
	w := soloWorld(t)
	if out, errOut, code := w.run(t, "status"); code != 0 || errOut != "" || !strings.Contains(out, "path ok 2 directories\n") || !strings.Contains(out, "brew attention 1 declared, all installed\n") {
		t.Errorf("kit status printed\n%s%s exit %d", out, errOut, code)
	}
	w.fake.On("brew", "uninstall", "--formula", "ffmpeg")
	if out, errOut, code := w.run(t, "apply"); code != 0 || errOut != "" {
		t.Errorf("kit apply printed\n%s%s exit %d", out, errOut, code)
	}
	w.expectCommit(t, commit{
		message: "kit machine add mini (laptop)", paths: []string{"kit.toml", "mini"},
		status: " M kit.toml\x00?? mini/declarations\x00", files: []string{"kit.toml", "mini/declarations"},
	})
	if out, errOut, code := w.run(t, "machine", "add", "mini"); code != 0 || !strings.Contains(out, "mini ok added to kit-config\n") {
		t.Errorf("kit machine add printed\n%s%s exit %d", out, errOut, code)
	}
	w.expectCommit(t, commit{
		message: "kit machine add office (laptop)", paths: []string{"kit.toml", "office"},
		status: " M kit.toml\x00?? office/declarations\x00", files: []string{"kit.toml", "office/declarations"},
	})
	if out, errOut, code := w.run(t, "machine", "add", "office", "--from", "laptop"); code != 0 || !strings.Contains(out, "office ok added to kit-config, a copy of laptop\n") {
		t.Errorf("kit machine add --from printed\n%s%s exit %d", out, errOut, code)
	}
	w.noShared(t)
}

// The first thing declared for every Mac makes the shared folder, and the
// file it goes in, as kit writes any: a line in its section of the shared
// declarations, or a step's folder.
func TestTheFirstSharedDeclarationMakesTheSharedFolder(t *testing.T) {
	for _, tt := range []struct {
		name, message string
		args          []string
		// paths are what kit commits, and made the files it makes in them.
		paths, made []string
		want        map[string]string
	}{
		{name: "a folder on the PATH", args: []string{"path", "add", "~/bin", "--shared"}, message: "kit path add ~/bin (laptop)",
			paths: []string{"shared/declarations"}, made: []string{"shared/declarations"},
			want: map[string]string{"shared/declarations": "[paths]\n~/bin\n"}},
		{name: "a package", args: []string{"brew", "add", "jq", "--shared"}, message: "kit brew add jq (laptop)",
			paths: []string{"shared/declarations"}, made: []string{"shared/declarations"},
			want: map[string]string{"shared/declarations": "[homebrew formulae]\njq\n"}},
		{name: "a feature", args: []string{"feature", "on", "remote-login", "--shared"}, message: "kit feature on remote-login (laptop)",
			paths: []string{"shared/declarations"}, made: []string{"shared/declarations"},
			want: map[string]string{"shared/declarations": "[features]\nremote-login\n"}},
		{name: "a step", args: []string{"step", "add", "tool", "Installs the tool", "--shared"}, message: "kit step add tool (laptop)",
			paths: []string{"shared/declarations", "shared/steps/tool"}, made: []string{"shared/declarations", "shared/steps/tool/run"},
			want: map[string]string{"shared/declarations": "[steps]\ntool \"Installs the tool\"\n"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := soloWorld(t)
			c := commit{message: tt.message, paths: tt.paths, files: tt.made}
			for _, f := range tt.made {
				c.status += "?? " + f + "\x00"
			}
			w.expectCommit(t, c)
			if out, errOut, code := w.run(t, tt.args...); code != 0 {
				t.Fatalf("kit %s printed\n%s%s exit %d", strings.Join(tt.args, " "), out, errOut, code)
			}
			for file, want := range tt.want {
				if got := w.read(t, filepath.Join(".config", "kit", file)); got != want {
					t.Errorf("%s = %q, want %q", file, got, want)
				}
			}
		})
	}
}

// A file every Mac links makes the shared folder too: its home folder,
// holding the file.
func TestTheFirstSharedFileMakesTheSharedFolder(t *testing.T) {
	w := soloWorld(t)
	w.write(t, ".zshrc", "zsh\n")
	w.fake.On("git", "-C", filepath.Join(w.home, ".config", "kit"), "check-ignore", "--verbose", "--stdin", "-z").Exits(1)
	w.expectCommit(t, commit{
		message: "kit file add ~/.zshrc (laptop)", paths: []string{"shared/home/.zshrc"},
		status: "?? shared/home/.zshrc\x00", files: []string{"shared/home/.zshrc"},
	})
	if out, errOut, code := w.run(t, "file", "add", filepath.Join(w.home, ".zshrc"), "--shared"); code != 0 || !strings.Contains(out, "in kit-config, linked: shared/home/.zshrc") {
		t.Errorf("kit file add --shared printed\n%s%s exit %d", out, errOut, code)
	}
	if w.read(t, ".config/kit/shared/home/.zshrc") != "zsh\n" || w.linksTo(".zshrc") == "" {
		t.Error("the file isn't in kit-config's shared home folder, linked back")
	}
}
