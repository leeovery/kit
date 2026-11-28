package snapshot_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/backup/arq"
	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/kind/defaults"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/snapshot"
)

// write writes files, by path in dir, making their folders.
func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// app is an app's Info.plist, its bundle id id.
func app(id string) string {
	return `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>` + id + `</string></dict></plist>`
}

// newMac is the Mac laptop, as the config, laid out in its home folder,
// knows it, declaring shared, with nothing on it but macOS: every watched
// domain empty, macOS 26, no feature on, no backup set up, and no program
// of any kind but defaults'.
func newMac(t *testing.T, shared string) (snapshot.Mac, *runnertest.Fake) {
	t.Helper()
	home := t.TempDir()
	dirs, err := config.Locate(home, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	write(t, dirs.Config, map[string]string{
		config.File:           "format = 1\nprimary = \"laptop\"\n\n[macs.laptop]\n\n[macs.studio]\n",
		"shared/declarations": shared,
	})
	cfg, err := config.Load(dirs.Config, kinds.Sections())
	if err != nil {
		t.Fatal(err)
	}
	fake := runnertest.New(t)
	fake.On("sw_vers", "-productVersion").Prints("26.1\n")
	for _, domain := range defaults.Watched {
		setting(fake, domain, "")
		setting(fake, "currentHost:"+domain, "")
	}
	fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n}\n")
	fake.On("defaults", "export", timemachine.Prefs, "-").Prints("<plist><dict/></plist>")
	fake.On(arq.Arqc, "stats").Fails(runner.ErrNotFound)
	return snapshot.Mac{
		Name: "laptop", Config: cfg, Run: fake, Home: home, Dirs: dirs,
		AppDirs: []string{t.TempDir()}, SudoLocal: filepath.Join(t.TempDir(), "sudo_local"),
		Now: func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) },
	}, fake
}

// homebrew scripts Homebrew with the formulae and casks given, one a line,
// every formula a leaf installed for itself, but those in deps. Nothing
// else is scripted: a snapshot never asks brew about Homebrew's own
// packages, which can't be private, nor taps or updates anything.
func homebrew(fake *runnertest.Fake, formulae, deps, casks string) {
	fake.On("brew", "list", "--formula", "--full-name", "-1").Prints(formulae + deps)
	fake.On("brew", "leaves").Prints(formulae)
	fake.On("brew", "leaves", "--installed-on-request").Prints(formulae)
	fake.On("brew", "list", "--cask", "--full-name", "-1").Prints(casks)
	fake.On("brew", "--caskroom").Prints(filepath.Join(os.TempDir(), "kit-no-caskroom") + "\n")
}

// setting scripts defaults export for id, currentHost: first for one of
// this host, as a dict of body.
func setting(fake *runnertest.Fake, id, body string) {
	args := []string{"export", strings.TrimPrefix(id, "currentHost:"), "-"}
	if strings.HasPrefix(id, "currentHost:") {
		args = append([]string{"-currentHost"}, args...)
	}
	fake.On("defaults", args...).Prints("<plist version=\"1.0\"><dict>" + body + "</dict></plist>")
}

