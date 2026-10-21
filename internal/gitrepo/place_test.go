package gitrepo_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The git settings Place reads: the remote's helper, and the commands git
// runs in ssh's place, and as a proxy for its own protocol.
const (
	originVCS  = "remote.origin.vcs"
	sshCommand = "core.sshCommand"
	gitProxy   = "core.gitProxy"
)

// askVisibility is gh asked a repository's visibility on GitHub, by its
// URL, whatever host GH_HOST names.
func askVisibility(repo string) []string {
	return []string{"repo", "view", "https://github.com/" + repo, "--json", "visibility", "--jq", ".visibility"}
}

// resolved is ssh -G's answer for a host it resolves to host, as real ssh
// prints it, with lines besides: none when "".
func resolved(host string, besides ...string) string {
	answer := "user git\n"
	if host != "" {
		answer += "hostname " + host + "\n"
	}
	return answer + "port 22\n" + strings.Join(besides, "")
}

// placeCase is a remote's URL, origin's, what git, ssh and gh say of it,
// and the place Place makes of it.
type placeCase struct {
	name, url string
	// env and config are the environment and git's settings: none set
	// but these.
	env, config map[string]string
	// asks are the settings Place asks git for after the remote's helper,
	// in order.
	asks []string
	// ssh are ssh -G's arguments, none when nil, and answers what it
	// prints.
	ssh     []string
	answers string
	// repo is the repository gh is asked about, none when "", and says
	// its visibility.
	repo, says string
	want       gitrepo.Place
}

