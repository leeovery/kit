package steps_test

import (
	"context"
	"os"
	"path/filepath"
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

// remotes scripts the config repository's remotes as real git names them,
// main's upstream origin's main: the remotes (names), branch.main.pushRemote
// (pushRemote, "" for unset, remote.pushDefault then unset too), the push
// URLs of the remote it's pushed to (pushURLs), and origin's URL (origin,
// "" for no origin); and none of the remotes handed to a helper, nor an ssh
// command, variant, program for the other end or protocol version set, nor
// anything that undoes TLS's proof of an https URL's host.
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
	for _, key := range []string{"remote." + pushRemote + ".vcs", "remote.origin.vcs", "core.sshCommand", "ssh.variant", "protocol.version"} {
		f.On("git", gitIn("config", "--get", key)...).Exits(1)
	}
	for _, key := range []string{"remote." + pushRemote + ".receivepack", "remote.origin.uploadpack"} {
		f.On("git", gitIn("config", "-z", "--get-all", key)...).Exits(1)
	}
	for _, url := range append(strings.Fields(pushURLs), origin) {
		f.On("git", gitIn("config", "--type=bool", "--get-urlmatch", "http.sslVerify", url)...).Exits(1)
		for _, key := range []string{"http.sslCAInfo", "http.sslCAPath", "http.curloptResolve"} {
			f.On("git", gitIn("config", "--get-urlmatch", key, url)...).Exits(1)
		}
	}
}

// resolves scripts ssh -G resolving dest to host, as real ssh prints it,
// cut short, for git pushing to path there, and fetching from it.
func resolves(f *runnertest.Fake, dest, host, path string) {
	user, asked, _ := strings.Cut(dest, "@")
	said := "host " + asked + "\nuser " + user + "\nhostname " + host + "\nport 22\ncanonicalizefallbacklocal yes\ncanonicalizehostname false\ncontrolmaster false\nsessiontype default\n"
	f.On("ssh", "-G", dest, "git-receive-pack '"+path+"'").Prints(said)
	f.On("ssh", "-G", "-o", "SendEnv=GIT_PROTOCOL", dest, "git-upload-pack '"+path+"'").Prints(said)
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
// the branch is pushed to, asked as git pushes there, and origin's, asked
// as git fetches from it, so a URL ssh sends elsewhere pushing is named
// though it's private fetching; each place that isn't named, a repository
// on GitHub that isn't private with how to make it so. Proof is
// gitrepo.Place's: this Mac's, never a folder macOS syncs off it, or a
// repository GitHub says is private, never a place kit can't read taken
// for private. With no remote it's pushed to or fetched from, it isn't
// public; git, ssh or gh failing to answer, Push's URLs among them, or gh
// not signed in, fails.
func TestConfigPrivate(t *testing.T) {
	const usual, https = "git@github.com:someone/kit-config.git", "https://github.com/someone/my.config"
	home := t.TempDir()
	local, synced := filepath.Join(t.TempDir(), "kit-config.git"), filepath.Join(home, "Library", "CloudStorage", "Dropbox", "kit-config.git")
	// Each a repository git takes: a HEAD naming a branch, objects and refs.
	for _, d := range []string{local, synced} {
		for _, folder := range []string{"objects", "refs"} {
			if err := os.MkdirAll(filepath.Join(d, folder), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(d, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		script func(*runnertest.Fake)
		want   check.Result
	}{
		{
			name: "private, the usual form",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", usual+"\n", usual)
				resolves(f, "git@github.com", "github.com", "someone/kit-config.git")
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
				resolves(f, "git@github.com", "github.com", "/someone/kit-config.git")
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
				resolves(f, "git@git.example.com", "git.example.com", "someone/c.git")
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
				resolves(f, "git@git.example.com", "git.example.com", "someone/kit-config.git")
			},
			want: check.Result{State: check.Attention, Summary: "can't prove git.example.com:someone/kit-config.git private: git.example.com isn't GitHub", Items: []check.Item{unproven("git.example.com:someone/kit-config.git", "git.example.com isn't GitHub")}},
		},
		{
			name: "an ssh command set",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", usual+"\n", usual)
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
				remotes(f, "origin\n", "", local+"\n", local)
			},
			want: check.Result{State: check.OK, Summary: "on this Mac, so private"},
		},
		{
			name: "in a cloud drive",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", synced+"\n", local)
			},
			want: check.Result{State: check.Attention, Summary: "can't prove " + synced + " private: git writes it in ~/Library/CloudStorage/Dropbox, which syncs off this Mac", Items: []check.Item{unproven(synced, "git writes it in ~/Library/CloudStorage/Dropbox, which syncs off this Mac")}},
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
			name: "gh signed out",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", usual+"\n", usual)
				resolves(f, "git@github.com", "github.com", "someone/kit-config.git")
				f.On("gh", view("someone/kit-config")...).Exits(4).PrintsToStderr("To get started with GitHub CLI, please run:  gh auth login\nAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token.\n")
			},
			want: check.Result{State: check.Failed, Reason: "gh isn't signed in to GitHub: run gh auth login"},
		},
		{
			name: "pushed by a command ssh sends elsewhere",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", usual+"\n", usual)
				resolves(f, "git@github.com", "github.com", "someone/kit-config.git")
				f.On("ssh", "-G", "git@github.com", "git-receive-pack 'someone/kit-config.git'").Prints("host github.com\nuser git\nhostname evil.example\nport 22\ncanonicalizefallbacklocal yes\ncanonicalizehostname false\ncontrolmaster false\nsessiontype default\n")
				f.On("gh", view("someone/kit-config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.Attention, Summary: "can't prove github.com:someone/kit-config.git private: evil.example isn't GitHub", Items: []check.Item{unproven("github.com:someone/kit-config.git", "evil.example isn't GitHub")}},
		},
		{
			name: "fetched by a command ssh sends elsewhere",
			script: func(f *runnertest.Fake) {
				remotes(f, "origin\n", "", usual+"\n", usual)
				resolves(f, "git@github.com", "github.com", "someone/kit-config.git")
				f.On("ssh", "-G", "-o", "SendEnv=GIT_PROTOCOL", "git@github.com", "git-upload-pack 'someone/kit-config.git'").Prints("host github.com\nuser git\nhostname evil.example\nport 22\ncanonicalizefallbacklocal yes\ncanonicalizehostname false\ncontrolmaster false\nsessiontype default\n")
				f.On("gh", view("someone/kit-config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.Attention, Summary: "can't prove github.com:someone/kit-config.git private: evil.example isn't GitHub", Items: []check.Item{unproven("github.com:someone/kit-config.git", "evil.example isn't GitHub")}},
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
			s := steps.ConfigPrivate(gitrepo.Repo{Dir: dir, Run: fake, Home: home})
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

// gh is asked of each repository on GitHub once a check, whatever URLs and
// ways lead to it: pushed to and fetched from by the same URL, it's asked
// once; the next check asks again.
func TestConfigPrivateAsksGhOnceACheck(t *testing.T) {
	const usual = "git@github.com:someone/kit-config.git"
	fake := runnertest.New(t)
	remotes(fake, "origin\n", "", usual+"\n", usual)
	resolves(fake, "git@github.com", "github.com", "someone/kit-config.git")
	fake.On("gh", view("someone/kit-config")...).Prints("PRIVATE\n")
	s := steps.ConfigPrivate(gitrepo.Repo{Dir: dir, Run: fake, Home: t.TempDir()})
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
