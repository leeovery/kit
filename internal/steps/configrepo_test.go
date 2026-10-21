package steps_test

import (
	"context"
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

// remotes scripts the config repository's remotes as real git names them,
// main's upstream origin's main: the remotes (names), branch.main.pushRemote
// (pushRemote, "" for unset, remote.pushDefault then unset too), the push
// URLs of the remote it's pushed to (pushURLs), and origin's URL (origin,
// "" for no origin); and none of the remotes handed to a helper, nor an ssh
// command set.
func remotes(f *runnertest.Fake, names, pushRemote, pushURLs, origin string) {
	f.On("git", gitIn("remote")...).Prints(names)
	f.On("git", gitIn("symbolic-ref", "-q", "HEAD")...).Prints("refs/heads/main\n")
	f.On("git", gitIn("for-each-ref", "--format=%(upstream)%00%(upstream:remotename)", "refs/heads/main")...).Prints("refs/remotes/origin/main\x00origin\n")
	if pushRemote == "" {
		f.On("git", gitIn("config", "--get", "branch.main.pushRemote")...).Exits(1)
		f.On("git", gitIn("config", "--get", "remote.pushDefault")...).Exits(1)
		pushRemote = "origin"
	} else {
		f.On("git", gitIn("config", "--get", "branch.main.pushRemote")...).Prints(pushRemote + "\n")
	}
	f.On("git", gitIn("remote", "get-url", "--push", "--all", pushRemote)...).Prints(pushURLs)
	noOrigin := f.On("git", gitIn("remote", "get-url", "origin")...)
	if origin == "" {
		noOrigin.Exits(2).PrintsToStderr("error: No such remote 'origin'\n")
	} else {
		noOrigin.Prints(origin + "\n")
	}
	for _, key := range []string{"remote." + pushRemote + ".vcs", "remote.origin.vcs", "core.sshCommand"} {
		f.On("git", gitIn("config", "--get", key)...).Exits(1)
	}
}

// resolves scripts ssh -G resolving dest to host, as real ssh prints it.
func resolves(f *runnertest.Fake, dest, host string) {
	f.On("ssh", "-G", dest).Prints("user git\nhostname " + host + "\nport 22\n")
}

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
// URL kit pushes it to, a remote's push URLs, else its URL, of the remote
// the branch is pushed to, and origin's, which it's fetched from; each
// place that isn't named, a repository on GitHub that isn't private with
// how to make it so. Proof is gitrepo.Place's: this Mac's, or a repository
// GitHub says is private, never a place kit can't read taken for private.
// With no remote it's pushed to or fetched from, it isn't public; git, ssh
// or gh failing to answer, Push's URLs among them, fails.
func TestConfigPrivate(t *testing.T) {
	const lees, https = "git@github.com:someone/kit-config.git", "https://github.com/someone/my.config"
	tests := []struct {
		name   string
		script func(*runnertest.Fake)
		want   check.Result
	}{
		{
			name: "private, Lee's own form",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", lees+"\n", lees)
				resolves(f, "git@github.com", "github.com")
				f.On("gh", view("someone/kit-config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.OK, Summary: "private on GitHub (someone/kit-config)"},
		},
		{
			name: "private, over HTTPS",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", https+"\n", https)
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.OK, Summary: "private on GitHub (someone/my.config)"},
		},
		{
			name: "public",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", "ssh://git@github.com/someone/kit-config.git\n", "ssh://git@github.com/someone/kit-config.git")
				resolves(f, "git@github.com", "github.com")
				f.On("gh", view("someone/kit-config")...).Prints("PUBLIC\n")
			},
			want: check.Result{State: check.Attention, Summary: "public on GitHub (someone/kit-config): it must be private", Items: []check.Item{notPrivate("someone/kit-config")}},
		},
		{
			name: "a push URL public, its URL private",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", "https://github.com/someone/public-config\n", https)
				f.On("gh", view("someone/public-config")...).Prints("PUBLIC\n")
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.Attention, Summary: "public on GitHub (someone/public-config): it must be private", Items: []check.Item{notPrivate("someone/public-config")}},
		},
		{
			name: "fetched from a public URL, pushed to a private one",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", https+"\n", "https://github.com/someone/public-config")
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
				f.On("gh", view("someone/public-config")...).Prints("PUBLIC\n")
			},
			want: check.Result{State: check.Attention, Summary: "public on GitHub (someone/public-config): it must be private", Items: []check.Item{notPrivate("someone/public-config")}},
		},
		{
			name: "pushed to a fork, public",
			script: func(f *runnertest.Fake) {
				remotes(f, "fork\norigin\n", "fork", "https://github.com/someone/fork\n", https)
				f.On("gh", view("someone/fork")...).Prints("PUBLIC\n")
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.Attention, Summary: "public on GitHub (someone/fork): it must be private", Items: []check.Item{notPrivate("someone/fork")}},
		},
		{
			name: "several not private",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", "https://github.com/someone/a\nhttps://github.com/someone/b\ngit@git.example.com:someone/c.git\n", "https://github.com/someone/a")
				f.On("gh", view("someone/a")...).Prints("PUBLIC\n")
				f.On("gh", view("someone/b")...).Prints("INTERNAL\n")
				resolves(f, "git@git.example.com", "git.example.com")
			},
			want: check.Result{
				State:   check.Attention,
				Summary: "public on GitHub (someone/a), internal on GitHub (someone/b): they must be private; can't prove git.example.com:someone/c.git private: git.example.com isn't GitHub",
				Items:   []check.Item{notPrivate("someone/a"), notPrivate("someone/b"), unproven("git.example.com:someone/c.git", "git.example.com isn't GitHub")},
			},
		},
		{
			name: "elsewhere",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", "git@git.example.com:someone/kit-config.git\n", "git@git.example.com:someone/kit-config.git")
				resolves(f, "git@git.example.com", "git.example.com")
			},
			want: check.Result{State: check.Attention, Summary: "can't prove git.example.com:someone/kit-config.git private: git.example.com isn't GitHub", Items: []check.Item{unproven("git.example.com:someone/kit-config.git", "git.example.com isn't GitHub")}},
		},
		{
			name: "an ssh command set",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", lees+"\n", lees)
				f.On("git", gitIn("config", "--get", "core.sshCommand")...).Prints("ssh -F /tmp/other\n")
			},
			want: check.Result{State: check.Attention, Summary: "can't prove github.com:someone/kit-config.git private: core.sshCommand is set, so git may not reach it with ssh as its config says", Items: []check.Item{unproven("github.com:someone/kit-config.git", "core.sshCommand is set, so git may not reach it with ssh as its config says")}},
		},
		{
			name: "a place kit can't read",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", "https://github%2Ecom/someone/kit-config\n", https)
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.Attention, Summary: "can't prove https://github%2Ecom/someone/kit-config private: kit can't read where it goes", Items: []check.Item{unproven("https://github%2Ecom/someone/kit-config", "kit can't read where it goes")}},
		},
		{
			name: "on this Mac",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", "/Volumes/Backup/kit-config.git\n", "/Volumes/Backup/kit-config.git")
			},
			want: check.Result{State: check.OK, Summary: "on this Mac, so private"},
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
				f.On("git", gitIn("remote")...).Prints("backup\nupstream\n")
				f.On("git", gitIn("symbolic-ref", "-q", "HEAD")...).Prints("refs/heads/main\n")
				f.On("git", gitIn("for-each-ref", "--format=%(upstream)%00%(upstream:remotename)", "refs/heads/main")...).Prints("\x00\n")
				f.On("git", gitIn("config", "--get", "branch.main.pushRemote")...).Exits(1)
				f.On("git", gitIn("config", "--get", "remote.pushDefault")...).Exits(1)
				f.On("git", gitIn("remote", "get-url", "origin")...).Exits(2).PrintsToStderr("error: No such remote 'origin'\n")
			},
			want: check.Result{State: check.OK, Summary: "no remote it's pushed to or fetched from, so not public"},
		},
		{
			name: "HEAD on no branch, origin private",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", "", https)
				f.On("git", gitIn("symbolic-ref", "-q", "HEAD")...).Exits(1)
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.OK, Summary: "private on GitHub (someone/my.config)"},
		},
		{
			name: "Push's URLs unread, origin's read",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", "", https)
				f.On("git", gitIn("remote", "get-url", "--push", "--all", "origin")...).Exits(128).PrintsToStderr("fatal: unable to read config file '.git/config': Permission denied\n")
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.Failed, Reason: "fatal: unable to read config file '.git/config': Permission denied"},
		},
		{
			name: "GitHub can't be asked",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", lees+"\n", lees)
				resolves(f, "git@github.com", "github.com")
				f.On("gh", view("someone/kit-config")...).Exits(4).PrintsToStderr("To get started with GitHub CLI, please run:  gh auth login")
			},
			want: check.Result{State: check.Failed, Reason: "couldn't ask GitHub: To get started with GitHub CLI, please run:  gh auth login"},
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
			s := steps.ConfigPrivate(gitrepo.Repo{Dir: dir, Run: fake, Getenv: func(string) string { return "" }})
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
