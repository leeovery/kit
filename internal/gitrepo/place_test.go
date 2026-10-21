package gitrepo_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// The git settings Place reads: the remote's helper; the command git runs
// for ssh, and its variant; the remote's programs for the other end; and
// the protocol's version.
const (
	originVCS   = "remote.origin.vcs"
	sshCommand  = "core.sshCommand"
	sshVariant  = "ssh.variant"
	receivePack = "remote.origin.receivepack"
	uploadPack  = "remote.origin.uploadpack"
	protocol    = "protocol.version"
)

// lees is the user and host of Lee's own remotes' form, git@github.com,
// split so a scan for personal data doesn't take it for an email address;
// connection a hash ssh makes of a connection (%C), in the form it prints.
const (
	lees       = "git@" + "github.com"
	connection = "9f2c4b1d7e8a6f3c5b0d2e4a6c8f1b3d5e7a9c0b"
)

// ran is a command Place runs, and its answer: what it prints, or the
// status it exits with.
type ran struct {
	name   string
	args   []string
	prints string
	exits  int
}

// script scripts r's answer in fake.
func (r ran) script(fake *runnertest.Fake) *runnertest.Script {
	return fake.On(r.name, r.args...).Prints(r.prints).Exits(r.exits)
}

// setting is git asked its setting key, as git config --get reads it:
// value, or not set for "".
func setting(key, value string) ran {
	r := ran{name: "git", args: git("config", "--get", key), prints: value + "\n"}
	if value == "" {
		r = ran{name: "git", args: r.args, exits: 1}
	}
	return r
}

// sshSettings are the settings Place reads, in order, for an ssh URL git
// goes to way, none set but set's: the ssh command, its variant, the
// remote's program for way, its values as git config -z --get-all prints
// them, and the protocol's version.
func sshSettings(way gitrepo.Way, set map[string]string) []ran {
	pack := receivePack
	if way == gitrepo.Fetching {
		pack = uploadPack
	}
	all := ran{name: "git", args: git("config", "-z", "--get-all", pack), exits: 1}
	if value, ok := set[pack]; ok {
		all = ran{name: "git", args: all.args, prints: value + "\x00"}
	}
	return []ran{setting(sshCommand, set[sshCommand]), setting(sshVariant, set[sshVariant]), all, setting(protocol, set[protocol])}
}

// sshG is ssh -G asked with args, answering answer.
func sshG(answer string, args ...string) ran {
	return ran{name: "ssh", args: append([]string{"-G"}, args...), prints: answer}
}

// said is ssh -G's answer for dest, as real ssh prints it with no config of
// the user's, cut short: but for hostname, port, the user, the user's own
// (someone) where dest names none, and besides, lines ssh prints after
// these, in order.
func said(dest, hostname, port string, besides ...string) string {
	user, host := "someone", dest
	if u, h, ok := strings.CutLast(dest, "@"); ok {
		user, host = u, h
	}
	return "host " + host + "\nuser " + user + "\nhostname " + hostname + "\nport " + port +
		"\naddressfamily any\nbatchmode no\ncanonicalizefallbacklocal yes\ncanonicalizehostname false\ncheckhostip no\ncompression no\ncontrolmaster false\nsessiontype default\n" +
		strings.Join(besides, "") + "identityfile ~/.ssh/id_ed25519\n"
}

// tlsSettings are git's settings for the https URL u, as Place reads them,
// in order, none set but set's.
func tlsSettings(u string, set map[string]string) []ran {
	var all []ran
	for _, key := range []string{"http.sslVerify", "http.sslCAInfo", "http.sslCAPath", "http.curloptResolve"} {
		args := git("config", "--get-urlmatch", key, u)
		if key == "http.sslVerify" {
			args = git("config", "--type=bool", "--get-urlmatch", key, u)
		}
		r := ran{name: "git", args: args, exits: 1}
		if value, ok := set[key]; ok {
			r = ran{name: "git", args: args, prints: value + "\n"}
		}
		all = append(all, r)
	}
	return all
}

// visibility is gh asked repo's visibility on GitHub, saying says.
func visibility(repo, says string) ran {
	return ran{name: "gh", args: askVisibility(repo), prints: says + "\n"}
}

// askVisibility is gh asked a repository's visibility on GitHub, by its
// URL, whatever host GH_HOST names.
func askVisibility(repo string) []string {
	return []string{"repo", "view", "https://github.com/" + repo, "--json", "visibility", "--jq", ".visibility"}
}

// runs is what Place runs, in order: each a command, or several.
func runs(each ...any) []ran {
	var all []ran
	for _, r := range each {
		switch r := r.(type) {
		case ran:
			all = append(all, r)
		case []ran:
			all = append(all, r...)
		}
	}
	return all
}

