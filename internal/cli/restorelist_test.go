package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/restore"
)

// kit restore add declares a path in this Mac's [restore], from ~, its
// --skips after it, quoted as a shell reads them, its note last, then
// commits and pushes; it copies nothing, and says so.
func TestRestoreAddDeclaresALine(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync(t, []string{"laptop/declarations"}, "kit restore add ~/Code (laptop): the code")
	out, errOut, code := w.run(t, "restore", "add", filepath.Join(w.home, "Code")+"/", "--skip", "logs", "--skip", "old builds", "--note", "the code")
	if code != 0 || !strings.Contains(out, "~/Code ok declared in laptop; nothing copied\n") {
		t.Errorf("kit restore add printed\n%s%s\nexit %d", out, errOut, code)
	}
	if got, want := w.readSection(t, "laptop", "restore"), "~/Code --skip logs --skip \"old builds\"   # the code\n"; got != want {
		t.Errorf("[restore] = %q, want %q", got, want)
	}
	cfg, err := config.Load(filepath.Join(w.home, ".config", "kit"), kinds.ConfigSections())
	if err != nil {
		t.Fatal(err)
	}
	list, err := cfg.List(config.RestoreKind, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := restore.Lines(list)
	if err != nil || len(lines) != 1 || lines[0].Path != "~/Code" || !slices.Equal(lines[0].Skips, []string{"logs", "old builds"}) {
		t.Errorf("the restore reads [restore] as %+v, %v; want ~/Code, skipping logs and old builds", lines, err)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".local", "state", "kit", "restore.json")); err == nil {
		t.Error("kit restore add recorded a restore: adding a line copies nothing")
	}
}

// A line the restore would refuse is refused before anything's written,
// in the restore's own words: one not in the home folder, a --skip leading
// out of its path, a path kit links, prefs saves, a kind puts in place,
// the backups leave out or kit keeps its own files in, or one in any of
// those; one another line holds or is in, case aside, the shared file's
// lines too; and one listed already, however that line spells it.
func TestRestoreAddRefusesWhatTheRestoreWould(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"outside the home folder", []string{"/Volumes/Data"}, "/Volumes/Data isn't in the home folder: a line's path starts ~/, as in ~/Code"},
		{"a skip leading out", []string{"~/Notes", "--skip", "../Code"}, "--skip ../Code: what to leave out is in the line's path, and .. leads out of it"},
		{"a skip absolute", []string{"~/Notes", "--skip", "/logs"}, "--skip /logs: what to leave out is relative to the line's path, never absolute"},
		{"a linked file", []string{"~/.zshrc"}, "laptop/declarations: the restore never carries ~/.zshrc: kit links ~/.zshrc"},
		{"in what prefs saves", []string{"~/.config/app/data"}, "laptop/declarations: the restore never carries ~/.config/app/data: prefs saves ~/.config/app"},
		{"kit's own folder", []string{"~/.config/kit"}, "laptop/declarations: the restore never carries ~/.config/kit: kit keeps its own files in ~/.config/kit"},
		{"in another line's", []string{"~/Code/site"}, "laptop/declarations: ~/Code/site and ~/Code (laptop/declarations:20) overlap: a path is restored by one line"},
		{"holding another line's, case aside", []string{"~/code"}, "laptop/declarations: ~/code and ~/Code (laptop/declarations:20) overlap: a path is restored by one line"},
		{"in the shared file's line", []string{"~/Documents/notes"}, "laptop/declarations: ~/Documents/notes and ~/Documents (shared/declarations:14) overlap: a path is restored by one line"},
		{"listed already", []string{"~/Code"}, "~/Code is in laptop/declarations already"},
		{"listed already, spelt otherwise", []string{"~/Ledgers"}, "laptop/declarations: ~/Ledgers is listed already, as ~/Ledgers/ (laptop/declarations:21)"},
		{"listed already for this Mac, declared for every Mac", []string{"~/Ledgers", "--shared"}, "on laptop, shared/declarations: ~/Ledgers is listed already, as ~/Ledgers/ (laptop/declarations:21)"},
		{"in what the backups leave out", []string{"~/Library/Caches/tool"}, "laptop/declarations: the restore never carries ~/Library/Caches/tool: the backups leave out ~/Library/Caches"},
		{"a skill's folder", []string{"~/.claude/skills/helper"}, "laptop/declarations: the restore never carries ~/.claude/skills/helper: kit puts ~/.claude/skills/helper in place"},
		{"a file secrets fill", []string{"~/.config/tool/token"}, "laptop/declarations: the restore never carries ~/.config/tool/token: kit puts ~/.config/tool/token in place"},
		{"kit's state", []string{"~/.local/state/kit"}, "laptop/declarations: the restore never carries ~/.local/state/kit: kit keeps its own files in ~/.local/state/kit"},
		{"in kit's data", []string{"~/.local/share/kit/prefs"}, "laptop/declarations: the restore never carries ~/.local/share/kit/prefs: kit keeps its own files in ~/.local/share/kit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := laptopWorld(t)
			w.write(t, ".config/kit/shared/home/.zshrc", "zsh\n")
			w.keeps("shared/home/.zshrc")
			w.writeSection(t, "laptop", "features", "prefs\n")
			w.writeSection(t, "laptop", "prefs files", "~/.config/app\n")
			w.writeSection(t, "shared", "restore", "~/Documents\n")
			w.writeSection(t, "laptop", "backup exclusions", "~/Library/Caches\n")
			w.writeSection(t, "laptop", "claude skills", "owner/repo/helper\n")
			w.writeSection(t, "laptop", "secrets", "~/.config/tool/token op://vault/item/token\n")
			w.writeSection(t, "laptop", "restore", "~/Code\n~/Ledgers/\n")
			was := w.read(t, ".config/kit/laptop/declarations")
			out, errOut, code := w.run(t, append([]string{"restore", "add"}, tt.args...)...)
			if code == 0 || !strings.Contains(out+errOut, tt.want) {
				t.Errorf("kit restore add %s printed\n%s%s\nexit %d, want %q", strings.Join(tt.args, " "), out, errOut, code, tt.want)
			}
			if got := w.read(t, ".config/kit/laptop/declarations"); got != was {
				t.Errorf("laptop's declarations became\n%s", got)
			}
		})
	}
}

