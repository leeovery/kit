package steps_test

import (
	"context"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

const dir = "/Users/someone/.config/kit"

// gitIn is git's arguments in the config repository.
func gitIn(args ...string) []string { return append([]string{"-C", dir}, args...) }

// view is gh asked a repository's visibility.
func view(repo string) []string {
	return []string{"repo", "view", repo, "--json", "visibility", "--jq", ".visibility"}
}

// remotes scripts the config repository's remotes as real git names them,
// main's upstream origin's main: the remotes (names), branch.main.pushRemote
// (pushRemote, "" for unset, remote.pushDefault then unset too), the push
// URLs of the remote it's pushed to (pushURLs), and origin's URL (origin,
// "" for no origin).
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
	if origin == "" {
		f.On("git", gitIn("remote", "get-url", "origin")...).Exits(2).PrintsToStderr("error: No such remote 'origin'\n")
		return
	}
	f.On("git", gitIn("remote", "get-url", "origin")...).Prints(origin + "\n")
}

// resolves scripts ssh -G resolving host to itself, as for a host its
// config doesn't alias.
func resolves(f *runnertest.Fake, host string) {
	f.On("ssh", "-G", host).Prints("user git\nhostname " + host + "\nport 22\n")
}

// notPrivate is the item for repo, on GitHub and not private.
func notPrivate(repo string) check.Item {
	return check.Item{
		ID: "config-private:" + repo, Name: repo, State: "not-private",
		Detail: "make it private: gh repo edit " + repo + " --visibility private --accept-visibility-change-consequences",
	}
}

// The config repository is checked private on GitHub wherever it goes:
// every URL kit pushes it to, a remote's push URLs, else its URL, of the
// remote the branch is pushed to, and origin's, which it's fetched from;
// each repository there that isn't private named. Its place is as git
// reads its URL (gitrepo.GitHubVisibility). With no remote, or none it's
// pushed to, or elsewhere, it isn't public on GitHub; a place that can't
// be told fails.
func TestConfigPrivate(t *testing.T) {
	const ssh, https = "git@github.com:someone/kit-config.git", "https://github.com/someone/my.config"
	tests := []struct {
		name   string
		script func(*runnertest.Fake)
		want   check.Result
	}{
		{
			name: "private, over SSH",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", ssh+"\n", ssh)
				resolves(f, "github.com")
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
				resolves(f, "github.com")
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
				remotes(f, "origin\n", "", "https://github.com/someone/a\nhttps://github.com/someone/b\n", "https://github.com/someone/a")
				f.On("gh", view("someone/a")...).Prints("PUBLIC\n")
				f.On("gh", view("someone/b")...).Prints("INTERNAL\n")
			},
			want: check.Result{State: check.Attention, Summary: "public on GitHub (someone/a), internal on GitHub (someone/b): they must be private", Items: []check.Item{notPrivate("someone/a"), notPrivate("someone/b")}},
		},
		{
			name: "no remote",
			script: func(f *runnertest.Fake) {
				f.On("git", gitIn("remote")...)
				f.On("git", gitIn("remote", "get-url", "origin")...).Exits(2).PrintsToStderr("error: No such remote 'origin'\n")
			},
			want: check.Result{State: check.OK, Summary: "no remote, so not public"},
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
			want: check.Result{State: check.OK, Summary: "no remote it's pushed to, so not public"},
		},
		{
			name: "elsewhere",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", "git@git.example.com:someone/kit-config.git\n", "git@git.example.com:someone/kit-config.git")
				resolves(f, "git.example.com")
			},
			want: check.Result{State: check.OK, Summary: "not on GitHub, so not public there"},
		},
		{
			name: "on this Mac",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", "/Volumes/Backup/kit-config.git\n", "/Volumes/Backup/kit-config.git")
			},
			want: check.Result{State: check.OK, Summary: "not on GitHub, so not public there"},
		},
		{
			name: "a place that can't be told",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", "https://github%2Ecom/someone/kit-config\n", https)
			},
			want: check.Result{State: check.Failed, Reason: "can't tell where https://github%2Ecom/someone/kit-config is"},
		},
		{
			name: "GitHub can't be asked",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", ssh+"\n", ssh)
				resolves(f, "github.com")
				f.On("gh", view("someone/kit-config")...).Exits(4).PrintsToStderr("To get started with GitHub CLI, please run:  gh auth login")
			},
			want: check.Result{State: check.Failed, Reason: "couldn't ask GitHub: To get started with GitHub CLI, please run:  gh auth login"},
		},
		{
			name: "outside a repository",
			script: func(f *runnertest.Fake) {
				f.On("git", gitIn("remote")...).Exits(128).PrintsToStderr("fatal: not a git repository (or any of the parent directories): .git\n")
				f.On("git", gitIn("remote", "get-url", "origin")...).Exits(128).PrintsToStderr("fatal: not a git repository (or any of the parent directories): .git\n")
			},
			want: check.Result{State: check.Failed, Reason: "fatal: not a git repository (or any of the parent directories): .git"},
		},
		{
			name: "no git",
			script: func(f *runnertest.Fake) {
				f.On("git", gitIn("remote")...).Fails(runner.ErrNotFound)
				f.On("git", gitIn("remote", "get-url", "origin")...).Fails(runner.ErrNotFound)
			},
			want: check.Result{State: check.Failed, Reason: "not found"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			tt.script(fake)
			s := steps.ConfigPrivate(fake, dir)
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