// placeCase is a remote's URL, origin's, the way git goes to it, what git,
// ssh and gh say of it, and the place Place makes of it.
type placeCase struct {
	name, url string
	way       gitrepo.Way
	// env is the environment git runs in, as the runner has it.
	env []string
	// vcs is the remote's helper: none when "".
	vcs string
	// runs are what Place runs once it has asked for the remote's
	// helper, in order, as git, ssh and gh answer.
	runs []ran
	want gitrepo.Place
}

// run scripts what c says in a fake, has Place read c's URL in it, and
// checks the place, and that Place ran what c says, in order.
func (c placeCase) run(t *testing.T) {
	t.Helper()
	fake := runnertest.New(t)
	fake.Env = c.env
	var want []string
	for _, r := range runs(setting(originVCS, c.vcs), c.runs) {
		r.script(fake)
		want = append(want, runner.Command{Name: r.name, Args: r.args}.String())
	}
	got, err := gitrepo.Repo{Dir: dir, Run: fake}.Place(context.Background(), "origin", c.url, c.way)
	if err != nil || got != c.want {
		t.Errorf("%s: Place(%q) = %+v, %v; want %+v", c.name, c.url, got, err, c.want)
	}
	if calls := fake.Calls(); !slices.Equal(calls, want) {
		t.Errorf("%s: ran\n%s\nwant\n%s", c.name, strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

// A remote on GitHub is proven private only as a repository gh says is
// private, on github.com or ssh.github.com: over https, its host in any
// case, with a full name's last dot or without, TLS's proof of it intact;
// or over ssh, to the host ssh's config resolves, asked with what git gives
// ssh going that way: its user and port as written, the option to send the
// protocol's version as git passes it, gitrepo.Fetching at any version but 0,
// gitrepo.Pushing at 1 alone, and the command git runs at the other end, the
// remote's program for that way, its first setting, as git takes it, else
// git's own, then the path, from its ~ when that's its second letter. A
// controlpath that names the host, or the connection's hash, which ssh is
// asked for, leaves it proven. Every answer is real git's or ssh's.
func TestPlaceProvesPrivate(t *testing.T) {
	const repo = "someone/kit"
	private := gitrepo.Place{Repo: repo, Visibility: "private"}
	githubSaid := said(lees, "github.com", "22")
	for _, c := range []placeCase{
		{name: "Lee's own form", url: lees + ":someone/kit-config.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, lees, "git-receive-pack 'someone/kit-config.git'"), visibility("someone/kit-config", "PRIVATE")), want: gitrepo.Place{Repo: "someone/kit-config", Visibility: "private"}},
		{name: "Lee's own form, fetched from", url: lees + ":someone/kit-config.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, nil), sshG(githubSaid, "-o", "SendEnv=GIT_PROTOCOL", lees, "git-upload-pack 'someone/kit-config.git'"), visibility("someone/kit-config", "PRIVATE")), want: gitrepo.Place{Repo: "someone/kit-config", Visibility: "private"}},
		{name: "https", url: "https://github.com/someone/kit.git", runs: runs(tlsSettings("https://github.com/someone/kit.git", nil), visibility(repo, "PRIVATE")), want: private},
		{name: "https, a user", url: "https://someone@" + "github.com/someone/kit", runs: runs(tlsSettings("https://someone@"+"github.com/someone/kit", nil), visibility(repo, "PRIVATE")), want: private},
		{name: "https, a token, kept from git's config", url: "https://x-access-token:T@" + "github.com/someone/kit.git", runs: runs(tlsSettings("https://x-access-token@"+"github.com/someone/kit.git", nil), visibility(repo, "PRIVATE")), want: private},
		{name: "https, capitals", url: "https://GitHub.com/someone/kit", runs: runs(tlsSettings("https://GitHub.com/someone/kit", nil), visibility(repo, "PRIVATE")), want: private},
		{name: "https, a full name's last dot", url: "https://github.com./someone/kit", runs: runs(tlsSettings("https://github.com./someone/kit", nil), visibility(repo, "PRIVATE")), want: private},
		{name: "https, checking on", url: "https://github.com/someone/kit", runs: runs(tlsSettings("https://github.com/someone/kit", map[string]string{"http.sslVerify": "true"}), visibility(repo, "PRIVATE")), want: private},
		{name: "https, addresses of its own cleared", url: "https://github.com/someone/kit", runs: runs(tlsSettings("https://github.com/someone/kit", map[string]string{"http.curloptResolve": ""}), visibility(repo, "PRIVATE")), want: private},
		{name: "ssh://, a port", url: "ssh://" + lees + ":22/someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, "-p", "22", lees, "git-receive-pack '/someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "ssh on port 443, fetched from", url: "ssh://git@" + "ssh.github.com:443/someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, nil), sshG(said("git@"+"ssh.github.com", "ssh.github.com", "443"), "-o", "SendEnv=GIT_PROTOCOL", "-p", "443", "git@"+"ssh.github.com", "git-upload-pack '/someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "git+ssh://", url: "git+ssh://" + lees + "/someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, lees, "git-receive-pack '/someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "ssh's form, no user", url: "github.com:someone/kit", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("github.com", "github.com", "22"), "github.com", "git-receive-pack 'someone/kit'"), visibility(repo, "PRIVATE")), want: private},
		{name: "ssh's form, capitals, as written", url: "git@" + "GitHub.com:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@"+"GitHub.com", "github.com", "22"), "git@"+"GitHub.com", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a full name's last dot, in capitals", url: "git@" + "SSH.GitHub.com.:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@"+"SSH.GitHub.com.", "ssh.github.com.", "22"), "git@"+"SSH.GitHub.com.", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "an ssh alias", url: "git@github-personal:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@github-personal", "github.com", "22"), "git@github-personal", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "an alias in capitals, as written", url: "Work:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("Work", "github.com", "22"), "Work", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "an alias matched with its user", url: "git@work:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@work", "github.com", "22"), "git@work", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "the remote's receivepack", url: lees + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{receivePack: "git receive-pack --x"}), sshG(githubSaid, lees, "git receive-pack --x 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "two receivepacks, the first git's", url: lees + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{receivePack: "first-receive-pack\x00second-receive-pack"}), sshG(githubSaid, lees, "first-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a receivepack set empty", url: lees + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{receivePack: ""}), sshG(githubSaid, lees, " 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "the remote's uploadpack, fetched from", url: lees + ":someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, map[string]string{uploadPack: "/opt/git-upload-pack"}), sshG(githubSaid, "-o", "SendEnv=GIT_PROTOCOL", lees, "/opt/git-upload-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "pushed at version 1", url: lees + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{protocol: "1"}), sshG(githubSaid, "-o", "SendEnv=GIT_PROTOCOL", lees, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "pushed at version 2", url: lees + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{protocol: "2"}), sshG(githubSaid, lees, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "fetched from at version 0", url: lees + ":someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, map[string]string{protocol: "0"}), sshG(githubSaid, lees, "git-upload-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "ssh's own variant", url: lees + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{sshVariant: "ssh"}), sshG(githubSaid, lees, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "GIT_SSH_VARIANT auto, over ssh.variant", url: lees + ":someone/kit.git", env: []string{"GIT_SSH_VARIANT=auto"}, runs: runs(slices.Delete(sshSettings(gitrepo.Pushing, nil), 1, 2), sshG(githubSaid, lees, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a controlpath naming the host", url: lees + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(lees, "github.com", "22", "controlpath /Users/someone/.ssh/cm-"+lees+":22\n"), lees, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a controlpath naming the connection", url: lees + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(lees, "github.com", "22", "controlpath /Users/someone/.ssh/cm-"+connection+"\n"), lees, "git-receive-pack 'someone/kit.git'"), sshG(said(lees, "github.com", "22", "controlpath "+connection+"\n"), "-o", "ControlPath=%C", lees, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "public on GitHub", url: "https://github.com/someone/kit", runs: runs(tlsSettings("https://github.com/someone/kit", nil), visibility(repo, "PUBLIC")), want: gitrepo.Place{Repo: repo, Visibility: "public"}},
		{name: "internal on GitHub", url: "https://github.com/someone/kit", runs: runs(tlsSettings("https://github.com/someone/kit", nil), visibility(repo, "INTERNAL")), want: gitrepo.Place{Repo: repo, Visibility: "internal"}},
	} {
		c.run(t)
		if c.want.Private() != (c.want.Visibility == "private") {
			t.Errorf("%s: %+v.Private() = %v", c.name, c.want, c.want.Private())
		}
	}
}

// Anything kit can't prove private is unproven, and why said: a URL git
// hands to a helper, by the remote's vcs setting, a scheme git doesn't
// reach itself, in lower case, or a ::; one git reads otherwise than
// net/url, with a query, a fragment or an ssh password; a way other than
// https or ssh; over ssh, an ssh command or a variant set, in the
// environment git runs in or git's config, a proxy in ssh's answer, a host
// name it canonicalizes, or a controlpath naming neither host nor
// connection, whatever its controlmaster says, as ssh uses a connection
// there either way; over https, a variable that undoes TLS's proof of the
// host, or a setting for the URL that does: checking off, certificate
// authorities of its own, addresses of its own; another host, by name, as
// ssh resolves it for the command git runs, an IP address, a host not in
// plain ASCII, or a host under github.com but GitHub's for repositories; a
// host kit can't read; a path on GitHub that isn't an owner's repository,
// asked of ssh as git quotes it.
func TestPlaceLeavesTheRestUnproven(t *testing.T) {
	helper := "git hands it to a helper, which alone knows where it goes"
	unread := "kit can't read where it goes"
	notARepo := "it isn't an owner's repository on GitHub"
	noTLS := ", so TLS doesn't prove the host git reaches"
	tls := "https://github.com/someone/kit"
	githubSaid := said(lees, "github.com", "22")
	sharedSaid := strings.Replace(said(lees, "github.com", "22", "controlpath /tmp/kit-shared-socket\n"), "controlmaster false", "controlmaster auto", 1)
	shared := "ssh's controlpath, /tmp/kit-shared-socket, names no host, so a connection to another host there can carry it: name the host in it, as %C does"
	probed := sshG(said(lees, "github.com", "22", "controlpath "+connection+"\n"), "-o", "ControlPath=%C", lees, "git-receive-pack 'someone/kit.git'")
	for _, c := range []placeCase{
		{name: "the remote's vcs", url: tls, vcs: "hg", want: gitrepo.Place{Unproven: "remote.origin.vcs hands it to a helper, which alone knows where it goes"}},
		{name: "s3://", url: "s3://bucket/kit", want: gitrepo.Place{Unproven: "git hands s3:// URLs to a helper, which alone knows where they go"}},
		{name: "codecommit://", url: "codecommit://kit", want: gitrepo.Place{Unproven: "git hands codecommit:// URLs to a helper, which alone knows where they go"}},
		{name: "a scheme in capitals", url: "HTTPS://github.com/someone/kit", want: gitrepo.Place{Unproven: "git hands HTTPS:// URLs to a helper, which alone knows where they go"}},
		{name: "a ::", url: "gcrypt::" + lees + ":someone/kit", want: gitrepo.Place{Unproven: helper}},
		{name: "a query", url: "https://github.com/someone/kit.git?x=1", want: gitrepo.Place{Unproven: unread}},
		{name: "a fragment", url: "ssh://" + lees + "/someone/kit.git#main", want: gitrepo.Place{Unproven: unread}},
		{name: "an ssh password", url: "ssh://git:pw@" + "github.com/someone/kit.git", want: gitrepo.Place{Unproven: unread}},
		{name: "http://", url: "http://github.com/someone/kit.git", want: gitrepo.Place{Unproven: "git reaches it over http://, and kit proves a repository on GitHub only over https or ssh"}},
		{name: "git://", url: "git://github.com/someone/kit.git", want: gitrepo.Place{Unproven: "git reaches it over git://, and kit proves a repository on GitHub only over https or ssh"}},
		{name: "ftp://", url: "ftp://github.com/someone/kit.git", want: gitrepo.Place{Unproven: "git reaches it over ftp://, and kit proves a repository on GitHub only over https or ssh"}},
		{name: "ftps://", url: "ftps://github.com/someone/kit.git", want: gitrepo.Place{Unproven: "git reaches it over ftps://, and kit proves a repository on GitHub only over https or ssh"}},
		{name: "core.sshCommand", url: lees + ":someone/kit.git", runs: sshSettings(gitrepo.Pushing, map[string]string{sshCommand: "ssh -F /tmp/other"})[:1], want: gitrepo.Place{Unproven: "core.sshCommand is set, so git may not reach it with ssh as its config says"}},
		{name: "GIT_SSH_COMMAND", url: lees + ":someone/kit.git", env: []string{"GIT_SSH_COMMAND=ssh -o HostName=192.0.2.1"}, want: gitrepo.Place{Unproven: "GIT_SSH_COMMAND is set, so git may not reach it with ssh as its config says"}},
		{name: "GIT_SSH_COMMAND, empty", url: lees + ":someone/kit.git", env: []string{"GIT_SSH_COMMAND="}, want: gitrepo.Place{Unproven: "GIT_SSH_COMMAND is set, so git may not reach it with ssh as its config says"}},
		{name: "GIT_SSH", url: "ssh://" + lees + "/someone/kit.git", env: []string{"GIT_SSH=/usr/local/bin/other-ssh"}, want: gitrepo.Place{Unproven: "GIT_SSH is set, so git may not reach it with ssh as its config says"}},
		{name: "GIT_SSH_VARIANT", url: lees + ":someone/kit.git", env: []string{"GIT_SSH_VARIANT=plink"}, runs: sshSettings(gitrepo.Pushing, nil)[:1], want: gitrepo.Place{Unproven: "GIT_SSH_VARIANT is plink, so git gives ssh other options"}},
		{name: "ssh.variant", url: lees + ":someone/kit.git", runs: sshSettings(gitrepo.Pushing, map[string]string{sshVariant: "simple"})[:2], want: gitrepo.Place{Unproven: "ssh.variant is simple, so git gives ssh other options"}},
		{name: "a Match on the command git runs", url: lees + ":someone/prefs.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(lees, "evil.example", "22"), lees, "git-receive-pack 'someone/prefs.git'")), want: gitrepo.Place{Unproven: "evil.example isn't GitHub"}},
		{name: "a ProxyCommand alias", url: "git@proxied:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@proxied", "github.com", "22", "proxycommand nc 192.0.2.1 22\n"), "git@proxied", "git-receive-pack 'someone/kit.git'")), want: gitrepo.Place{Unproven: "ssh's config takes it through a proxy (proxycommand), which can take it anywhere"}},
		{name: "a ProxyJump alias", url: "git@jumped:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@jumped", "github.com", "22", "proxyjump bastion.example.com\n"), "git@jumped", "git-receive-pack 'someone/kit.git'")), want: gitrepo.Place{Unproven: "ssh's config takes it through a proxy (proxyjump), which can take it anywhere"}},
		{name: "a host name canonicalized", url: "git@gh:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(strings.Replace(said("git@gh", "github.com", "22"), "canonicalizehostname false", "canonicalizehostname true", 1), "git@gh", "git-receive-pack 'someone/kit.git'")), want: gitrepo.Place{Unproven: "ssh's config canonicalizes its host name (canonicalizehostname), looking it up anew, which can take it elsewhere"}},
		{name: "a shared controlpath", url: lees + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(sharedSaid, lees, "git-receive-pack 'someone/kit.git'"), probed), want: gitrepo.Place{Unproven: shared}},
		{name: "a shared controlpath, no master here", url: lees + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(lees, "github.com", "22", "controlpath /tmp/kit-shared-socket\n"), lees, "git-receive-pack 'someone/kit.git'"), probed), want: gitrepo.Place{Unproven: shared}},
		{name: "an alias elsewhere", url: "git@box:kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@box", "192.0.2.7", "22"), "git@box", "git-receive-pack 'kit.git'")), want: gitrepo.Place{Unproven: "192.0.2.7 isn't GitHub"}},
		{name: "GIT_SSL_NO_VERIFY", url: tls, env: []string{"GIT_SSL_NO_VERIFY=1"}, want: gitrepo.Place{Unproven: "GIT_SSL_NO_VERIFY is set" + noTLS}},
		{name: "GIT_SSL_NO_VERIFY, empty", url: tls, env: []string{"GIT_SSL_NO_VERIFY="}, want: gitrepo.Place{Unproven: "GIT_SSL_NO_VERIFY is set" + noTLS}},
		{name: "GIT_SSL_CAINFO", url: tls, env: []string{"GIT_SSL_CAINFO=/tmp/ca.pem"}, want: gitrepo.Place{Unproven: "GIT_SSL_CAINFO is set" + noTLS}},
		{name: "GIT_SSL_CAPATH", url: tls, env: []string{"GIT_SSL_CAPATH=/tmp/certs"}, want: gitrepo.Place{Unproven: "GIT_SSL_CAPATH is set" + noTLS}},
		{name: "SSL_CERT_FILE", url: tls, env: []string{"SSL_CERT_FILE=/tmp/ca.pem"}, want: gitrepo.Place{Unproven: "SSL_CERT_FILE is set" + noTLS}},
		{name: "SSL_CERT_DIR", url: tls, env: []string{"SSL_CERT_DIR=/tmp/certs"}, want: gitrepo.Place{Unproven: "SSL_CERT_DIR is set" + noTLS}},
		{name: "http.sslVerify off", url: tls, runs: tlsSettings(tls, map[string]string{"http.sslVerify": "false"})[:1], want: gitrepo.Place{Unproven: "http.sslVerify is false for it" + noTLS}},
		{name: "http.sslCAInfo", url: tls, runs: tlsSettings(tls, map[string]string{"http.sslCAInfo": "/tmp/ca.pem"})[:2], want: gitrepo.Place{Unproven: "http.sslCAInfo is set for it" + noTLS}},
		{name: "http.sslCAPath, empty", url: tls, runs: tlsSettings(tls, map[string]string{"http.sslCAPath": ""})[:3], want: gitrepo.Place{Unproven: "http.sslCAPath is set for it" + noTLS}},
		{name: "http.curloptResolve", url: tls, runs: tlsSettings(tls, map[string]string{"http.curloptResolve": "api.github.com:443:192.0.2.2"}), want: gitrepo.Place{Unproven: "http.curloptResolve sends it to an address of its own"}},
		{name: "another host", url: "https://gitlab.com/someone/kit.git", want: gitrepo.Place{Unproven: "gitlab.com isn't GitHub"}},
		{name: "another host over ssh", url: "git@" + "gitlab.com:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@"+"gitlab.com", "gitlab.com", "22"), "git@"+"gitlab.com", "git-receive-pack 'someone/kit.git'")), want: gitrepo.Place{Unproven: "gitlab.com isn't GitHub"}},
		{name: "a host ending in github.com", url: "https://notgithub.com/someone/kit", want: gitrepo.Place{Unproven: "notgithub.com isn't GitHub"}},
		{name: "www", url: "https://www.github.com/someone/kit", want: gitrepo.Place{Unproven: "www.github.com holds none of GitHub's repositories, which are on github.com"}},
		{name: "a gist", url: "https://gist.github.com/0a1b2c3d.git", want: gitrepo.Place{Unproven: "gist.github.com holds none of GitHub's repositories, which are on github.com"}},
		{name: "an IP address", url: "https://192.0.2.10/someone/kit", want: gitrepo.Place{Unproven: "192.0.2.10 isn't GitHub"}},
		{name: "an IPv6 address", url: "https://[2001:db8::1]/someone/kit", want: gitrepo.Place{Unproven: `"2001:db8::1" isn't GitHub`}},
		{name: "a host not in plain ASCII", url: "https://g\u0456thub.com/someone/kit", want: gitrepo.Place{Unproven: `"g\u0456thub.com" isn't GitHub`}},
		{name: "an ssh host not in plain ASCII", url: "git@g\u0456thub.com:someone/kit", runs: sshSettings(gitrepo.Pushing, nil)[:2], want: gitrepo.Place{Unproven: `its host, "g\u0456thub.com", isn't plain ASCII`}},
		{name: "an escaped host", url: "https://github%2Ecom/someone/kit", want: gitrepo.Place{Unproven: unread}},
		{name: "a host in brackets", url: "git@[github.com]:someone/kit", want: gitrepo.Place{Unproven: unread}},
		{name: "a host like a flag", url: "ssh://-oProxyCommand=x/someone/kit", runs: sshSettings(gitrepo.Pushing, nil)[:2], want: gitrepo.Place{Unproven: unread}},
		{name: "a user like a flag", url: "ssh://-oProxyCommand=x@" + "github.com/someone/kit", runs: sshSettings(gitrepo.Pushing, nil)[:2], want: gitrepo.Place{Unproven: unread}},
		{name: "no repository", url: "https://github.com/someone", want: gitrepo.Place{Unproven: notARepo}},
		{name: "a name like a flag", url: "https://github.com/someone/-kit", want: gitrepo.Place{Unproven: notARepo}},
		{name: "a path git quotes", url: lees + ":someone/it's r!.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, lees, `git-receive-pack 'someone/it'\''s r'\!'.git'`)), want: gitrepo.Place{Unproven: notARepo}},
		{name: "a path escaped", url: "ssh://" + lees + "/someone/my%20kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, lees, "git-receive-pack '/someone/my kit.git'")), want: gitrepo.Place{Unproven: notARepo}},
		{name: "a home's path, on a port", url: "ssh://" + lees + ":2222/~someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(lees, "github.com", "2222"), "-p", "2222", lees, "git-receive-pack '~someone/kit.git'")), want: gitrepo.Place{Unproven: notARepo}},
		{name: "a ~ second, as git has it", url: lees + ":a~b/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, lees, "git-receive-pack '~b/kit.git'")), want: gitrepo.Place{Unproven: notARepo}},
	} {
		c.run(t)
		if c.want.Private() {
			t.Errorf("%s: %+v is private", c.name, c.want)
		}
	}
}

// A local path, or a file:// URL, is this Mac's only while it stays on the
// Mac, links resolved: on a filesystem that's local, and outside the
// folders in the home macOS syncs off it, cloud drives' and iCloud
// Drive's, told by their files, so by any name; else unproven, saying
// where it syncs or that it isn't local. A relative path is the
// repository's; a file:// URL's path is decoded, its host ignored, as git
// takes them.
func TestPlaceOnThisMac(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repoDir := filepath.Join(root, "repo")
	dropbox := filepath.Join(home, "Library", "CloudStorage", "Dropbox", "prefs.git")
	icloud := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "prefs.git")
	for _, d := range []string{dropbox, icloud, filepath.Join(root, "Backup", "kit.git"), repoDir, filepath.Join(root, "my kit.git")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(dropbox, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	onThisMac := gitrepo.Place{OnThisMac: true}
	syncs := func(in string) gitrepo.Place {
		return gitrepo.Place{Unproven: "it's in ~/" + in + ", which syncs off this Mac"}
	}
	smb := func(_ string, fs *unix.Statfs_t) error {
		fs.Fstypename = [16]byte{'s', 'm', 'b', 'f', 's'}
		return nil
	}
	place := func(url string, statfs func(string, *unix.Statfs_t) error) (gitrepo.Place, error) {
		fake := runnertest.New(t)
		fake.On("git", "-C", repoDir, "config", "--get", originVCS).Exits(1)
		repo := gitrepo.WithStatfs(gitrepo.Repo{Dir: repoDir, Run: fake, Home: home}, statfs)
		return repo.Place(context.Background(), "origin", url, gitrepo.Pushing)
	}
	for _, c := range []struct {
		name, url string
		statfs    func(string, *unix.Statfs_t) error
		want      gitrepo.Place
	}{
		{name: "a local path", url: filepath.Join(root, "Backup", "kit.git"), want: onThisMac},
		{name: "a relative path", url: "../Backup/kit.git", want: onThisMac},
		{name: "a file:// URL", url: "file://" + root + "/my%20kit.git", want: onThisMac},
		{name: "a file:// URL with a host", url: "file://localhost" + root + "/Backup/kit.git", want: onThisMac},
		{name: "in a cloud drive", url: dropbox, want: syncs("Library/CloudStorage/Dropbox")},
		{name: "in iCloud Drive", url: icloud, want: syncs("Library/Mobile Documents/com~apple~CloudDocs")},
		{name: "a link into a cloud drive", url: filepath.Join(root, "linked"), want: syncs("Library/CloudStorage/Dropbox")},
		{name: "a file:// URL into a cloud drive", url: "file://" + dropbox, want: syncs("Library/CloudStorage/Dropbox")},
		{name: "the cloud drives' folder", url: filepath.Join(home, "Library", "CloudStorage"), want: syncs("Library/CloudStorage")},
		{name: "on a filesystem that isn't local", url: filepath.Join(root, "Backup", "kit.git"), statfs: smb, want: gitrepo.Place{Unproven: "it's on smbfs, which isn't local"}},
		{name: "not there", url: filepath.Join(root, "gone.git"), want: gitrepo.Place{Unproven: "kit can't find it on this Mac"}},
	} {
		if got, err := place(c.url, c.statfs); err != nil || got != c.want {
			t.Errorf("%s: Place(%q) = %+v, %v; want %+v", c.name, c.url, got, err, c.want)
		}
	}
	// A case-insensitive filesystem, as macOS's is by default, takes a
	// synced folder by any case.
	if lower := strings.ToLower(dropbox); fileThere(lower) {
		if got, err := place(lower, nil); err != nil || got != syncs("Library/CloudStorage/dropbox") {
			t.Errorf("Place(%q) = %+v, %v; want it synced", lower, got, err)
		}
	}
}

// fileThere reports whether there's a file at path.
func fileThere(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Place reads the environment git runs in, as the runner gives it: a
// variable set in kit's own, which the runner doesn't give git, takes no
// part; one the runner gives git, as a run's context sets, does.
func TestPlaceReadsTheEnvironmentGitRunsIn(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "ssh -o HostName=192.0.2.1")
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	for _, c := range []struct {
		url  string
		runs []ran
	}{
		{lees + ":someone/kit.git", runs(sshSettings(gitrepo.Pushing, nil), sshG(said(lees, "github.com", "22"), lees, "git-receive-pack 'someone/kit.git'"), visibility("someone/kit", "PRIVATE"))},
		{"https://github.com/someone/kit", runs(tlsSettings("https://github.com/someone/kit", nil), visibility("someone/kit", "PRIVATE"))},
	} {
		fake := runnertest.New(t)
		for _, r := range runs(setting(originVCS, ""), c.runs) {
			r.script(fake)
		}
		repo := gitrepo.Repo{Dir: dir, Run: fake}
		if got, err := repo.Place(context.Background(), "origin", c.url, gitrepo.Pushing); err != nil || !got.Private() {
			t.Errorf("Place(%q), its variables in kit's environment alone = %+v, %v; want private", c.url, got, err)
		}
		ctx := runner.WithSecretEnv(context.Background(), "GIT_SSH_COMMAND=ssh -o HostName=192.0.2.1", "GIT_SSL_NO_VERIFY=1")
		if got, err := repo.Place(ctx, "origin", c.url, gitrepo.Pushing); err != nil || got.Unproven == "" {
			t.Errorf("Place(%q), its variables given git = %+v, %v; want unproven", c.url, got, err)
		}
	}
}

// git, ssh or gh failing to answer is an error, never a place: git reading
// its config, ssh its, ssh naming no host, or not saying a connection's
// hash; gh asked, or not signed in, which it says on two lines, the advice
// on the first, then exits 4; a runner that can't say what environment git
// runs in; a local path without the home to tell what syncs.
func TestPlaceFails(t *testing.T) {
	const ssh = lees + ":someone/kit.git"
	const tls = "https://github.com/someone/kit"
	pushed := "git-receive-pack 'someone/kit.git'"
	for _, c := range []struct {
		name, url string
		// runs are what Place runs before what fails, which exits exits,
		// saying stderr; repo the repository, as a test's but for runs.
		runs    []ran
		fails   ran
		exits   int
		stderr  string
		repo    func(*runnertest.Fake) gitrepo.Repo
		says    string
		exactly bool
	}{
		{name: "git's config unreadable", url: ssh, fails: setting(originVCS, ""), exits: 128, stderr: "fatal: bad config line 3 in file .git/config\n", says: "fatal: bad config line 3 in file .git/config"},
		{name: "ssh's config unreadable", url: ssh, runs: sshSettings(gitrepo.Pushing, nil), fails: sshG("", lees, pushed), exits: 255, stderr: "/Users/someone/.ssh/config line 3: Bad configuration option: hostnme\n", says: "can't tell which host ssh reaches for github.com: "},
		{name: "ssh naming no host", url: ssh, runs: sshSettings(gitrepo.Pushing, nil), fails: sshG("user git\nport 22\n", lees, pushed), says: "can't tell which host ssh reaches for github.com"},
		{name: "ssh not saying a connection's hash", url: ssh, runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(lees, "github.com", "22", "controlpath /tmp/kit-shared-socket\n"), lees, pushed)), fails: sshG("", "-o", "ControlPath=%C", lees, pushed), exits: 255, stderr: "/Users/someone/.ssh/config line 3: Bad configuration option: controlpth\n", says: "can't tell which host ssh reaches for github.com: "},
		{name: "an http.sslVerify git can't read", url: tls, fails: tlsSettings(tls, nil)[0], exits: 128, stderr: "fatal: bad boolean config value 'maybe' for 'http.sslverify'\n", says: "fatal: bad boolean config value 'maybe' for 'http.sslverify'"},
		{name: "gh failing", url: tls, runs: tlsSettings(tls, nil), fails: visibility("someone/kit", ""), exits: 1, stderr: "HTTP 401: Bad credentials\n", says: "couldn't ask GitHub: "},
		{name: "gh signed out", url: tls, runs: tlsSettings(tls, nil), fails: visibility("someone/kit", ""), exits: 4, stderr: "To get started with GitHub CLI, please run:  gh auth login\nAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token.\n", says: "gh isn't signed in to GitHub: run gh auth login", exactly: true},
		{name: "a runner that can't say", url: tls, repo: func(f *runnertest.Fake) gitrepo.Repo { return gitrepo.Repo{Dir: dir, Run: only{f}} }, says: "can't tell what environment git runs in", exactly: true},
		{name: "no home", url: "/Volumes/Backup/kit.git", repo: func(f *runnertest.Fake) gitrepo.Repo { return gitrepo.Repo{Dir: dir, Run: f} }, says: "kit doesn't know the home folder, to tell a path that syncs off this Mac", exactly: true},
	} {
		fake := runnertest.New(t)
		for _, r := range runs(setting(originVCS, ""), c.runs) {
			r.script(fake)
		}
		if c.fails.name != "" {
			c.fails.script(fake).Exits(c.exits).PrintsToStderr(c.stderr)
		}
		repo := gitrepo.Repo{Dir: dir, Run: fake, Home: t.TempDir()}
		if c.repo != nil {
			repo = c.repo(fake)
		}
		place, err := repo.Place(context.Background(), "origin", c.url, gitrepo.Pushing)
		said := err != nil && strings.Contains(err.Error(), c.says)
		if c.exactly {
			said = err != nil && err.Error() == c.says
		}
		if !said || place != (gitrepo.Place{}) {
			t.Errorf("%s: Place() = %+v, %v; want an error saying %q", c.name, place, err, c.says)
		}
	}
}

// only is a runner that runs commands, and can't say what environment it
// runs them in.
type only struct{ runner.Runner }

// A URL is named without who it signs in as, which may carry a token: a
// URL's sign-in, its brackets kept whole, or ssh's user.
func TestShown(t *testing.T) {
	for _, c := range []struct{ url, want string }{
		{"https://x-access-token:T@" + "github.com/someone/kit", "https://github.com/someone/kit"},
		{"ssh://[git@" + "github.com:22]/someone/kit", "ssh://[github.com:22]/someone/kit"},
		{"ssh://git@[2001:db8::1]:22/kit", "ssh://[2001:db8::1]:22/kit"},
		{"https://github.com/someone/kit", "https://github.com/someone/kit"},
		{lees + ":someone/kit.git", "github.com:someone/kit.git"},
		{"x-access-token:T@" + "github.com:someone/kit", "github.com:someone/kit"},
		{"/Volumes/Backup/@kit/prefs.git", "/Volumes/Backup/@kit/prefs.git"},
	} {
		if got := gitrepo.Shown(c.url); got != c.want {
			t.Errorf("Shown(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
