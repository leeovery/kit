package gitrepo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// visibilityOf is gh asked a repository's visibility by its URL, so of
// github.com whatever host GH_HOST names.
func visibilityOf(repo string) []string {
	return []string{"repo", "view", "https://github.com/" + repo, "--json", "visibility", "--jq", ".visibility"}
}

// A repository on github.com, at a URL in one of the three ways kit takes
// one, exactly, with or without .git, is the repository gh is asked about,
// by its URL; it's proven private only as gh says it's private.
func TestPlaceProvesPrivate(t *testing.T) {
	for _, c := range []struct {
		url, says string
		private   bool
	}{
		{"git@github.com:someone/kit-config.git", "PRIVATE\n", true},
		{"git@github.com:someone/kit-config", "PRIVATE\n", true},
		{"https://github.com/someone/kit-config.git", "PRIVATE\n", true},
		{"https://github.com/someone/kit-config", "PRIVATE\n", true},
		{"ssh://git@github.com/someone/kit-config.git", "PRIVATE\n", true},
		{"ssh://git@github.com/someone/kit-config", "PRIVATE\n", true},
		{"git@github.com:someone/kit-config.git", "PUBLIC\n", false},
		{"https://github.com/someone/kit-config", "INTERNAL\n", false},
	} {
		fake := runnertest.New(t)
		fake.On("gh", visibilityOf("someone/kit-config")...).Prints(c.says)
		got, err := gitrepo.Repo{Dir: dir, Run: fake}.Place(context.Background(), c.url)
		want := gitrepo.Place{Repo: "someone/kit-config", Visibility: strings.ToLower(strings.TrimSpace(c.says))}
		if err != nil || got != want || got.Private() != c.private {
			t.Errorf("Place(%q), gh saying %q = %+v, %v; want %+v, private %v", c.url, c.says, got, err, want, c.private)
		}
	}
}

// Anything else is unproven, saying why, and gh is never asked: another
// scheme, host, port, form or user; a sign-in in the URL; a path past the
// repository's, a query or a fragment; a name gh would take for a flag; a
// helper's URL; a path on this Mac.
func TestPlaceRefusesTheRest(t *testing.T) {
	for _, url := range []string{
		"http://github.com/someone/kit-config.git",
		"https://www.github.com/someone/kit-config.git",
		"https://gist.github.com/someone/kit-config.git",
		"https://GitHub.com/someone/kit-config.git",
		"https://github.com:443/someone/kit-config.git",
		"https://x-access-token:T@" + "github.com/someone/kit-config.git",
		"https://github.com/someone/kit-config/",
		"https://github.com/someone/kit-config/extra",
		"https://github.com/someone/kit-config?x=1",
		"https://github.com/someone/kit-config#main",
		"https://github.com/-someone/kit-config",
		"github.com:someone/kit-config.git",
		"someone@" + "github.com:someone/kit-config.git",
		"git@" + "ssh.github.com:someone/kit-config.git",
		"git@git.example.com:someone/kit-config.git",
		"ssh://github.com/someone/kit-config.git",
		"ssh://git@github.com:22/someone/kit-config.git",
		"ssh://git@github.com:someone/kit-config.git",
		"git+ssh://git@github.com/someone/kit-config.git",
		"gcrypt::git@github.com:someone/kit-config.git",
		"file:///Users/someone/kit-config.git",
		"/Users/someone/kit-config.git",
		"",
	} {
		fake := runnertest.New(t)
		got, err := gitrepo.Repo{Dir: dir, Run: fake}.Place(context.Background(), url)
		if err != nil || got.Private() || got.Repo != "" || !strings.HasPrefix(got.Unproven, "kit proves private only a repository on github.com, at https://github.com/<owner>/<name>, ") {
			t.Errorf("Place(%q) = %+v, %v; want it unproven, saying why", url, got, err)
		}
		if len(fake.Calls()) != 0 {
			t.Errorf("Place(%q) ran %q; want nothing asked", url, fake.Calls())
		}
	}
}

// gh failing to say is an error, never a place: signed out, it says how
// to sign in; otherwise what gh said.
func TestPlaceFails(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("gh", visibilityOf("someone/kit-config")...).Exits(4).PrintsToStderr("To get started with GitHub CLI, please run:  gh auth login\n")
	if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).Place(context.Background(), "git@github.com:someone/kit-config.git"); err == nil || err.Error() != "gh isn't signed in to GitHub: run gh auth login" {
		t.Errorf("Place(), gh signed out = %v", err)
	}

	fake = runnertest.New(t)
	fake.On("gh", visibilityOf("someone/kit-config")...).Exits(1).PrintsToStderr("GraphQL: Could not resolve to a Repository with the name 'someone/kit-config'. (repository)\n")
	if _, err := (gitrepo.Repo{Dir: dir, Run: fake}).Place(context.Background(), "git@github.com:someone/kit-config.git"); err == nil || !strings.HasPrefix(err.Error(), "couldn't ask GitHub: ") || !strings.Contains(err.Error(), "Could not resolve to a Repository") {
		t.Errorf("Place(), gh failing = %v", err)
	}
}

// A URL is named without who it signs in as, which may carry a token: a
// URL's sign-in, its brackets kept whole, or ssh's user.
func TestShown(t *testing.T) {
	for _, c := range []struct{ url, want string }{
		{"https://x-access-token:T@" + "github.com/someone/kit", "https://github.com/someone/kit"},
		{"ssh://[git@" + "github.com:22]/someone/kit", "ssh://[github.com:22]/someone/kit"},
		{"ssh://git@[2001:db8::1]:22/kit", "ssh://[2001:db8::1]:22/kit"},
		{"https://github.com/someone/kit", "https://github.com/someone/kit"},
		{"git@github.com:someone/kit.git", "github.com:someone/kit.git"},
		{"x-access-token:T@" + "github.com:someone/kit", "github.com:someone/kit"},
		{"/Volumes/Backup/@kit/prefs.git", "/Volumes/Backup/@kit/prefs.git"},
	} {
		if got := gitrepo.Shown(c.url); got != c.want {
			t.Errorf("Shown(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