// Declared for every Mac, a line must fit each Mac's restore: one another
// Mac's line holds is refused, naming that Mac; one that fits comes out of
// the Macs' files that had it.
func TestRestoreAddSharedFitsEveryMac(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "restore", "~/Notes\n")
	w.writeSection(t, "studio", "restore", "~/Ledgers/2026\n")
	out, errOut, code := w.run(t, "restore", "add", "~/Ledgers", "--shared")
	if want := "on studio, shared/declarations: ~/Ledgers and ~/Ledgers/2026 (studio/declarations:5) overlap: a path is restored by one line"; code == 0 || !strings.Contains(out, want) {
		t.Errorf("kit restore add --shared over studio's line printed\n%s%s\nexit %d, want %q", out, errOut, code, want)
	}
	w.expectSync(t, []string{"laptop/declarations", "shared/declarations"}, "kit restore add ~/Notes (laptop)")
	if out, errOut, code := w.run(t, "restore", "add", "~/Notes", "--shared"); code != 0 || !strings.Contains(out, "~/Notes ok declared in shared, out of laptop; nothing copied\n") {
		t.Errorf("kit restore add --shared printed\n%s%s\nexit %d", out, errOut, code)
	}
	if got := w.readSection(t, "shared", "restore"); got != "~/Notes\n" {
		t.Errorf("shared [restore] = %q", got)
	}
	if got := w.readSection(t, "laptop", "restore"); got != "" {
		t.Errorf("laptop's [restore] = %q, want ~/Notes out of it", got)
	}
}

// Declared for every Mac, a line that's kit's on another Mac is refused,
// naming that Mac: there, a file another Mac's folder links.
func TestRestoreAddSharedIsntKitsOnAnyMac(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, ".config/kit/studio/home/.notes", "notes\n")
	dir := filepath.Join(w.home, ".config", "kit")
	w.fake.On("git", "-C", dir, "--literal-pathspecs", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "studio/home").Prints("studio/home/.notes\x00")
	was := w.read(t, ".config/kit/shared/declarations")
	out, errOut, code := w.run(t, "restore", "add", "~/.notes", "--shared")
	if want := "on studio, shared/declarations: the restore never carries ~/.notes: kit links ~/.notes"; code == 0 || !strings.Contains(out, want) {
		t.Errorf("kit restore add --shared of studio's linked file printed\n%s%s\nexit %d, want %q", out, errOut, code, want)
	}
	if got := w.read(t, ".config/kit/shared/declarations"); got != was {
		t.Errorf("the shared declarations became\n%s", got)
	}
}

// kit restore remove takes a path out of this Mac's [restore], by the
// path as given, however its line spells it, then commits and pushes; one on every Mac's list needs
// --shared, and one not listed is refused.
func TestRestoreRemove(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "restore", "~/Code/ --skip logs\n~/Notes\n")
	w.writeSection(t, "shared", "restore", "~/Documents\n")
	w.expectSync(t, []string{"laptop/declarations"}, "kit restore remove ~/Code (laptop)")
	if out, errOut, code := w.run(t, "restore", "remove", filepath.Join(w.home, "Code")); code != 0 || !strings.Contains(out, "~/Code ok out of laptop\n") {
		t.Errorf("kit restore remove printed\n%s%s\nexit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "restore"); got != "~/Notes\n" {
		t.Errorf("laptop's [restore] = %q, want ~/Code out of it", got)
	}
	for _, tt := range []struct{ path, want string }{
		{"~/Documents", "on every Mac's restore list, in shared/declarations:14: --shared takes it out of every Mac's"},
		{"~/Music", "~/Music isn't on this Mac's restore list"},
	} {
		if out, _, code := w.run(t, "restore", "remove", tt.path); code == 0 || !strings.Contains(out, tt.want) {
			t.Errorf("kit restore remove %s printed\n%s\nexit %d, want %q", tt.path, out, code, tt.want)
		}
	}
	w.expectSync(t, []string{"shared/declarations"}, "kit restore remove ~/Documents (laptop)")
	if out, errOut, code := w.run(t, "restore", "remove", "~/Documents", "--shared"); code != 0 || !strings.Contains(out, "~/Documents ok out of shared\n") {
		t.Errorf("kit restore remove --shared printed\n%s%s\nexit %d", out, errOut, code)
	}
}

// kit restore add's help says a line copies nothing, how a Mac in use
// restores one, and that it never touches one holding data.
func TestRestoreAddHelpSaysItCopiesNothing(t *testing.T) {
	out, _, _ := laptopWorld(t).run(t, "restore", "add", "--help")
	if !strings.Contains(out, "Adding a line copies nothing. On a Mac in use, a path added later is\nrestored only by kit apply --restore-from time-machine|arq, and only while\nit's absent or empty: one holding data counts as here, and is never\ntouched.") {
		t.Errorf("kit restore add --help printed\n%s", out)
	}
}
