package cli_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/status"
)

// incoming scripts git's answers as a run brings in kit-config's remote
// commits, as real git gives them: behind of them, changing the files
// changes names, nothing committed here they haven't, so the branch moves
// on to them, which brings writes in: each file's content, by its path in
// kit-config.
func (w *world) incoming(t *testing.T, behind string, writes map[string]string) {
	t.Helper()
	dir := filepath.Join(w.home, ".config", "kit")
	git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
	var changes string
	for path := range writes {
		changes += path + "\x00"
	}
	w.fake.On("git", git("rev-list", "--count", "HEAD..refs/remotes/origin/main")...).Prints(behind + "\n").Then().Prints("0\n")
	w.fake.On("git", git("diff", "--name-only", "-z", "--no-renames", "HEAD...refs/remotes/origin/main")...).Prints(changes)
	w.fake.On("git", git("rev-list", "--count", "refs/remotes/origin/main..HEAD")...).Prints("0\n")
	w.fake.On("git", git("merge", "--ff-only", "--quiet", "refs/remotes/origin/main")...).Does(func() {
		for path, content := range writes {
			w.write(t, filepath.Join(".config", "kit", path), content)
		}
	})
}

// kit status brings in kit-config's remote commits before it reads the
// config, so it checks what another Mac declared since, and says how many
// came in, once, in config-sync's summary, --json counting them.
func TestStatusBringsInTheRemotesCommitsFirst(t *testing.T) {
	w := laptopWorld(t)
	w.incoming(t, "2", map[string]string{"laptop/declarations": "[homebrew formulae]\ngo\nripgrep\n"})
	out, errOut, code := w.run(t, "status")
	for _, want := range []string{"brew missing:new ripgrep (to install)\n", "config-sync ok pushed; 2 commits brought in\n"} {
		if !strings.Contains(out, want) || code != 0 {
			t.Errorf("kit status printed\n%s%s\nexit %d; want it to hold %q", out, errOut, code, want)
		}
	}
	calls := w.fake.Calls()
	merged := slices.IndexFunc(calls, func(c string) bool { return strings.Contains(c, " merge ") })
	listed := slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, "brew list") })
	if merged < 0 || listed < merged {
		t.Errorf("ran %q; want the remote's commits brought in before anything's checked", calls)
	}

	w = laptopWorld(t)
	w.incoming(t, "1", map[string]string{"kit.toml": twoMacs})
	out, _, _ = w.run(t, "status", "--json")
	var doc status.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("kit status --json printed %q: %v", out, err)
	}
	i := slices.IndexFunc(doc.Steps, func(s status.Step) bool { return s.ID == "config-sync" })
	if i < 0 || doc.Steps[i].Counts["brought_in"] != 1 || doc.Steps[i].Summary != "pushed; 1 commit brought in" {
		t.Errorf("config-sync = %+v; want 1 commit brought in, counted", doc.Steps)
	}
}

// What came in that this kit can't read fails the run as a config it
// can't read does.
func TestARunReadsWhatCameIn(t *testing.T) {
	w := laptopWorld(t)
	w.incoming(t, "1", map[string]string{"kit.toml": "minimum_kit = \"9.0.0\"\n" + twoMacs})
	_, errOut, code := w.run(t, "status")
	if code != 2 || errOut != "kit: the config needs kit 9.0.0 or later, and this is 0.1.0: update kit\n" {
		t.Errorf("kit status printed %q, exit %d; want it unable to read the config", errOut, code)
	}
}

// A pull that can't happen never fails the run: config-sync says why, its
// first day quiet, as drift is, then needing attention; once one brings
// the remote's commits in, it's forgotten.
func TestARunCarriesOnWithoutThemWhenItCant(t *testing.T) {
	w := laptopWorld(t)
	dir := filepath.Join(w.home, ".config", "kit")
	fetch := w.fake.On("git", "-C", dir, "fetch", "--prune", "--quiet", "origin").Exits(128).PrintsToStderr("ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.\n")
	out, errOut, code := w.run(t, "status")
	const said = "config-sync problem:new the remote's commits not brought in (couldn't reach the remote, so nothing was brought in)\n"
	if code != 0 || !strings.Contains(out, "config-sync ok pushed\n") || !strings.Contains(out, said) {
		t.Errorf("kit status, offline, printed\n%s%s\nexit %d; want it to carry on, saying %q", out, errOut, code, said)
	}
	if rec := w.read(t, filepath.Join(".local", "state", "kit", "drift.json")); !strings.Contains(rec, `"config-sync:pull": "2026-01-02T03:04:05Z"`) {
		t.Errorf("the drift record = %s; want the pull missed, first seen now", rec)
	}

	w.now = w.now.AddDate(0, 0, 2)
	w.ranHourly(t)
	out, _, code = w.run(t, "status")
	if code != 0 || !strings.Contains(out, "config-sync attention pushed\n") || !strings.Contains(out, "config-sync problem the remote's commits not brought in (") {
		t.Errorf("kit status, offline 2 days, printed\n%s\nexit %d; want it needing attention", out, code)
	}

	fetch.Then()
	out, _, _ = w.run(t, "status")
	if !strings.Contains(out, "config-sync ok pushed\n") || strings.Contains(out, "not brought in") {
		t.Errorf("kit status, back online, printed\n%s", out)
	}
	if rec := w.read(t, filepath.Join(".local", "state", "kit", "drift.json")); strings.Contains(rec, "config-sync") {
		t.Errorf("the drift record = %s; want the missed pull forgotten", rec)
	}
}

// kit apply, status, reconcile and nightly bring in kit-config's remote
// commits first; kit list stays quick and offline, and the commands that
// change the config pull only as they push. At a terminal, the run says
// what it's doing meanwhile, under its heading, unless steps are named, as
// such a run has none.
func TestWhichRunsBringInTheRemotesCommits(t *testing.T) {
	for _, tt := range []struct {
		args  []string
		pulls bool
	}{
		{[]string{"status"}, true},
		{[]string{"status", "brew"}, true},
		{[]string{"apply", "--plan"}, true},
		{[]string{"nightly", "--plan"}, true},
		{[]string{"reconcile"}, true},
		{[]string{"list"}, false},
		{[]string{"brew", "add", "jq"}, false},
	} {
		w := laptopWorld(t)
		w.run(t, tt.args...)
		fetched := slices.Contains(w.fake.Calls(), "git -C "+filepath.Join(w.home, ".config", "kit")+" fetch --prune --quiet origin")
		if fetched != tt.pulls {
			t.Errorf("kit %s brought in the remote's commits: %v, want %v", strings.Join(tt.args, " "), fetched, tt.pulls)
		}
	}

	for _, tt := range []struct {
		args []string
		says bool
	}{{[]string{"status"}, true}, {[]string{"status", "brew"}, false}} {
		w := laptopWorld(t)
		w.terminal = true
		out, _, _ := w.run(t, tt.args...)
		if got := strings.Contains(out, "bringing in kit-config's changes"); got != tt.says {
			t.Errorf("at a terminal, kit %s said it was bringing in kit-config's changes: %v, want %v", strings.Join(tt.args, " "), got, tt.says)
		}
	}
}
