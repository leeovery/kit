package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// filesWorld is laptopWorld with files to link: the shared ~/.zshrc, the
// laptop's ~/.config/tool/conf, and a ~/.differs of the Mac's own where
// kit-config's belongs.
func filesWorld(t *testing.T) *world {
	t.Helper()
	w := laptopWorld(t)
	w.write(t, ".config/kit/shared/home/.zshrc", "zsh\n")
	w.write(t, ".config/kit/shared/home/.differs", "kit's\n")
	w.write(t, ".config/kit/laptop/home/.config/tool/conf", "conf\n")
	w.write(t, ".differs", "the Mac's\n")
	w.keeps("shared/home/.differs", "shared/home/.zshrc", "laptop/home/.config/tool/conf")
	return w
}

// keeps scripts git listing paths, in the config repository, as the files
// it keeps in the home folders.
func (w *world) keeps(paths ...string) {
	dir := filepath.Join(w.home, ".config", "kit")
	var folders []string
	for _, scope := range []string{"shared", "laptop"} {
		if _, err := os.Stat(filepath.Join(dir, scope, "home")); err == nil {
			folders = append(folders, scope+"/home")
		}
	}
	args := append([]string{"-C", dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}, folders...)
	w.fake.On("git", args...).Prints(strings.Join(paths, "\x00") + "\x00")
}

// linksTo is where the link at path, from the home, leads: "" when it
// isn't a link.
func (w *world) linksTo(path string) string {
	target, err := os.Readlink(filepath.Join(w.home, path))
	if err != nil {
		return ""
	}
	return target
}

func TestStatusShowsLinkedFiles(t *testing.T) {
	out, _, _ := filesWorld(t).run(t, "status")
	for _, want := range []string{
		"file ok 0 of 3 linked\n",
		"file missing:new ~/.config/tool/conf, ~/.zshrc (to link)\n",
		"file diverged:new ~/.differs (a file of its own, different from shared/home/.differs)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kit status printed\n%s\nwant %q", out, want)
		}
	}
}

func TestApplyLinksFilesAndLeavesADifferentOne(t *testing.T) {
	w := filesWorld(t)
	out, _, _ := w.run(t, "apply", "file")
	repo := filepath.Join(w.home, ".config", "kit")
	if w.linksTo(".zshrc") != filepath.Join(repo, "shared/home/.zshrc") || w.linksTo(".config/tool/conf") != filepath.Join(repo, "laptop/home/.config/tool/conf") {
		t.Errorf("kit apply file printed\n%s\nand didn't link the files", out)
	}
	if w.linksTo(".differs") != "" || w.read(t, ".differs") != "the Mac's\n" {
		t.Error("kit apply overwrote a different file")
	}
}

func TestReconcileRevertsADivergedFile(t *testing.T) {
	w := filesWorld(t)
	out, _, code := w.run(t, "reconcile", "file:~/.differs", "--revert")
	if code != 0 || !strings.Contains(out, "linked to kit-config's; the Mac's copy is in the Bin (~/.Trash/.differs)") {
		t.Errorf("kit reconcile printed\n%s exit %d", out, code)
	}
	if w.read(t, ".Trash/.differs") != "the Mac's\n" || w.linksTo(".differs") == "" {
		t.Error("the Mac's copy isn't in the Bin, with kit-config's linked")
	}
}

func TestReconcileAdoptsADivergedFile(t *testing.T) {
	w := filesWorld(t)
	w.expectSync([]string{"shared/home/.differs"}, "kit reconcile (laptop): adopt file:~/.differs")
	out, _, code := w.run(t, "reconcile", "file:~/.differs", "--adopt")
	if code != 0 || !strings.Contains(out, "kit-config ok committed and pushed shared/home/.differs") {
		t.Errorf("kit reconcile printed\n%s exit %d", out, code)
	}
	if w.read(t, ".config/kit/shared/home/.differs") != "the Mac's\n" || w.linksTo(".differs") == "" {
		t.Error("the Mac's copy isn't in kit-config, linked")
	}
}

func TestReconcileOffersWhatSuitsAFile(t *testing.T) {
	_, errOut, code := filesWorld(t).run(t, "reconcile", "file:~/.differs", "--remove")
	if code != 2 || !strings.Contains(errOut, "file:~/.differs can't be removed: --adopt, --revert or --snooze") {
		t.Errorf("kit reconcile printed %q, exit %d", errOut, code)
	}
}

func TestAddFileMovesItIntoKitConfig(t *testing.T) {
	w := filesWorld(t)
	w.write(t, ".config/new/conf", "new\n")
	w.expectSync([]string{"laptop/home/.config/new/conf"}, "kit file add ~/.config/new/conf (laptop)")
	out, _, code := w.run(t, "file", "add", filepath.Join(w.home, ".config/new/conf"))
	if code != 0 || !strings.Contains(out, "in kit-config, linked: laptop/home/.config/new/conf") {
		t.Errorf("kit file add printed\n%s exit %d", out, code)
	}
	if w.read(t, ".config/kit/laptop/home/.config/new/conf") != "new\n" || w.linksTo(".config/new/conf") == "" {
		t.Error("the file isn't in kit-config, linked back")
	}
}

func TestRemoveAFileEveryMacLinksNeedsShared(t *testing.T) {
	w := filesWorld(t)
	w.run(t, "apply", "file")
	out, _, code := w.run(t, "file", "remove", "~/.zshrc")
	if code == 0 || !strings.Contains(out, "linked on every Mac, from shared/home/.zshrc: --shared takes it out of every Mac's") {
		t.Errorf("kit file remove printed\n%s exit %d", out, code)
	}
	w.expectSync([]string{"shared/home/.zshrc"}, "kit file remove ~/.zshrc (laptop)")
	out, _, code = w.run(t, "file", "remove", "~/.zshrc", "--shared")
	if code != 0 || w.linksTo(".zshrc") != "" || w.read(t, ".zshrc") != "zsh\n" {
		t.Errorf("kit file remove --shared printed\n%s exit %d; want a copy back in place of the link", out, code)
	}
}

func TestReconcileAtATerminalOffersAFilesChoices(t *testing.T) {
	w := filesWorld(t)
	w.terminal = true
	var offered []string
	w.choose = func(question string, options []string) (int, error) {
		if strings.HasPrefix(question, "~/.differs  file · a different file where its link belongs") {
			offered = options
			for i, o := range options {
				if o == "Revert" {
					return i, nil
				}
			}
		}
		for i, o := range options {
			if o == "Skip" {
				return i, nil
			}
		}
		return 0, nil
	}
	w.run(t, "reconcile", "--all", "file")
	want := []string{"Adopt", "Revert", "Snooze", "Skip"}
	if strings.Join(offered, "|") != strings.Join(want, "|") {
		t.Errorf("offered %q, want %q", offered, want)
	}
	if w.read(t, ".Trash/.differs") != "the Mac's\n" {
		t.Error("the answer wasn't carried out")
	}
}

func TestWhyOfALinkedFile(t *testing.T) {
	w := filesWorld(t)
	w.run(t, "apply", "file")
	out, _, _ := w.run(t, "why", "~/.zshrc")
	if want := "~/.zshrc (file)\n  linked from shared/home/.zshrc\n  linked here\n"; out != want {
		t.Errorf("kit why printed\n%s\nwant\n%s", out, want)
	}
	out, _, _ = w.run(t, "why", filepath.Join(w.home, ".differs"))
	if !strings.Contains(out, "~/.differs (file)\n  linked from shared/home/.differs\n  not linked here: kit status says why") {
		t.Errorf("kit why printed\n%s", out)
	}
}
