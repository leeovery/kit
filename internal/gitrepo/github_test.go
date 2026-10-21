package gitrepo_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// askVisibility is gh asked a repository's visibility on GitHub.
func askVisibility(repo string) []string {
	return []string{"repo", "view", repo, "--json", "visibility", "--jq", ".visibility"}
}

// A remote's place is read as git reads its URL, each form git takes as it
// gives it back: a URL's host without who it signs in as or its port, in
// lower case, without a full name's last dot; ssh's host:path, with or
// without a user; an ssh host as ssh's config resolves it, as an alias, ssh
// -G reading the config alone. Any of GitHub's hosts is GitHub, and the
// repository's visibility there is gh's answer, never inferred. Another
// host isn't on GitHub, nor is this Mac's, a local path or a file:// URL,
// as private as the Mac. A place that can't be told is an error, never
// taken for one not on GitHub, nor a URL's sign-in shown in it, as it may
// carry a token.
func TestGitHubVisibility(t *testing.T) {
	const repo = "someone/kit"
	// A URL whose user and host the personal-data scan would take for an
	// email address is split at its @.
	for _, c := range []struct {
		name, remote string
		// ssh is the host ssh -G is asked about, and resolves the host it
		// says: none asked when "".
		ssh, resolves string
		// repo is the repository gh is asked about, "" for none, and says
		// its visibility; want is that, lower case.
		repo, says, want string
		// err is what the error says: none when "".
		err string
	}{
		{name: "https", remote: "https://github.com/someone/kit.git", repo: repo, says: "PRIVATE", want: "private"},
		{name: "https, a user", remote: "https://lee@" + "github.com/someone/kit", repo: repo, says: "PUBLIC", want: "public"},
		{name: "https, a token", remote: "https://x-access-token:T@" + "github.com/someone/kit.git", repo: repo, says: "PUBLIC", want: "public"},
		{name: "ssh://, a port", remote: "ssh://git@github.com:22/someone/kit.git", ssh: "github.com", resolves: "github.com", repo: repo, says: "PUBLIC", want: "public"},
		{name: "ssh on port 443", remote: "ssh://git@" + "ssh.github.com:443/someone/kit.git", ssh: "ssh.github.com", resolves: "ssh.github.com", repo: repo, says: "PUBLIC", want: "public"},
		{name: "capitals", remote: "https://GitHub.com/someone/kit", repo: repo, says: "PUBLIC", want: "public"},
		{name: "www", remote: "https://www.github.com/someone/kit", repo: repo, says: "INTERNAL", want: "internal"},
		{name: "a full name's last dot", remote: "https://github.com./someone/kit", repo: repo, says: "PUBLIC", want: "public"},
		{name: "git://", remote: "git://github.com/someone/kit.git", repo: repo, says: "PUBLIC", want: "public"},
		{name: "ssh's form", remote: "git@github.com:someone/kit.git", ssh: "github.com", resolves: "github.com", repo: repo, says: "PRIVATE", want: "private"},
		{name: "ssh's form, no user", remote: "github.com:someone/kit", ssh: "github.com", resolves: "github.com", repo: repo, says: "PUBLIC", want: "public"},
		{name: "ssh's form, capitals", remote: "git@" + "GitHub.com:someone/kit.git", ssh: "github.com", resolves: "github.com", repo: repo, says: "PUBLIC", want: "public"},
		{name: "an ssh alias", remote: "git@github-personal:someone/kit.git", ssh: "github-personal", resolves: "github.com", repo: repo, says: "PUBLIC", want: "public"},
		{name: "an ssh alias elsewhere", remote: "git@box:kit.git", ssh: "box", resolves: "192.168.1.20"},
		{name: "another host", remote: "https://gitlab.com/someone/kit.git"},
		{name: "another host over ssh", remote: "git@" + "gitlab.com:someone/kit.git", ssh: "gitlab.com", resolves: "gitlab.com"},
		{name: "a local path", remote: "/Users/someone/backups/kit.git"},
		{name: "a relative path", remote: "../kit.git"},
		{name: "a file:// URL", remote: "file:///Users/someone/backups/kit.git"},
		{name: "an escaped host", remote: "https://github%2Ecom/someone/kit", err: "can't tell where https://github%2Ecom/someone/kit is"},
		{name: "an escaped host, a token", remote: "https://x-access-token:T@github%2Ecom/someone/kit", err: "can't tell where https://github%2Ecom/someone/kit is"},
		{name: "a remote helper", remote: "gcrypt::git@github.com:someone/kit", err: "can't tell where gcrypt::git@github.com:someone/kit is"},
		{name: "a host in brackets", remote: "git@[github.com]:someone/kit", err: "can't tell where git@[github.com]:someone/kit is"},
		{name: "a host like a flag", remote: "ssh://-oProxyCommand=x/someone/kit", err: "can't tell where ssh://-oProxyCommand=x/someone/kit is"},
		{name: "no repository", remote: "https://x-access-token:T@" + "github.com/someone", err: "can't tell which GitHub repository https://github.com/someone is"},
		{name: "a gist", remote: "https://gist.github.com/0a1b2c3d.git", err: "can't tell which GitHub repository https://gist.github.com/0a1b2c3d.git is"},
		{name: "a name like a flag", remote: "https://github.com/someone/-kit", err: "can't tell which GitHub repository https://github.com/someone/-kit is"},
		{name: "ssh resolving nothing", remote: "git@github-personal:someone/kit.git", ssh: "github-personal", err: "can't tell which host ssh reaches for github-personal"},
	} {
		fake := runnertest.New(t)
		if c.ssh != "" {
			resolved := "user git\nport 22\n"
			if c.resolves != "" {
				resolved += "hostname " + c.resolves + "\n"
			}
			fake.On("ssh", "-G", c.ssh).Prints(resolved)
		}
		if c.repo != "" {
			fake.On("gh", askVisibility(c.repo)...).Prints(c.says + "\n")
		}
		repo, visibility, err := gitrepo.GitHubVisibility(context.Background(), fake, c.remote)
		switch {
		case c.err != "":
			if err == nil || err.Error() != c.err {
				t.Errorf("%s: GitHubVisibility(%q) = %q, %q, %v; want %q", c.name, c.remote, repo, visibility, err, c.err)
			}
		case err != nil || repo != c.repo || visibility != c.want:
			t.Errorf("%s: GitHubVisibility(%q) = %q, %q, %v; want %q, %q", c.name, c.remote, repo, visibility, err, c.repo, c.want)
		}
		var want []string
		if c.ssh != "" {
			want = append(want, "ssh -G "+c.ssh)
		}
		if c.repo != "" {
			want = append(want, "gh "+strings.Join(askVisibility(c.repo), " "))
		}
		if got := fake.Calls(); !slices.Equal(got, want) {
			t.Errorf("%s: ran %q, want %q", c.name, got, want)
		}
	}
}

// ssh failing to read its config, or gh failing to answer, is an error,
// never taken for a remote not on GitHub.
func TestGitHubVisibilityFails(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("ssh", "-G", "github.com").Exits(255).PrintsToStderr("/Users/someone/.ssh/config line 3: Bad configuration option: hostnme\n")
	if _, _, err := gitrepo.GitHubVisibility(context.Background(), fake, "git@github.com:someone/kit.git"); err == nil || !strings.HasPrefix(err.Error(), "can't tell which host ssh reaches for github.com: ") {
		t.Errorf("GitHubVisibility(), ssh's config unreadable = %v", err)
	}

	fake = runnertest.New(t)
	fake.On("gh", askVisibility("someone/kit")...).Exits(1).PrintsToStderr("HTTP 401: Bad credentials\n")
	if repo, _, err := gitrepo.GitHubVisibility(context.Background(), fake, "https://github.com/someone/kit"); repo != "someone/kit" || err == nil || !strings.HasPrefix(err.Error(), "couldn't ask GitHub: ") {
		t.Errorf("GitHubVisibility(), gh failing = %q, %v", repo, err)
	}
}