// run scripts git, ssh and gh as c says in a fake, has Place read c's URL
// in it, and checks the place, and that Place asked what c says, in order.
func (c placeCase) run(t *testing.T) {
	t.Helper()
	fake := runnertest.New(t)
	setting := func(key string) {
		if value, ok := c.config[key]; ok {
			fake.On("git", git("config", "--get", key)...).Prints(value + "\n")
			return
		}
		fake.On("git", git("config", "--get", key)...).Exits(1)
	}
	want := []string{runner.Command{Name: "git", Args: git("config", "--get", originVCS)}.String()}
	setting(originVCS)
	for _, key := range c.asks {
		setting(key)
		want = append(want, runner.Command{Name: "git", Args: git("config", "--get", key)}.String())
	}
	if c.ssh != nil {
		fake.On("ssh", c.ssh...).Prints(c.answers)
		want = append(want, runner.Command{Name: "ssh", Args: c.ssh}.String())
	}
	if c.repo != "" {
		fake.On("gh", askVisibility(c.repo)...).Prints(c.says + "\n")
		want = append(want, runner.Command{Name: "gh", Args: askVisibility(c.repo)}.String())
	}
	repo := gitrepo.Repo{Dir: dir, Run: fake, Getenv: func(name string) string { return c.env[name] }}
	got, err := repo.Place(context.Background(), "origin", c.url)
	if err != nil || got != c.want {
		t.Errorf("%s: Place(%q) = %+v, %v; want %+v", c.name, c.url, got, err, c.want)
	}
	if calls := fake.Calls(); !slices.Equal(calls, want) {
		t.Errorf("%s: ran\n%s\nwant\n%s", c.name, strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

// A remote is proven private only as this Mac's, a local path or a file://
// URL, or as a repository on GitHub that gh says is private, the host git
// reaches github.com or a host under it: a URL's host without who it signs
// in as or its port, in lower case, without a full name's last dot; an ssh
// host as ssh's config resolves it, given as git gives it, its user and
// port with it, as an alias's case and a Match on its user count, ssh -G
// reading the config alone. Every form git takes comes out one or the
// other, or unproven, never taken for private for being something kit
// misread. URLs a scan for personal data would take for email addresses
// are split at their @.
func TestPlaceProvesPrivate(t *testing.T) {
	const repo = "someone/kit"
	private := gitrepo.Place{Repo: repo, Visibility: "private"}
	for _, c := range []placeCase{
		{name: "Lee's own form", url: "git@" + "github.com:someone/kit-config.git", asks: []string{sshCommand}, ssh: []string{"-G", "git@" + "github.com"}, answers: resolved("github.com"), repo: "someone/kit-config", says: "PRIVATE", want: gitrepo.Place{Repo: "someone/kit-config", Visibility: "private"}},
		{name: "https", url: "https://github.com/someone/kit.git", repo: repo, says: "PRIVATE", want: private},
		{name: "https, a user", url: "https://someone@" + "github.com/someone/kit", repo: repo, says: "PRIVATE", want: private},
		{name: "https, a token", url: "https://x-access-token:T@" + "github.com/someone/kit.git", repo: repo, says: "PRIVATE", want: private},
		{name: "https, capitals", url: "https://GitHub.com/someone/kit", repo: repo, says: "PRIVATE", want: private},
		{name: "https, www", url: "https://www.github.com/someone/kit", repo: repo, says: "PRIVATE", want: private},
		{name: "https, a full name's last dot", url: "https://github.com./someone/kit", repo: repo, says: "PRIVATE", want: private},
		{name: "git://", url: "git://github.com/someone/kit.git", asks: []string{gitProxy}, repo: repo, says: "PRIVATE", want: private},
		{name: "ssh://, a port", url: "ssh://git@" + "github.com:22/someone/kit.git", asks: []string{sshCommand}, ssh: []string{"-G", "-p", "22", "git@" + "github.com"}, answers: resolved("github.com"), repo: repo, says: "PRIVATE", want: private},
		{name: "ssh on port 443", url: "ssh://git@" + "ssh.github.com:443/someone/kit.git", asks: []string{sshCommand}, ssh: []string{"-G", "-p", "443", "git@" + "ssh.github.com"}, answers: resolved("ssh.github.com"), repo: repo, says: "PRIVATE", want: private},
		{name: "ssh's form, no user", url: "github.com:someone/kit", asks: []string{sshCommand}, ssh: []string{"-G", "github.com"}, answers: resolved("github.com"), repo: repo, says: "PRIVATE", want: private},
		{name: "ssh's form, capitals, as written", url: "git@" + "GitHub.com:someone/kit.git", asks: []string{sshCommand}, ssh: []string{"-G", "git@" + "GitHub.com"}, answers: resolved("github.com"), repo: repo, says: "PRIVATE", want: private},
		{name: "an ssh alias", url: "git@github-personal:someone/kit.git", asks: []string{sshCommand}, ssh: []string{"-G", "git@github-personal"}, answers: resolved("github.com"), repo: repo, says: "PRIVATE", want: private},
		{name: "an alias in capitals, as written", url: "Work:someone/kit.git", asks: []string{sshCommand}, ssh: []string{"-G", "Work"}, answers: resolved("GitHub.com"), repo: repo, says: "PRIVATE", want: private},
		{name: "an alias matched with its user", url: "git@work:someone/kit.git", asks: []string{sshCommand}, ssh: []string{"-G", "git@work"}, answers: resolved("github.com"), repo: repo, says: "PRIVATE", want: private},
		{name: "a local path", url: "/Volumes/Backup/kit.git", want: gitrepo.Place{OnThisMac: true}},
		{name: "a relative path", url: "../kit.git", want: gitrepo.Place{OnThisMac: true}},
		{name: "a file:// URL", url: "file:///Volumes/Backup/kit.git", want: gitrepo.Place{OnThisMac: true}},
		{name: "public on GitHub", url: "https://github.com/someone/kit", repo: repo, says: "PUBLIC", want: gitrepo.Place{Repo: repo, Visibility: "public"}},
		{name: "internal on GitHub", url: "https://github.com/someone/kit", repo: repo, says: "INTERNAL", want: gitrepo.Place{Repo: repo, Visibility: "internal"}},
	} {
		c.run(t)
		if c.want.Private() != (c.want.OnThisMac || c.says == "PRIVATE") {
			t.Errorf("%s: %+v.Private() = %v", c.name, c.want, c.want.Private())
		}
	}
}

// Anything kit can't prove private is unproven, and why said: a URL git
// hands to a helper, by the remote's vcs setting, a scheme git doesn't
// reach itself, in lower case, or a ::; an ssh command set, in the
// environment or git's config, or a proxy in ssh's config, which can take
// it anywhere, as can a proxy for git's own protocol; another host, by
// name, an IP address or a host not in plain ASCII; a host kit can't read;
// a path on GitHub that isn't an owner's repository.
func TestPlaceLeavesTheRestUnproven(t *testing.T) {
	helper := "git hands it to a helper, which alone knows where it goes"
	unread := "kit can't read where it goes"
	notARepo := "it isn't an owner's repository on GitHub"
	for _, c := range []placeCase{
		{name: "the remote's vcs", url: "https://github.com/someone/kit", config: map[string]string{originVCS: "hg"}, want: gitrepo.Place{Unproven: "remote.origin.vcs hands it to a helper, which alone knows where it goes"}},
		{name: "s3://", url: "s3://bucket/kit", want: gitrepo.Place{Unproven: "git hands s3:// URLs to a helper, which alone knows where they go"}},
		{name: "codecommit://", url: "codecommit://kit", want: gitrepo.Place{Unproven: "git hands codecommit:// URLs to a helper, which alone knows where they go"}},
		{name: "a scheme in capitals", url: "HTTPS://github.com/someone/kit", want: gitrepo.Place{Unproven: "git hands HTTPS:// URLs to a helper, which alone knows where they go"}},
		{name: "a ::", url: "gcrypt::git@" + "github.com:someone/kit", want: gitrepo.Place{Unproven: helper}},
		{name: "core.sshCommand", url: "git@" + "github.com:someone/kit.git", config: map[string]string{sshCommand: "ssh -F /tmp/other"}, asks: []string{sshCommand}, want: gitrepo.Place{Unproven: "core.sshCommand is set, so git may not reach it with ssh as its config says"}},
		{name: "GIT_SSH_COMMAND", url: "git@" + "github.com:someone/kit.git", env: map[string]string{"GIT_SSH_COMMAND": "ssh -o HostName=192.0.2.1"}, want: gitrepo.Place{Unproven: "GIT_SSH_COMMAND is set, so git may not reach it with ssh as its config says"}},
		{name: "GIT_SSH", url: "ssh://git@" + "github.com/someone/kit.git", env: map[string]string{"GIT_SSH": "/usr/local/bin/other-ssh"}, want: gitrepo.Place{Unproven: "GIT_SSH is set, so git may not reach it with ssh as its config says"}},
		{name: "a ProxyCommand alias", url: "git@proxied:someone/kit.git", asks: []string{sshCommand}, ssh: []string{"-G", "git@proxied"}, answers: resolved("github.com", "proxycommand nc 192.0.2.1 22\n"), want: gitrepo.Place{Unproven: "ssh's config takes it through a proxy (proxycommand), which can take it anywhere"}},
		{name: "a ProxyJump alias", url: "git@jumped:someone/kit.git", asks: []string{sshCommand}, ssh: []string{"-G", "git@jumped"}, answers: resolved("github.com", "proxyjump bastion.example.com\n"), want: gitrepo.Place{Unproven: "ssh's config takes it through a proxy (proxyjump), which can take it anywhere"}},
		{name: "an alias elsewhere", url: "git@box:kit.git", asks: []string{sshCommand}, ssh: []string{"-G", "git@box"}, answers: resolved("192.0.2.7"), want: gitrepo.Place{Unproven: "192.0.2.7 isn't GitHub"}},
		{name: "git:// through core.gitProxy", url: "git://github.com/someone/kit.git", config: map[string]string{gitProxy: "proxy-to-anywhere"}, asks: []string{gitProxy}, want: gitrepo.Place{Unproven: "core.gitProxy is set, which can take it anywhere"}},
		{name: "git:// through GIT_PROXY_COMMAND", url: "git://github.com/someone/kit.git", env: map[string]string{"GIT_PROXY_COMMAND": "proxy-to-anywhere"}, want: gitrepo.Place{Unproven: "GIT_PROXY_COMMAND is set, which can take it anywhere"}},
		{name: "another host", url: "https://gitlab.com/someone/kit.git", want: gitrepo.Place{Unproven: "gitlab.com isn't GitHub"}},
		{name: "another host over ssh", url: "git@" + "gitlab.com:someone/kit.git", asks: []string{sshCommand}, ssh: []string{"-G", "git@" + "gitlab.com"}, answers: resolved("gitlab.com"), want: gitrepo.Place{Unproven: "gitlab.com isn't GitHub"}},
		{name: "a host ending in github.com", url: "https://notgithub.com/someone/kit", want: gitrepo.Place{Unproven: "notgithub.com isn't GitHub"}},
		{name: "an IP address", url: "https://192.0.2.10/someone/kit", want: gitrepo.Place{Unproven: "192.0.2.10 isn't GitHub"}},
		{name: "an IPv6 address", url: "https://[2001:db8::1]/someone/kit", want: gitrepo.Place{Unproven: `"2001:db8::1" isn't GitHub`}},
		{name: "a host not in plain ASCII", url: "https://g\u0456thub.com/someone/kit", want: gitrepo.Place{Unproven: `"g\u0456thub.com" isn't GitHub`}},
		{name: "an ssh host not in plain ASCII", url: "git@g\u0456thub.com:someone/kit", asks: []string{sshCommand}, want: gitrepo.Place{Unproven: `its host, "g\u0456thub.com", isn't plain ASCII`}},
		{name: "an escaped host", url: "https://github%2Ecom/someone/kit", want: gitrepo.Place{Unproven: unread}},
		{name: "a host in brackets", url: "git@[github.com]:someone/kit", want: gitrepo.Place{Unproven: unread}},
		{name: "a host like a flag", url: "ssh://-oProxyCommand=x/someone/kit", asks: []string{sshCommand}, want: gitrepo.Place{Unproven: unread}},
		{name: "a user like a flag", url: "ssh://-oProxyCommand=x@" + "github.com/someone/kit", asks: []string{sshCommand}, want: gitrepo.Place{Unproven: unread}},
		{name: "no repository", url: "https://github.com/someone", want: gitrepo.Place{Unproven: notARepo}},
		{name: "a gist", url: "https://gist.github.com/0a1b2c3d.git", want: gitrepo.Place{Unproven: notARepo}},
		{name: "a name like a flag", url: "https://github.com/someone/-kit", want: gitrepo.Place{Unproven: notARepo}},
	} {
		c.run(t)
		if c.want.Private() {
			t.Errorf("%s: %+v is private", c.name, c.want)
		}
	}
}

// git, ssh or gh failing to answer is an error, never a place: git reading
// its config, ssh its, ssh naming no host, gh asked.
func TestPlaceFails(t *testing.T) {
	const lees = "git@" + "github.com:someone/kit.git"
	for _, c := range []struct {
		name   string
		script func(*runnertest.Fake)
		url    string
		says   string
	}{
		{"git's config unreadable", func(f *runnertest.Fake) {
			f.On("git", git("config", "--get", originVCS)...).Exits(128).PrintsToStderr("fatal: bad config line 3 in file .git/config\n")
		}, lees, "fatal: bad config line 3 in file .git/config"},
		{"ssh's config unreadable", func(f *runnertest.Fake) {
			f.On("git", git("config", "--get", originVCS)...).Exits(1)
			f.On("git", git("config", "--get", sshCommand)...).Exits(1)
			f.On("ssh", "-G", "git@"+"github.com").Exits(255).PrintsToStderr("/Users/someone/.ssh/config line 3: Bad configuration option: hostnme\n")
		}, lees, "can't tell which host ssh reaches for github.com: "},
		{"ssh naming no host", func(f *runnertest.Fake) {
			f.On("git", git("config", "--get", originVCS)...).Exits(1)
			f.On("git", git("config", "--get", sshCommand)...).Exits(1)
			f.On("ssh", "-G", "git@"+"github.com").Prints(resolved(""))
		}, lees, "can't tell which host ssh reaches for github.com"},
		{"gh failing", func(f *runnertest.Fake) {
			f.On("git", git("config", "--get", originVCS)...).Exits(1)
			f.On("gh", askVisibility("someone/kit")...).Exits(1).PrintsToStderr("HTTP 401: Bad credentials\n")
		}, "https://github.com/someone/kit", "couldn't ask GitHub: "},
	} {
		fake := runnertest.New(t)
		c.script(fake)
		repo := gitrepo.Repo{Dir: dir, Run: fake, Getenv: func(string) string { return "" }}
		if place, err := repo.Place(context.Background(), "origin", c.url); err == nil || !strings.Contains(err.Error(), c.says) || place != (gitrepo.Place{}) {
			t.Errorf("%s: Place() = %+v, %v; want an error saying %q", c.name, place, err, c.says)
		}
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
		{"git@" + "github.com:someone/kit.git", "github.com:someone/kit.git"},
		{"x-access-token:T@" + "github.com:someone/kit", "github.com:someone/kit"},
		{"/Volumes/Backup/@kit/prefs.git", "/Volumes/Backup/@kit/prefs.git"},
	} {
		if got := gitrepo.Shown(c.url); got != c.want {
			t.Errorf("Shown(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