// declarations is the Mac's declarations file.
func declarations(t *testing.T, m snapshot.Mac) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(m.Config.Dir, config.DeclFile(m.Name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// counts are each part's kind and how many lines it declares, and its
// error.
func counts(s snapshot.Snapshot) []string {
	var out []string
	for _, p := range s.Parts {
		line := fmt.Sprintf("%s %d", p.Kind, len(p.Lines))
		if p.Err != nil {
			line += ": " + p.Err.Error()
		}
		out = append(out, line)
	}
	return out
}

// take finds what a snapshot of m declares, and declares it.
func take(t *testing.T, m snapshot.Mac) snapshot.Snapshot {
	t.Helper()
	s, _, err := snapshot.Find(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Write(m, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// A Mac in use is declared as it stands, in one edit of its own file: each
// kind whose program is here, with its section, what's installed for
// itself and not declared already, as adopting it declares it; its macOS
// and git settings; the features on, and the backup tools set up.
func TestASnapshotDeclaresTheMacAsItStands(t *testing.T) {
	m, fake := newMac(t, "[features]\nremote-login\n\n[homebrew formulae]\njq\n")
	homebrew(fake, "jq\nowner/tap/tool\nripgrep\n", "libyaml\n", "ghostty\n")
	tapTool(fake, "200")
	fake.On("git", "config", "--global", "--list", "-z").Prints("user.name\nSome One\x00core.editor\nmicro\x00include.path\na\x00include.path\nb\x00")
	setting(fake, "com.apple.dock", "<key>tilesize</key><integer>60</integer><key>version</key><integer>1</integer>")
	fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n\t\"com.openssh.sshd\" => enabled\n\t\"com.apple.smbd\" => disabled\n\t\"com.apple.screensharing\" => enabled\n}\n")
	write(t, filepath.Dir(m.SudoLocal), map[string]string{"sudo_local": "auth       sufficient     pam_tid.so\n"})
	fake.On("defaults", "export", timemachine.Prefs, "-").Prints("<plist><dict><key>AutoBackup</key><true/><key>Destinations</key><array><dict/></array></dict></plist>")
	fake.On("tmutil", "status").Prints("Running = 0;\n")
	fake.On("tmutil", "destinationinfo").Prints("Mount Point   : /Volumes/Backups\n")

	s := take(t, m)
	want := "[features]\nscreen-sharing\ntouch-id-sudo\n\n[backups]\ntime-machine\n\n[git config]\ncore.editor micro\nuser.name \"Some One\"\n\n" +
		"[macos settings]\ncom.apple.dock tilesize -int 60\n\n[homebrew formulae]\nripgrep\nowner/tap/tool\n\n[homebrew casks]\nghostty\n"
	if got := declarations(t, m); got != want {
		t.Errorf("laptop/declarations =\n%s\nwant\n%s", got, want)
	}
	wantCounts := []string{"brew 2", "cask 1", "git-config 2", "defaults 1", "features 2", "backups 1"}
	if got := counts(s); !slices.Equal(got, wantCounts) {
		t.Errorf("parts = %q, want %q: no part for a kind whose program isn't here", got, wantCounts)
	}
	git := s.Parts[2]
	if len(git.Refused) != 1 || git.Refused[0].Entry.Name != "include.path" || !strings.Contains(git.Refused[0].Err.Error(), "set 2 times") {
		t.Errorf("git's refused = %+v, want include.path, set twice, left out saying so", git.Refused)
	}
}

// tapTool scripts owner/tap/tool, a formula of a tap added already, whose
// download answers, asked for without a token, as answer says.
func tapTool(fake *runnertest.Fake, answer string) {
	const download = "https://github.com/owner/tool/releases/download/v1/tool.tar.gz"
	fake.On("brew", "info", "--json=v2", "--formula", "owner/tap/tool").Prints(`{"formulae": [{"name": "tool", "full_name": "owner/tap/tool", "urls": {"stable": {"url": "` + download + `"}}}], "casks": []}`)
	fake.On("curl", "-q", "-sS", "-o", "/dev/null", "-w", "%{http_code} %{url_effective}", "-L", "-r", "0-0", "--max-time", "10", download).Prints(answer + " " + download)
}

// A tap's package whose download is private is held for Homebrew's GitHub
// token, as adopting it is: refused, saying how to declare the token, while
// the Mac doesn't declare it; in the section held for it once it does.
func TestAPrivatePackageIsHeldForItsToken(t *testing.T) {
	for _, tt := range []struct {
		name, shared, want string
	}{
		{"the token not declared", "", "[homebrew formulae]\nripgrep\n"},
		{"the token declared", "[secrets]\nHOMEBREW_GITHUB_API_TOKEN op://vault/item/field\n",
			"[homebrew formulae]\nripgrep\n\n[homebrew formulae --secret HOMEBREW_GITHUB_API_TOKEN]\nowner/tap/tool\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, fake := newMac(t, tt.shared)
			homebrew(fake, "owner/tap/tool\nripgrep\n", "", "")
			tapTool(fake, "404")
			s := take(t, m)
			if got := declarations(t, m); !strings.Contains(got, tt.want) || strings.Count(got, "owner/tap/tool") > 1 {
				t.Errorf("laptop/declarations =\n%s\nwant it to hold\n%s", got, tt.want)
			}
			if refused := s.Parts[0].Refused; tt.shared == "" && (len(refused) != 1 || !strings.Contains(refused[0].Err.Error(), "kit secret add HOMEBREW_GITHUB_API_TOKEN")) {
				t.Errorf("refused = %+v, want owner/tap/tool, naming kit secret add", refused)
			}
		})
	}
}

// A part that can't be looked at says why, and declares nothing, not even
// its section; the rest are declared.
func TestAPartThatCantBeLookedAtSaysWhy(t *testing.T) {
	m, fake := newMac(t, "")
	homebrew(fake, "", "", "ghostty\n")
	fake.On("brew", "list", "--formula", "--full-name", "-1").Exits(1).PrintsToStderr("Error: something broke\n")
	s := take(t, m)
	if p := s.Parts[0]; p.Kind != "brew" || p.Err == nil || len(p.Lines) != 0 {
		t.Errorf("brew's part = %+v, want why it couldn't be looked at", p)
	}
	if got, want := declarations(t, m), "[macos settings]\n\n[homebrew casks]\nghostty\n"; got != want {
		t.Errorf("laptop/declarations =\n%s\nwant\n%s", got, want)
	}
}

// Looks is told of every part waiting its turn before any is looked at,
// then of each as it's looked at, and once it has been, what it found, or
// why it couldn't be looked at; the features and backup tools by name.
func TestFindSaysHowEachPartStandsAsItLooks(t *testing.T) {
	m, fake := newMac(t, "")
	homebrew(fake, "", "", "ghostty\n")
	fake.On("brew", "list", "--formula", "--full-name", "-1").Exits(1).PrintsToStderr("Error: something broke\n")
	fake.On("launchctl", "print-disabled", "system").Prints("disabled services = {\n\t\"com.openssh.sshd\" => enabled\n}\n")
	write(t, filepath.Dir(m.SudoLocal), map[string]string{"sudo_local": "auth       sufficient     pam_tid.so\n"})
	fake.On("defaults", "export", timemachine.Prefs, "-").Prints("<plist><dict><key>AutoBackup</key><true/><key>Destinations</key><array><dict/></array></dict></plist>")
	fake.On("tmutil", "status").Prints("Running = 0;\n")
	fake.On("tmutil", "destinationinfo").Prints("Mount Point   : /Volumes/Backups\n")
	var mu sync.Mutex
	var looks []snapshot.Look
	m.Looks = func(l snapshot.Look) {
		mu.Lock()
		defer mu.Unlock()
		looks = append(looks, l)
	}
	take(t, m)
	labels := []string{"Homebrew formulae", "Homebrew casks", "macOS settings", "Features", "Backups"}
	if len(looks) < 3*len(labels) {
		t.Fatalf("told %+v: want each of %q waiting, looked at, and looked", looks, labels)
	}
	for i, label := range labels {
		if l := looks[i]; l.Label != label || l.Doing != "" || l.Looked {
			t.Errorf("told %+v at %d: want %s waiting its turn", l, i, label)
		}
		at := slices.IndexFunc(looks, func(l snapshot.Look) bool { return l.Label == label && l.Doing != "" })
		done := slices.IndexFunc(looks, func(l snapshot.Look) bool { return l.Label == label && l.Looked })
		if at < len(labels) || done < at {
			t.Errorf("told %+v: want %s looked at, then looked", looks, label)
		}
	}
	for _, l := range looks {
		switch {
		case l.Label == "Homebrew casks" && l.Looked && (l.Found != 1 || l.Names != nil),
			l.Label == "Homebrew formulae" && l.Looked && l.Err == nil,
			l.Label == "Features" && l.Looked && !slices.Equal(l.Names, []string{"Touch ID for sudo", "Remote Login"}),
			l.Label == "Backups" && l.Looked && !slices.Equal(l.Names, []string{"Time Machine"}):
			t.Errorf("told %+v: want casks' one found, formulae's error, the features on and backup tools set up by name", l)
		}
	}
}

// A line the config refuses, as one declared for every Mac already, is
// left out, its part saying why, and the rest written.
func TestALineTheConfigRefusesIsLeftOut(t *testing.T) {
	m, _ := newMac(t, "[homebrew formulae]\njq\n")
	s := snapshot.Snapshot{Parts: []snapshot.Part{{Kind: "brew", Lines: []config.Entry{{Name: "jq"}, {Name: "bat"}}}}}
	if err := snapshot.Write(m, &s); err != nil {
		t.Fatal(err)
	}
	p := s.Parts[0]
	if len(p.Lines) != 1 || p.Lines[0].Name != "bat" || len(p.Refused) != 1 || p.Refused[0].Entry.Name != "jq" {
		t.Errorf("part = %+v, want bat declared, jq refused", p)
	}
	if got, want := declarations(t, m), "[homebrew formulae]\nbat\n"; got != want {
		t.Errorf("laptop/declarations =\n%s\nwant\n%s", got, want)
	}
}

// The casks that take over apps installed by hand are declared among the
// casks' lines, once each, those the snapshot holds already as they are;
// with no casks' part looked at, as one of their own.
func TestTheCasksAdoptedAreDeclaredWithTheRest(t *testing.T) {
	for _, tt := range []struct {
		name  string
		parts []snapshot.Part
		want  []string
	}{
		{"among the casks", []snapshot.Part{{Kind: "brew"}, {Kind: "cask", Lines: []config.Entry{{Name: "ghostty"}, {Name: "tally"}}}}, []string{"brew 0", "cask 3"}},
		{"the casks not looked at", []snapshot.Part{{Kind: "cask", Err: errors.New("brew broke")}}, []string{"cask 0: brew broke", "cask 2"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newMac(t, "")
			s := snapshot.Snapshot{Parts: tt.parts}
			s.Adopt([]string{"tally", "editor", "editor"})
			if got := counts(s); !slices.Equal(got, tt.want) {
				t.Errorf("parts = %q, want %q", got, tt.want)
			}
			if err := snapshot.Write(m, &s); err != nil {
				t.Fatal(err)
			}
			if got := declarations(t, m); !strings.Contains(got, "[homebrew casks]\neditor\n") || strings.Count(got, "tally") != 1 {
				t.Errorf("laptop/declarations =\n%s\nwant editor and tally declared, once each", got)
			}
		})
	}
}

// The questions offer the home folder's own folders a new Mac would bring
// back, sized, the person's work ticked and downloads and media not;
// never the Library, a hidden or kit's folder, a link, nor one holding
// nothing. A folder not counted whole in the time given says as much.
func TestTheDataFoldersOffered(t *testing.T) {
	m, _ := newMac(t, "")
	write(t, m.Home, map[string]string{
		"Code/site/index.html": strings.Repeat("x", 10000), "Documents/notes.txt": "notes",
		"Downloads/file.zip": "zip", "Library/Preferences/x.plist": "x", ".cache/x": "x", "Applications/Tool.app/x": "x",
	})
	for _, dir := range []string{"Movies", "Public"} {
		if err := os.Mkdir(filepath.Join(m.Home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(m.Home, "Code"), filepath.Join(m.Home, "Elsewhere")); err != nil {
		t.Fatal(err)
	}
	s, _, err := snapshot.Find(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range s.Folders {
		got = append(got, fmt.Sprintf("%s whole:%v ticked:%v", f.Path, f.Whole, f.Ticked))
		if f.Path == "~/Code" && f.Size < 10000 {
			t.Errorf("~/Code is %d bytes, want at least the 10000 in it", f.Size)
		}
	}
	want := []string{"~/Code whole:true ticked:true", "~/Documents whole:true ticked:true", "~/Downloads whole:true ticked:false"}
	if !slices.Equal(got, want) {
		t.Errorf("folders = %q, want %q", got, want)
	}

	m.SizeFor = time.Nanosecond
	if s, _, err = snapshot.Find(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	for _, f := range s.Folders {
		if f.Whole {
			t.Errorf("%s counted whole with no time to count it", f.Path)
		}
	}
}

// The questions offer the shell's and tools' files that are the Mac's own,
// at their usual places: never a link, nor git's settings file, which
// git's settings, a kind, own.
func TestTheFilesOfferedToLink(t *testing.T) {
	m, _ := newMac(t, "")
	write(t, m.Home, map[string]string{".zshrc": "export A=1\n", ".gitconfig": "[user]\n", ".config/ghostty/config": "theme = dark\n", ".ssh/config": "Host *\n"})
	if err := os.Symlink(filepath.Join(m.Home, ".zshrc"), filepath.Join(m.Home, ".zprofile")); err != nil {
		t.Fatal(err)
	}
	s, _, err := snapshot.Find(t.Context(), m)
	if want := []string{"~/.zshrc", "~/.ssh/config", "~/.config/ghostty/config"}; err != nil || !slices.Equal(s.Files, want) {
		t.Errorf("files = %q, %v; want %q", s.Files, err, want)
	}
}

// A file kit owns already, as one a secret fills, is never offered to
// link: a path has one owner.
func TestAFileKitOwnsIsntOfferedToLink(t *testing.T) {
	m, _ := newMac(t, "[secrets]\n~/.ssh/config op://vault/item/config\n")
	write(t, m.Home, map[string]string{".zshrc": "export A=1\n", ".ssh/config": "Host *\n"})
	s, _, err := snapshot.Find(t.Context(), m)
	if want := []string{"~/.zshrc"}; err != nil || !slices.Equal(s.Files, want) {
		t.Errorf("files = %q, %v; want %q, the secret's file left out", s.Files, err, want)
	}
}

// A folder part of which macOS won't let the run read isn't whole, saying
// why; one whose own listing can't be read is offered all the same, as
// it may hold the person's data.
func TestAFolderThatCantBeReadIsntWhole(t *testing.T) {
	m, _ := newMac(t, "")
	write(t, m.Home, map[string]string{"Documents/notes.txt": "notes", "Documents/Private/a.txt": "a", "Locked/b.txt": "b"})
	for _, dir := range []string{"Documents/Private", "Locked"} {
		path := filepath.Join(m.Home, dir)
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
	}
	s, _, err := snapshot.Find(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range s.Folders {
		got = append(got, fmt.Sprintf("%s whole:%v err:%v", f.Path, f.Whole, f.Err != nil))
	}
	if want := []string{"~/Documents whole:false err:true", "~/Locked whole:false err:true"}; !slices.Equal(got, want) {
		t.Errorf("folders = %q, want %q", got, want)
	}
}

// Without the network, whether a tap's package needs a secret can't be
// told: it's declared as it is, and its part says so, and why.
func TestAPackageThatCouldntBeCheckedSaysSo(t *testing.T) {
	m, fake := newMac(t, "")
	homebrew(fake, "owner/tap/tool\n", "", "")
	const download = "https://github.com/owner/tool/releases/download/v1/tool.tar.gz"
	fake.On("brew", "info", "--json=v2", "--formula", "owner/tap/tool").Prints(`{"formulae": [{"name": "tool", "full_name": "owner/tap/tool", "urls": {"stable": {"url": "` + download + `"}}}], "casks": []}`)
	fake.On("curl", "-q", "-sS", "-o", "/dev/null", "-w", "%{http_code} %{url_effective}", "-L", "-r", "0-0", "--max-time", "10", download).Prints("000 " + download).Exits(6)
	s := take(t, m)
	if got := declarations(t, m); !strings.Contains(got, "[homebrew formulae]\nowner/tap/tool\n") {
		t.Errorf("laptop/declarations =\n%s\nwant owner/tap/tool declared as it is", got)
	}
	if u := s.Parts[0].Unchecked; len(u) != 1 || u[0].Name != "owner/tap/tool" || u[0].Err == nil {
		t.Errorf("unchecked = %+v, want owner/tap/tool, saying why", u)
	}
}

// A file of several hard links counts once, as du counts it, the folder's
// own blocks with it.
func TestAHardLinkedFileCountsOnce(t *testing.T) {
	m, _ := newMac(t, "")
	write(t, m.Home, map[string]string{"Code/a.bin": strings.Repeat("x", 100000)})
	if err := os.Link(filepath.Join(m.Home, "Code", "a.bin"), filepath.Join(m.Home, "Code", "b.bin")); err != nil {
		t.Fatal(err)
	}
	var want int64
	for _, path := range []string{"Code", "Code/a.bin"} {
		info, err := os.Lstat(filepath.Join(m.Home, path))
		if err != nil {
			t.Fatal(err)
		}
		want += info.Sys().(*syscall.Stat_t).Blocks * 512
	}
	s, _, err := snapshot.Find(t.Context(), m)
	if err != nil || len(s.Folders) != 1 || s.Folders[0].Size != want {
		t.Errorf("folders = %+v, %v; want ~/Code, %d bytes", s.Folders, err, want)
	}
}

// The snapshot carries the apps installed by hand, each with the cask that
// installs it, as Homebrew's catalogue has it; when the catalogue can't be
// read, the apps all the same, with no cask, and why.
func TestTheAppsInstalledByHand(t *testing.T) {
	for _, tt := range []struct {
		name      string
		catalogue map[string]string
		cask      string
		why       bool
	}{
		{"catalogued", map[string]string{"api/internal/packages.arm64_tahoe.jws.json.payload": `{"protected":"e30","signature":"c2ln"}
{"casks": {"google-chrome": {"auto_updates": true, "raw_artifacts": [[":uninstall", {":quit": "com.google.Chrome"}], [":app", ["Google Chrome.app"]]]}}}`}, "google-chrome", false},
		{"no catalogue", nil, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, fake := newMac(t, "")
			apps := m.AppDirs[0]
			write(t, apps, map[string]string{
				"Google Chrome.app/Contents/Info.plist": app("com.google.Chrome"),
				"Tool.app/Contents/Info.plist":          app("com.example.tool"),
			})
			homebrew(fake, "", "", "")
			cache := t.TempDir()
			write(t, cache, tt.catalogue)
			fake.On("brew", "--cache").Prints(cache + "\n")
			s, _, err := snapshot.Find(t.Context(), m)
			want := []brew.ByHand{
				{Path: filepath.Join(apps, "Google Chrome.app"), Name: "Google Chrome", ID: "com.google.Chrome", Cask: tt.cask},
				{Path: filepath.Join(apps, "Tool.app"), Name: "Tool", ID: "com.example.tool"},
			}
			if err != nil || s.ByHandErr != nil || !slices.Equal(s.ByHand, want) || (s.MatchErr != nil) != tt.why {
				t.Errorf("Find() = by hand %+v, why %v, unmatched %v, %v\nwant %+v, unmatched %v", s.ByHand, s.ByHandErr, s.MatchErr, err, want, tt.why)
			}
		})
	}
}

// With no Homebrew, every app neither Apple's nor the App Store's is
// installed by hand, with no cask, and no part is Homebrew's.
func TestWithoutHomebrew(t *testing.T) {
	m, fake := newMac(t, "")
	write(t, m.AppDirs[0], map[string]string{"Tool.app/Contents/Info.plist": app("com.example.tool")})
	fake.On("brew").Fails(runner.ErrNotFound)
	s, _, err := snapshot.Find(t.Context(), m)
	if want := []brew.ByHand{{Path: filepath.Join(m.AppDirs[0], "Tool.app"), Name: "Tool", ID: "com.example.tool"}}; err != nil || s.MatchErr != nil || !slices.Equal(s.ByHand, want) {
		t.Errorf("by hand = %+v, %v, unmatched %v; want Tool", s.ByHand, err, s.MatchErr)
	}
	if got, want := counts(s), []string{"defaults 0", "features 0", "backups 0"}; !slices.Equal(got, want) {
		t.Errorf("parts = %q, want %q", got, want)
	}
}

// The apps installed by hand are for the record alone: when they can't be
// told, the snapshot says why, and everything else is found.
func TestAppsByHandThatCantBeToldSayWhy(t *testing.T) {
	m, fake := newMac(t, "")
	write(t, m.AppDirs[0], map[string]string{"Tool.app/Contents/Info.plist": app("com.example.tool")})
	homebrew(fake, "", "", "ghostty\n")
	fake.On("brew", "--caskroom").Exits(1).PrintsToStderr("Error: something broke\n")
	s, _, err := snapshot.Find(t.Context(), m)
	if err != nil || s.ByHandErr == nil || s.ByHand != nil {
		t.Errorf("Find() = by hand %q, why %v, %v; want why they can't be told", s.ByHand, s.ByHandErr, err)
	}
	if got, want := counts(s), []string{"brew 0", "cask 1", "defaults 0", "features 0", "backups 0"}; !slices.Equal(got, want) {
		t.Errorf("parts = %q, want %q", got, want)
	}
}
