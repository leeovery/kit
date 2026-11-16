package steps_test

import (
	"context"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

const dir = "/Users/someone/.config/kit"

// gitIn is git's arguments in the config repository.
func gitIn(args ...string) []string { return append([]string{"-C", dir}, args...) }

// view is gh asked a repository's visibility, by its URL.
func view(repo string) []string {
	return []string{"repo", "view", "https://github.com/" + repo, "--json", "visibility", "--jq", ".visibility"}
}

// remotes scripts the config repository's remotes as real git names them:
// the remotes (names); origin's push URLs (pushURLs), asked only with
// origin the one remote; and origin's URL (origin, "" for no origin).
func remotes(f *runnertest.Fake, names, pushURLs, origin string) {
	f.On("git", gitIn("remote")...).Prints(names)
	f.On("git", gitIn("remote", "get-url", "--push", "--all", "origin")...).Prints(pushURLs)
	noOrigin := f.On("git", gitIn("remote", "get-url", "origin")...)
	if origin == "" {
		noOrigin.Exits(2).PrintsToStderr("error: No such remote 'origin'\n")
	} else {
		noOrigin.Prints(origin + "\n")
	}
}

// notGitHub is why kit can't prove private a URL that isn't a repository on
// github.com, written exactly.
const notGitHub = "kit proves private only a repository on github.com, at https://github.com/<owner>/<name>, git@github.com:<owner>/<name> or ssh://git@github.com/<owner>/<name>"

// notPrivate is the item for repo, on GitHub and not private.
func notPrivate(repo string) check.Item {
	return check.Item{
		ID: "config-private:" + repo, Name: repo, State: "not-private",
		Detail: "make it private: gh repo edit " + repo + " --visibility private --accept-visibility-change-consequences",
	}
}

// unproven is the item for a URL, shown, kit can't prove private, and why.
func unproven(shown, why string) check.Item {
	return check.Item{ID: "config-private:" + shown, Name: shown, State: "unproven", Detail: why}
}

// The config repository is checked proven private wherever it goes: every
// URL kit pushes it to, its one remote's push URLs, else its URL, and
// origin's, each URL once; each that isn't proven private named, a
// repository on GitHub that isn't private with how to make it so. Proof is
// gitrepo.Place's: a repository on github.com, its URL written exactly,
// that GitHub says is private; anything else, a path on this Mac among
// them, is unproven. With several remotes Push pushes to none, so origin's
// alone is checked. With no remote it's pushed to or fetched from, it
// isn't public; git or gh failing to answer, Push's URLs among them, or gh
// not signed in, fails.
func TestConfigPrivate(t *testing.T) {
	const usual, https = "git@github.com:someone/kit-config.git", "https://github.com/someone/my.config"
	tests := []struct {
		name   string
		script func(*runnertest.Fake)
		want   check.Result
	}{
		{
			name: "private, the usual form",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", usual+"\n", usual)
				f.On("gh", view("someone/kit-config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.OK, Summary: "private on GitHub (someone/kit-config)"},
		},
		{
			name: "private, over HTTPS",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", https+"\n", https)
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.OK, Summary: "private on GitHub (someone/my.config)"},
		},
		{
			name: "public",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "ssh://git@github.com/someone/kit-config.git\n", "ssh://git@github.com/someone/kit-config.git")
				f.On("gh", view("someone/kit-config")...).Prints("PUBLIC\n")
			},
			want: check.Result{State: check.Attention, Summary: "public on GitHub (someone/kit-config): it must be private", Items: []check.Item{notPrivate("someone/kit-config")}},
		},
		{
			name: "a push URL public, its URL private",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "https://github.com/someone/public-config\n", https)
				f.On("gh", view("someone/public-config")...).Prints("PUBLIC\n")
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.Attention, Summary: "public on GitHub (someone/public-config): it must be private", Items: []check.Item{notPrivate("someone/public-config")}},
		},
		{
			name: "fetched from a public URL, pushed to a private one",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", https+"\n", "https://github.com/someone/public-config")
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
				f.On("gh", view("someone/public-config")...).Prints("PUBLIC\n")
			},
			want: check.Result{State: check.Attention, Summary: "public on GitHub (someone/public-config): it must be private", Items: []check.Item{notPrivate("someone/public-config")}},
		},
		{
			name: "several remotes, origin private",
			script: func(f *runnertest.Fake) {
				remotes(f, "fork\norigin\n", "", https)
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.OK, Summary: "private on GitHub (someone/my.config)"},
		},
		{
			name: "several not private",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "https://github.com/someone/a\nhttps://github.com/someone/b\ngit@git.example.com:someone/c.git\n", "https://github.com/someone/a")
				f.On("gh", view("someone/a")...).Prints("PUBLIC\n")
				f.On("gh", view("someone/b")...).Prints("INTERNAL\n")
			},
			want: check.Result{
				State:   check.Attention,
				Summary: "public on GitHub (someone/a), internal on GitHub (someone/b): they must be private; can't prove git.example.com:someone/c.git private: " + notGitHub,
				Items:   []check.Item{notPrivate("someone/a"), notPrivate("someone/b"), unproven("git.example.com:someone/c.git", notGitHub)},
			},
		},
		{
			name: "elsewhere",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "git@git.example.com:someone/kit-config.git\n", "git@git.example.com:someone/kit-config.git")
			},
			want: check.Result{State: check.Attention, Summary: "can't prove git.example.com:someone/kit-config.git private: " + notGitHub, Items: []check.Item{unproven("git.example.com:someone/kit-config.git", notGitHub)}},
		},
		{
			name: "on this Mac",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "/Users/someone/kit-config.git\n", "/Users/someone/kit-config.git")
			},
			want: check.Result{State: check.Attention, Summary: "can't prove /Users/someone/kit-config.git private: " + notGitHub, Items: []check.Item{unproven("/Users/someone/kit-config.git", notGitHub)}},
		},
		{
			name: "no remote",
			script: func(f *runnertest.Fake) {
				f.On("git", gitIn("remote")...)
				f.On("git", gitIn("remote", "get-url", "origin")...).Exits(2).PrintsToStderr("error: No such remote 'origin'\n")
			},
			want: check.Result{State: check.OK, Summary: "no remote it's pushed to or fetched from, so not public"},
		},
		{
			name: "no remote it's pushed to",
			script: func(f *runnertest.Fake) {
				remotes(f, "backup\nupstream\n", "", "")
			},
			want: check.Result{State: check.OK, Summary: "no remote it's pushed to or fetched from, so not public"},
		},
		{
			name: "Push's URLs unread",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", https)
				f.On("git", gitIn("remote", "get-url", "--push", "--all", "origin")...).Exits(128).PrintsToStderr("fatal: unable to read config file '.git/config': Permission denied\n")
			},
			want: check.Result{State: check.Failed, Reason: "fatal: unable to read config file '.git/config': Permission denied"},
		},
		{
			name: "gh signed out",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", usual+"\n", usual)
				f.On("gh", view("someone/kit-config")...).Exits(4).PrintsToStderr("To get started with GitHub CLI, please run:  gh auth login\nAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token.\n")
			},
			want: check.Result{State: check.Failed, Reason: "gh isn't signed in to GitHub: run gh auth login"},
		},
		{
			name: "outside a repository",
			script: func(f *runnertest.Fake) {
				f.On("git", gitIn("remote")...).Exits(128).PrintsToStderr("fatal: not a git repository (or any of the parent directories): .git\n")
			},
			want: check.Result{State: check.Failed, Reason: "fatal: not a git repository (or any of the parent directories): .git"},
		},
		{
			name:   "no git",
			script: func(f *runnertest.Fake) { f.On("git", gitIn("remote")...).Fails(runner.ErrNotFound) },
			want:   check.Result{State: check.Failed, Reason: "not found"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			tt.script(fake)
			s := steps.ConfigPrivate(gitrepo.Repo{Dir: dir, Run: fake})
			got := s.Check(context.Background())
			if got.State != tt.want.State || got.Summary != tt.want.Summary || got.Reason != tt.want.Reason || len(got.Items) != len(tt.want.Items) {
				t.Fatalf("check = %+v\nwant %+v", got, tt.want)
			}
			for i := range got.Items {
				if got.Items[i] != tt.want.Items[i] {
					t.Errorf("item = %+v, want %+v", got.Items[i], tt.want.Items[i])
				}
			}
		})
	}
}

// gh is asked of each URL once a check: pushed to and fetched from by the
// same URL, it's asked once; the next check asks again.
func TestConfigPrivateAsksGhOnceACheck(t *testing.T) {
	const usual = "git@github.com:someone/kit-config.git"
	fake := runnertest.New(t)
	remotes(fake, "origin\n", usual+"\n", usual)
	fake.On("gh", view("someone/kit-config")...).Prints("PRIVATE\n")
	s := steps.ConfigPrivate(gitrepo.Repo{Dir: dir, Run: fake})
	asked := func() int {
		n := 0
		for _, call := range fake.Calls() {
			if strings.HasPrefix(call, "gh repo view") {
				n++
			}
		}
		return n
	}
	for check := 1; check <= 2; check++ {
		if got := s.Check(context.Background()); got.Summary != "private on GitHub (someone/kit-config)" {
			t.Fatalf("check %d = %+v, want it private", check, got)
		}
		if n := asked(); n != check {
			t.Errorf("after check %d, gh was asked %d times, want once a check", check, n)
		}
	}
}
