package gitrepo_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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

// receiving is what Place asks git config of a repository on this Mac a
// push reaches, as git's --get-regexp matches a setting's name: what it
// does with its work tree, its hooks' folder, the command listing its
// alternates' refs, and each hook configured's events.
const receiving = `^(receive\.denycurrentbranch|core\.(hookspath|alternaterefscommand)|hook\..+\.event)$`

// receiveHooks are the hooks git runs for the repository a push reaches:
// receive-pack's, and pre-auto-gc, which the gc it starts runs.
var receiveHooks = []string{"pre-receive", "update", "proc-receive", "post-receive", "post-update", "push-to-checkout", "reference-transaction", "pre-auto-gc"}

// usual is the user and host of the usual form of a remote on GitHub over
// ssh, git@github.com, split so a scan for personal data doesn't take it
// for an email address; connection and sentinelHash the hashes ssh makes
// of a connection (%C, the SHA-1 of the local host, the host, the port and
// the user) as git, on port 22, to github.com and to the sentinel, from a
// Mac named laptop, in the form it prints.
const (
	usual        = "git@" + "github.com"
	connection   = "1b26ffd058727c73d72b13ac1d76bc4db9ba01d9"
	sentinelHash = "02f350e539475fde9cc44fdd8bc02373401cc964"
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
		"\naddressfamily any\nbatchmode no\ncanonicalizefallbacklocal yes\ncanonicalizehostname false\ncheckhostip no\ncompression no\ncontrolmaster false\nsessiontype default\nstricthostkeychecking ask\nverifyhostkeydns false\n" +
		strings.Join(besides, "") + "identityfile ~/.ssh/id_ed25519\nglobalknownhostsfile /etc/ssh/ssh_known_hosts /etc/ssh/ssh_known_hosts2\nuserknownhostsfile /Users/someone/.ssh/known_hosts /Users/someone/.ssh/known_hosts2\n"
}

// saidWith is said, its line for key, as real ssh prints it, holding value.
func saidWith(answer, key, value string) string {
	for line := range strings.Lines(answer) {
		if strings.HasPrefix(line, key+" ") {
			return strings.Replace(answer, line, key+" "+value+"\n", 1)
		}
	}
	panic(key + " isn't in ssh's answer")
}

// sentinel is the host no config means that Place asks ssh -G of.
const sentinel = "kit-sentinel.invalid"

// sentinelG is ssh -G asked of the sentinel as git's call to ssh, args
// before the port, signed in as user on port, answering a controlpath of
// path.
func sentinelG(path, user, port, command string, args ...string) ran {
	dest := user + "@" + sentinel
	return sshG(said(dest, sentinel, port, "controlpath "+path+"\n"), slices.Concat(args, []string{"-p", port, dest, command})...)
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
// protocol's version as git passes it, fetching at any version but 0, a
// program of the remote's own too, pushing at 1 alone, never for the
// simple variant, and the command git runs at the other end, the remote's
// program for that way, its first setting, as git takes it, else git's
// own, then the path, from its ~ when that's its second letter. A variant
// git doesn't know, in its case, is ssh's own. ssh's proof of the host
// stands: its key checked, strictly or asking, in files ssh keeps, any or
// none, GitHub's keys pinned in one of their own too, or from DNS only on
// asking. A controlpath not the same for a host no config means, asked as
// git's call with the user and port ssh resolved, leaves it proven. Every
// answer is real git's or ssh's.
func TestPlaceProvesPrivate(t *testing.T) {
	const repo = "someone/kit"
	private := gitrepo.Place{Repo: repo, Visibility: "private"}
	githubSaid := said(usual, "github.com", "22")
	for _, c := range []placeCase{
		{name: "the usual form", url: usual + ":someone/kit-config.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, usual, "git-receive-pack 'someone/kit-config.git'"), visibility("someone/kit-config", "PRIVATE")), want: gitrepo.Place{Repo: "someone/kit-config", Visibility: "private"}},
		{name: "the usual form, fetched from", url: usual + ":someone/kit-config.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, nil), sshG(githubSaid, "-o", "SendEnv=GIT_PROTOCOL", usual, "git-upload-pack 'someone/kit-config.git'"), visibility("someone/kit-config", "PRIVATE")), want: gitrepo.Place{Repo: "someone/kit-config", Visibility: "private"}},
		{name: "https", url: "https://github.com/someone/kit.git", runs: runs(tlsSettings("https://github.com/someone/kit.git", nil), visibility(repo, "PRIVATE")), want: private},
		{name: "https, a user", url: "https://someone@" + "github.com/someone/kit", runs: runs(tlsSettings("https://someone@"+"github.com/someone/kit", nil), visibility(repo, "PRIVATE")), want: private},
		{name: "https, a token, kept from git's config", url: "https://x-access-token:T@" + "github.com/someone/kit.git", runs: runs(tlsSettings("https://x-access-token@"+"github.com/someone/kit.git", nil), visibility(repo, "PRIVATE")), want: private},
		{name: "https, capitals", url: "https://GitHub.com/someone/kit", runs: runs(tlsSettings("https://GitHub.com/someone/kit", nil), visibility(repo, "PRIVATE")), want: private},
		{name: "https, a full name's last dot", url: "https://github.com./someone/kit", runs: runs(tlsSettings("https://github.com./someone/kit", nil), visibility(repo, "PRIVATE")), want: private},
		{name: "https, checking on", url: "https://github.com/someone/kit", runs: runs(tlsSettings("https://github.com/someone/kit", map[string]string{"http.sslVerify": "true"}), visibility(repo, "PRIVATE")), want: private},
		{name: "https, addresses of its own cleared", url: "https://github.com/someone/kit", runs: runs(tlsSettings("https://github.com/someone/kit", map[string]string{"http.curloptResolve": ""}), visibility(repo, "PRIVATE")), want: private},
		{name: "ssh://, a port", url: "ssh://" + usual + ":22/someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, "-p", "22", usual, "git-receive-pack '/someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "ssh on port 443, fetched from", url: "ssh://git@" + "ssh.github.com:443/someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, nil), sshG(said("git@"+"ssh.github.com", "ssh.github.com", "443"), "-o", "SendEnv=GIT_PROTOCOL", "-p", "443", "git@"+"ssh.github.com", "git-upload-pack '/someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "git+ssh://", url: "git+ssh://" + usual + "/someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, usual, "git-receive-pack '/someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "ssh's form, no user", url: "github.com:someone/kit", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("github.com", "github.com", "22"), "github.com", "git-receive-pack 'someone/kit'"), visibility(repo, "PRIVATE")), want: private},
		{name: "ssh's form, capitals, as written", url: "git@" + "GitHub.com:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@"+"GitHub.com", "github.com", "22"), "git@"+"GitHub.com", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a full name's last dot, in capitals", url: "git@" + "SSH.GitHub.com.:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@"+"SSH.GitHub.com.", "ssh.github.com.", "22"), "git@"+"SSH.GitHub.com.", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "an ssh alias", url: "git@github-personal:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@github-personal", "github.com", "22"), "git@github-personal", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "an alias in capitals, as written", url: "Work:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("Work", "github.com", "22"), "Work", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "an alias matched with its user", url: "git@work:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@work", "github.com", "22"), "git@work", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "the remote's receivepack", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{receivePack: "git receive-pack --x"}), sshG(githubSaid, usual, "git receive-pack --x 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "two receivepacks, the first git's", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{receivePack: "first-receive-pack\x00second-receive-pack"}), sshG(githubSaid, usual, "first-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a receivepack set empty", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{receivePack: ""}), sshG(githubSaid, usual, " 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "the remote's uploadpack, fetched from", url: usual + ":someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, map[string]string{uploadPack: "/opt/git-upload-pack"}), sshG(githubSaid, "-o", "SendEnv=GIT_PROTOCOL", usual, "/opt/git-upload-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "pushed at version 1", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{protocol: "1"}), sshG(githubSaid, "-o", "SendEnv=GIT_PROTOCOL", usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "pushed at version 2", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{protocol: "2"}), sshG(githubSaid, usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "fetched from at version 0", url: usual + ":someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, map[string]string{protocol: "0"}), sshG(githubSaid, usual, "git-upload-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "ssh's own variant", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, map[string]string{sshVariant: "ssh"}), sshG(githubSaid, usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "GIT_SSH_VARIANT auto, over ssh.variant", url: usual + ":someone/kit.git", env: []string{"GIT_SSH_VARIANT=auto"}, runs: runs(slices.Delete(sshSettings(gitrepo.Pushing, nil), 1, 2), sshG(githubSaid, usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "the remote's uploadpack, fetched from at version 1", url: usual + ":someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, map[string]string{uploadPack: "/opt/git-upload-pack", protocol: "1"}), sshG(githubSaid, "-o", "SendEnv=GIT_PROTOCOL", usual, "/opt/git-upload-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "the remote's uploadpack, fetched from at version 2", url: usual + ":someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, map[string]string{uploadPack: "/opt/git-upload-pack", protocol: "2"}), sshG(githubSaid, "-o", "SendEnv=GIT_PROTOCOL", usual, "/opt/git-upload-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a variant git doesn't know, in its case", url: usual + ":someone/kit.git", env: []string{"GIT_SSH_VARIANT=Plink"}, runs: runs(slices.Delete(sshSettings(gitrepo.Pushing, nil), 1, 2), sshG(githubSaid, usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a variant git doesn't know, set", url: "ssh://" + usual + ":22/someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, map[string]string{sshVariant: "bogus"}), sshG(githubSaid, "-o", "SendEnv=GIT_PROTOCOL", "-p", "22", usual, "git-upload-pack '/someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "GIT_SSH_VARIANT empty", url: usual + ":someone/kit.git", env: []string{"GIT_SSH_VARIANT="}, runs: runs(slices.Delete(sshSettings(gitrepo.Pushing, nil), 1, 2), sshG(githubSaid, usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "the simple variant, which git gives the host and command alone", url: usual + ":someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, map[string]string{sshVariant: "simple"}), sshG(githubSaid, usual, "git-upload-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "the host's key checked strictly", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "stricthostkeychecking", "true"), usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "GitHub's keys pinned in a file of their own", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "userknownhostsfile", "/Users/someone/.ssh/known_hosts /Users/someone/.ssh/github_known_hosts"), usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "no file of known hosts", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "userknownhostsfile", "/dev/null"), usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "known hosts none", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "userknownhostsfile", "none"), usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "/dev/null among the files of known hosts", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "userknownhostsfile", "/Users/someone/.ssh/known_hosts /dev/null"), usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "the host's key from DNS on asking", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "verifyhostkeydns", "ask"), usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a controlpath naming the host", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(usual, "github.com", "22", "controlpath /Users/someone/.ssh/cm-"+usual+":22\n"), usual, "git-receive-pack 'someone/kit.git'"), sentinelG("/Users/someone/.ssh/cm-git@"+sentinel+":22", "git", "22", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a controlpath naming the connection", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(usual, "github.com", "22", "controlpath /Users/someone/.ssh/cm-"+connection+"\n"), usual, "git-receive-pack 'someone/kit.git'"), sentinelG("/Users/someone/.ssh/cm-"+sentinelHash, "git", "22", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a controlpath naming the alias, fetched from", url: "git@gh:someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, nil), sshG(said("git@gh", "github.com", "22", "controlpath /Users/someone/.ssh/cm-gh\n"), "-o", "SendEnv=GIT_PROTOCOL", "git@gh", "git-upload-pack 'someone/kit.git'"), sentinelG("/Users/someone/.ssh/cm-kit-sentinel.invalid", "git", "22", "git-upload-pack 'someone/kit.git'", "-o", "SendEnv=GIT_PROTOCOL"), visibility(repo, "PRIVATE")), want: private},
		{name: "a controlpath for the host alone", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(usual, "github.com", "22", "controlpath /Users/someone/.ssh/only-gh\n"), usual, "git-receive-pack 'someone/kit.git'"), sentinelG("/tmp/sock-github.com", "git", "22", "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
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
// https or ssh; over ssh, an ssh command set, or a variant git gives other
// options, plink, putty or tortoiseplink, or simple with a port, which git
// refuses, in the environment git runs in or git's config, a proxy in ssh's
// answer, a host name it canonicalizes, the host's key not checked, checked
// by another name, against a command's keys or from DNS, or trusted on
// first sight, on any port, or a controlpath the same for a host no config
// means, whatever its controlmaster says, as ssh uses a connection there
// either way; over https, a variable that undoes TLS's proof of the host,
// or a setting for the URL that does: checking off, certificate authorities
// of its own, addresses of its own; another host, by name, as ssh resolves
// it for the command git runs, an IP address, a host not in plain ASCII, or
// a host under github.com but GitHub's for repositories; a host kit can't
// read; a path on GitHub that isn't an owner's repository, asked of ssh as
// git quotes it.
func TestPlaceLeavesTheRestUnproven(t *testing.T) {
	helper := "git hands it to a helper, which alone knows where it goes"
	unread := "kit can't read where it goes"
	notARepo := "it isn't an owner's repository on GitHub"
	noTLS := ", so TLS doesn't prove the host git reaches"
	tls := "https://github.com/someone/kit"
	githubSaid := said(usual, "github.com", "22")
	pushed := "git-receive-pack 'someone/kit.git'"
	shared := func(path string) gitrepo.Place {
		return gitrepo.Place{Unproven: "ssh's controlpath, " + path + ", is the same for other hosts, so a connection to another host there can carry git's: name the host in it, as %C does"}
	}
	literal := "/tmp/sock-github.com"
	byUser := "/Users/someone/.ssh/cm-git"
	firstSight := gitrepo.Place{Unproven: "ssh's config trusts a host's key on first sight (stricthostkeychecking accept-new), so another host can pass for it"}
	for _, c := range []placeCase{
		{name: "the remote's vcs", url: tls, vcs: "hg", want: gitrepo.Place{Unproven: "remote.origin.vcs hands it to a helper, which alone knows where it goes"}},
		{name: "s3://", url: "s3://bucket/kit", want: gitrepo.Place{Unproven: "git hands s3:// URLs to a helper, which alone knows where they go"}},
		{name: "codecommit://", url: "codecommit://kit", want: gitrepo.Place{Unproven: "git hands codecommit:// URLs to a helper, which alone knows where they go"}},
		{name: "a scheme in capitals", url: "HTTPS://github.com/someone/kit", want: gitrepo.Place{Unproven: "git hands HTTPS:// URLs to a helper, which alone knows where they go"}},
		{name: "a ::", url: "gcrypt::" + usual + ":someone/kit", want: gitrepo.Place{Unproven: helper}},
		{name: "a query", url: "https://github.com/someone/kit.git?x=1", want: gitrepo.Place{Unproven: unread}},
		{name: "a fragment", url: "ssh://" + usual + "/someone/kit.git#main", want: gitrepo.Place{Unproven: unread}},
		{name: "an ssh password", url: "ssh://git:pw@" + "github.com/someone/kit.git", want: gitrepo.Place{Unproven: unread}},
		{name: "http://", url: "http://github.com/someone/kit.git", want: gitrepo.Place{Unproven: "git reaches it over http://, and kit proves a repository on GitHub only over https or ssh"}},
		{name: "git://", url: "git://github.com/someone/kit.git", want: gitrepo.Place{Unproven: "git reaches it over git://, and kit proves a repository on GitHub only over https or ssh"}},
		{name: "ftp://", url: "ftp://github.com/someone/kit.git", want: gitrepo.Place{Unproven: "git reaches it over ftp://, and kit proves a repository on GitHub only over https or ssh"}},
		{name: "ftps://", url: "ftps://github.com/someone/kit.git", want: gitrepo.Place{Unproven: "git reaches it over ftps://, and kit proves a repository on GitHub only over https or ssh"}},
		{name: "core.sshCommand", url: usual + ":someone/kit.git", runs: sshSettings(gitrepo.Pushing, map[string]string{sshCommand: "ssh -F /tmp/other"})[:1], want: gitrepo.Place{Unproven: "core.sshCommand is set, so git may not reach it with ssh as its config says"}},
		{name: "GIT_SSH_COMMAND", url: usual + ":someone/kit.git", env: []string{"GIT_SSH_COMMAND=ssh -o HostName=192.0.2.1"}, want: gitrepo.Place{Unproven: "GIT_SSH_COMMAND is set, so git may not reach it with ssh as its config says"}},
		{name: "GIT_SSH_COMMAND, empty", url: usual + ":someone/kit.git", env: []string{"GIT_SSH_COMMAND="}, want: gitrepo.Place{Unproven: "GIT_SSH_COMMAND is set, so git may not reach it with ssh as its config says"}},
		{name: "GIT_SSH", url: "ssh://" + usual + "/someone/kit.git", env: []string{"GIT_SSH=/usr/local/bin/other-ssh"}, want: gitrepo.Place{Unproven: "GIT_SSH is set, so git may not reach it with ssh as its config says"}},
		{name: "GIT_SSH_VARIANT", url: usual + ":someone/kit.git", env: []string{"GIT_SSH_VARIANT=plink"}, runs: sshSettings(gitrepo.Pushing, nil)[:1], want: gitrepo.Place{Unproven: "GIT_SSH_VARIANT is plink, so git gives ssh other options"}},
		{name: "GIT_SSH_VARIANT over ssh.variant", url: usual + ":someone/kit.git", env: []string{"GIT_SSH_VARIANT=putty"}, runs: sshSettings(gitrepo.Pushing, nil)[:1], want: gitrepo.Place{Unproven: "GIT_SSH_VARIANT is putty, so git gives ssh other options"}},
		{name: "ssh.variant", url: usual + ":someone/kit.git", runs: sshSettings(gitrepo.Pushing, map[string]string{sshVariant: "tortoiseplink"})[:2], want: gitrepo.Place{Unproven: "ssh.variant is tortoiseplink, so git gives ssh other options"}},
		{name: "the simple variant with a port", url: "ssh://" + usual + ":22/someone/kit.git", runs: sshSettings(gitrepo.Pushing, map[string]string{sshVariant: "simple"})[:2], want: gitrepo.Place{Unproven: "ssh.variant is simple, with which git reaches no port"}},
		{name: "a Match on the command git runs", url: usual + ":someone/prefs.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(usual, "evil.example", "22"), usual, "git-receive-pack 'someone/prefs.git'")), want: gitrepo.Place{Unproven: "evil.example isn't GitHub"}},
		{name: "a ProxyCommand alias", url: "git@proxied:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@proxied", "github.com", "22", "proxycommand nc 192.0.2.1 22\n"), "git@proxied", "git-receive-pack 'someone/kit.git'")), want: gitrepo.Place{Unproven: "ssh's config takes it through a proxy (proxycommand), which can take it anywhere"}},
		{name: "a ProxyJump alias", url: "git@jumped:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said("git@jumped", "github.com", "22", "proxyjump bastion.example.com\n"), "git@jumped", "git-receive-pack 'someone/kit.git'")), want: gitrepo.Place{Unproven: "ssh's config takes it through a proxy (proxyjump), which can take it anywhere"}},
		{name: "a host name canonicalized", url: "git@gh:someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(strings.Replace(said("git@gh", "github.com", "22"), "canonicalizehostname false", "canonicalizehostname true", 1), "git@gh", "git-receive-pack 'someone/kit.git'")), want: gitrepo.Place{Unproven: "ssh's config canonicalizes its host name (canonicalizehostname), looking it up anew, which can take it elsewhere"}},
		{name: "the host's key not checked", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "stricthostkeychecking", "false"), usual, pushed)), want: gitrepo.Place{Unproven: "ssh's config doesn't check the host's key (stricthostkeychecking), so another host can pass for it"}},
		{name: "the host's key checked by another name", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(usual, "github.com", "22", "hostkeyalias other.example\n"), usual, pushed)), want: gitrepo.Place{Unproven: "ssh's config checks the host's key by another name (hostkeyalias), so another host can pass for it"}},
		{name: "the keys ssh knows from a command", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(usual, "github.com", "22", "knownhostscommand /usr/bin/false %H\n"), usual, pushed)), want: gitrepo.Place{Unproven: "ssh's config takes the keys it knows from a command (knownhostscommand), which can give any"}},
		{name: "a key trusted on first sight", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "stricthostkeychecking", "accept-new"), usual, pushed)), want: firstSight},
		{name: "a key trusted on first sight, GitHub's keys pinned", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(saidWith(githubSaid, "stricthostkeychecking", "accept-new"), "userknownhostsfile", "/Users/someone/.ssh/known_hosts /Users/someone/.ssh/github_known_hosts"), usual, pushed)), want: firstSight},
		{name: "a key trusted on first sight, on another port", url: "ssh://" + usual + ":2222/someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(said(usual, "github.com", "2222"), "stricthostkeychecking", "accept-new"), "-p", "2222", usual, "git-receive-pack '/someone/kit.git'")), want: firstSight},
		{name: "the host's key from DNS", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "verifyhostkeydns", "true"), usual, pushed)), want: gitrepo.Place{Unproven: "ssh's config takes the host's key from DNS (verifyhostkeydns), which another host can answer for"}},
		{name: "a literal controlpath for every host", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(usual, "github.com", "22", "controlpath "+literal+"\n"), usual, pushed), sentinelG(literal, "git", "22", pushed)), want: shared(literal)},
		{name: "a controlpath naming the user alone, a master here", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(said(usual, "github.com", "22", "controlpath "+byUser+"\n"), "controlmaster", "auto"), usual, pushed), sentinelG(byUser, "git", "22", pushed)), want: shared(byUser)},
		{name: "a controlpath the same for the user and port ssh resolved", url: "github:someone/kit.git", way: gitrepo.Fetching, runs: runs(sshSettings(gitrepo.Fetching, nil), sshG(said("git@github", "github.com", "443", "controlpath "+byUser+"\n"), "-o", "SendEnv=GIT_PROTOCOL", "github", "git-upload-pack 'someone/kit.git'"), sentinelG(byUser, "git", "443", "git-upload-pack 'someone/kit.git'", "-o", "SendEnv=GIT_PROTOCOL")), want: shared(byUser)},
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
		{name: "a path git quotes", url: usual + ":someone/it's r!.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, usual, `git-receive-pack 'someone/it'\''s r'\!'.git'`)), want: gitrepo.Place{Unproven: notARepo}},
		{name: "a path escaped", url: "ssh://" + usual + "/someone/my%20kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, usual, "git-receive-pack '/someone/my kit.git'")), want: gitrepo.Place{Unproven: notARepo}},
		{name: "a home's path, on a port", url: "ssh://" + usual + ":2222/~someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(usual, "github.com", "2222"), "-p", "2222", usual, "git-receive-pack '~someone/kit.git'")), want: gitrepo.Place{Unproven: notARepo}},
		{name: "a ~ second, as git has it", url: usual + ":a~b/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(githubSaid, usual, "git-receive-pack '~b/kit.git'")), want: gitrepo.Place{Unproven: notARepo}},
	} {
		c.run(t)
		if c.want.Private() {
			t.Errorf("%s: %+v is private", c.name, c.want)
		}
	}
}

// gitRepoAt makes a repository's git folder at dir, as git tells one: a HEAD
// naming a branch, and objects and refs folders.
func gitRepoAt(t *testing.T, dir string) {
	t.Helper()
	gitFolderAt(t, dir, "ref: refs/heads/main\n", "objects", "refs")
}

// gitFolderAt makes the folder dir, holding head as its HEAD, none for "",
// and the folders given.
func gitFolderAt(t *testing.T, dir, head string, folders ...string) {
	t.Helper()
	for _, f := range append([]string{"."}, folders...) {
		if err := os.MkdirAll(filepath.Join(dir, f), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if head != "" {
		writeAt(t, filepath.Join(dir, "HEAD"), head)
	}
}

// writeAt writes data to the file at path, making its folder.
func writeAt(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// executableAt writes a script at path, making its folder, that its owner
// may run.
func executableAt(t *testing.T, path string) {
	t.Helper()
	writeAt(t, path, "#!/bin/sh\n")
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

// devOf is the device info, a file's lstat, says it's on.
func devOf(info fs.FileInfo) uint64 {
	return uint64(info.Sys().(*syscall.Stat_t).Dev)
}

// link makes a link at path to to, making its folder.
func link(t *testing.T, to, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(to, path); err != nil {
		t.Fatal(err)
	}
}

// localCase is a local remote's URL, origin's, the way git goes to it, the
// environment git runs in, the remote's program for that way (nil for
// none), the repository's settings git config prints, as receiving matches
// them ("" for none), what the filesystem says of itself, which device each
// folder is on, and the place Place makes of it.
type localCase struct {
	name, url string
	way       gitrepo.Way
	env       []string
	pack      *string
	receives  string
	statfs    func(string, *unix.Statfs_t) error
	device    func(string, fs.FileInfo) uint64
	want      gitrepo.Place
}

// place has Place read c's URL, the repository's folder repoDir, the home
// home, with git's settings as c says.
func (c localCase) place(t *testing.T, repoDir, home string) (gitrepo.Place, error) {
	fake := runnertest.New(t)
	fake.Env = c.env
	fake.On("git", "-C", repoDir, "config", "--get", originVCS).Exits(1)
	key := receivePack
	if c.way == gitrepo.Fetching {
		key = uploadPack
	}
	pack := fake.On("git", "-C", repoDir, "config", "-z", "--get-all", key).Exits(1)
	if c.pack != nil {
		pack.Prints(*c.pack + "\x00").Exits(0)
	}
	settings := fake.On("git", "--git-dir=.", "config", "-z", "--get-regexp", receiving).Exits(1)
	if c.receives != "" {
		settings.Prints(c.receives).Exits(0)
	}
	repo := gitrepo.WithDevice(gitrepo.WithStatfs(gitrepo.Repo{Dir: repoDir, Run: fake, Home: home}, c.statfs), c.device)
	return repo.Place(context.Background(), "origin", c.url, c.way)
}

// A local path, or a file:// URL, is this Mac's only while git runs its own
// program at the other end, never one the remote sets, and the repository
// it finds there has its folders on the Mac: on a filesystem that's local,
// and outside the folders in the home whose files may leave it, cloud
// drives' and iCloud Drive's, which sync off it, and the Desktop and
// Documents, which iCloud may sync, told by their files, so by any name,
// case or spelling, the home's too, and told before anything in them is
// read; holds nothing that leads git elsewhere; and, pushed to, runs
// nothing of its own; else unproven, saying why. git finds the repository
// as real git does at the other end of a push: the path's trailing slashes
// dropped, a leading ~ the home in git's environment, then the path with
// /.git, nothing, .git/.git and .git added in turn, the first a file, or a
// folder git takes for a repository (a HEAD naming a branch, by a ref or a
// link into refs/, or an object, and objects and refs, a worktree's in its
// common folder; space before a HEAD's ref as git takes space, never \v or
// \f), a gitfile followed to the folder it names, with no other tried
// should that be none, and never itself checked; links on the way to it
// followed, and .. after a link where the link leads. A link in its git
// folder or its common folder, a worktree's, dangling or not, wherever it
// leads, in objects, refs, logs, worktrees, info or at its top, leaves it
// unproven, but in hooks, which git never writes; so does a folder in it
// kit can't read, a folder on another filesystem than its git folder's,
// each folder's own, as one mounted there is, never a file said to be, and
// a file hard-linked, outside objects, where git's local clones hard-link;
// an entry gone as kit looks through it is passed over. Pushed to, it runs
// its own when git config says receive.denyCurrentBranch is updateInstead,
// its last value, in any case, core.hooksPath or core.alternateRefsCommand
// is set, with a value or none, or a hook is configured for one of
// receiveHooks, named in git's case, or when its common folder's hooks, a
// link followed, hold one kit may run, a link to one too; never fetched
// from, nor for another hook, one kit may not run, a sample, a folder, or
// one in a worktree's own git folder, which git never runs. git's own
// program at the other end is git's too without its dash. A relative path
// is the repository's; a file:// URL's path is decoded, its host ignored,
// as git takes them.
func TestPlaceOnThisMac(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repoDir := filepath.Join(root, "repo")
	dropbox := filepath.Join(home, "Library", "CloudStorage", "Dropbox")
	icloud := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "prefs.git")
	for _, d := range []string{filepath.Join(dropbox, "prefs.git"), icloud, filepath.Join(root, "Backup", "kit.git"), filepath.Join(root, "my kit.git"), filepath.Join(home, "Desktop", "kit.git"), filepath.Join(home, "Documents", "Backups", "kit.git"), filepath.Join(home, "kit.git"), filepath.Join(root, "prefs.git"), filepath.Join(home, "Library", "CloudStorage")} {
		gitRepoAt(t, d)
	}
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link(t, filepath.Join(dropbox, "prefs.git"), filepath.Join(root, "linked"))
	cloud := filepath.Join(dropbox, "prefs.git")
	// in is the folder of test's layout, name, in root.
	in := func(name string) string { return filepath.Join(root, name) }

	// A plain folder, and beside it with .git, a link into the cloud.
	if err := os.MkdirAll(in("plain/kit"), 0o700); err != nil {
		t.Fatal(err)
	}
	link(t, cloud, in("plain/kit.git"))
	// A folder whose .git is a gitfile naming a repository in the cloud,
	// from its own folder.
	writeAt(t, in("gitfile/kit/.git"), "gitdir: ../../home/Library/CloudStorage/Dropbox/prefs.git\n")
	// A repository, and beside it with .git, a link into the cloud; a
	// repository whose .git is a gitfile naming one in the cloud; a folder
	// with .git holding a .git that's a link into the cloud; a repository
	// with .git holding a gitfile naming one in the cloud.
	gitRepoAt(t, in("bare/kit"))
	link(t, cloud, in("bare/kit.git"))
	gitRepoAt(t, in("both/kit"))
	writeAt(t, in("both/kit/.git"), "gitdir: "+cloud+"\n")
	link(t, cloud, in("nested/kit.git/.git"))
	gitRepoAt(t, in("nestedrepo/kit.git"))
	writeAt(t, in("nestedrepo/kit.git/.git"), "gitdir: "+cloud+"\n")
	// A repository whose .git is an empty folder; one whose .git has no
	// objects; one whose HEAD git can't read; one whose HEAD is a link
	// out of refs/; each with .git beside it a link into the cloud.
	gitRepoAt(t, in("empty/kit"))
	gitFolderAt(t, in("empty/kit/.git"), "")
	gitFolderAt(t, in("noobjects/kit/.git"), "ref: refs/heads/main\n", "refs")
	gitFolderAt(t, in("norefs/kit/.git"), "ref: refs/heads/main\n", "objects")
	gitFolderAt(t, in("garbage/kit/.git"), "garbage\n", "objects", "refs")
	gitFolderAt(t, in("linkedout/kit/.git"), "", "objects", "refs")
	link(t, "../refs/heads/main", in("linkedout/kit/.git/HEAD"))
	gitFolderAt(t, in("vtab/kit/.git"), "ref:\vrefs/heads/main\n", "objects", "refs")
	gitFolderAt(t, in("formfeed/kit/.git"), "ref:\frefs/heads/main\n", "objects", "refs")
	gitFolderAt(t, in("tabbed/kit/.git"), "ref:\t\r\n refs/heads/main\n", "objects", "refs")
	for _, d := range []string{"empty", "noobjects", "norefs", "garbage", "linkedout", "vtab", "formfeed", "tabbed"} {
		link(t, cloud, in(d+"/kit.git"))
	}
	// A .git on this Mac whose HEAD is an object's hash, and one whose HEAD
	// is a link into refs/, each with .git beside it a link into the cloud.
	gitFolderAt(t, in("hex/kit/.git"), "0123456789abcdef0123456789abcdef01234567\n", "objects", "refs")
	gitFolderAt(t, in("linkedhead/kit/.git"), "", "objects", "refs")
	link(t, "refs/heads/main", in("linkedhead/kit/.git/HEAD"))
	for _, d := range []string{"hex", "linkedhead"} {
		link(t, cloud, in(d+"/kit.git"))
	}
	// A worktree on this Mac whose repository is in the cloud.
	gitRepoAt(t, filepath.Join(dropbox, "main.git"))
	gitFolderAt(t, in("wtgit"), "ref: refs/heads/wt\n")
	writeAt(t, in("wtgit/commondir"), filepath.Join(dropbox, "main.git")+"\n")
	writeAt(t, in("wt/.git"), "gitdir: "+in("wtgit")+"\n")
	// Repositories each holding a link, in a place git writes or at their
	// top, leading into the cloud, nowhere, to /, through a file, round in a
	// loop, back to the repository, to the home in another case, or to
	// Documents from a folder named hooks below the top.
	gitFolderAt(t, filepath.Join(dropbox, "objects"), "")
	gitFolderAt(t, filepath.Join(dropbox, "pack"), "")
	writeAt(t, filepath.Join(dropbox, "afile"), "x")
	inRepo := []struct{ name, at, to string }{
		{name: "its objects", at: "objects", to: filepath.Join(dropbox, "objects")},
		{name: "its packs", at: "objects/pack", to: filepath.Join(dropbox, "pack")},
		{name: "its branches", at: "refs/heads", to: cloud},
		{name: "a reflog leading nowhere in the cloud", at: "logs/refs/heads/side", to: filepath.Join(dropbox, "logs", "side")},
		{name: "a reflog leading nowhere here", at: "logs/HEAD", to: "../missing/HEAD"},
		{name: "its worktrees", at: "worktrees/wt/logs", to: cloud},
		{name: "its packed-refs", at: "packed-refs", to: filepath.Join(dropbox, "packed-refs")},
		{name: "a link at its top to /", at: "lfs", to: "/"},
		{name: "its info", at: "info", to: cloud},
		{name: "a link through a file", at: "objects/x", to: filepath.Join(dropbox, "afile", "x")},
		{name: "a loop of links", at: "objects/loop", to: "loop"},
		{name: "a link to the repository itself", at: "logs/self", to: ".."},
		{name: "a link to the home in another case", at: "refs/home", to: filepath.Join(root, "HOME")},
		{name: "a link from a folder named hooks below its top", at: "objects/hooks", to: filepath.Join(home, "Documents")},
	}
	for i, l := range inRepo {
		repo := in(fmt.Sprintf("links/%d/kit.git", i))
		gitRepoAt(t, repo)
		if err := os.RemoveAll(filepath.Join(repo, l.at)); err != nil {
			t.Fatal(err)
		}
		link(t, l.to, filepath.Join(repo, l.at))
	}
	// A worktree whose repository holds a link into the cloud, and one
	// whose own git folder holds a link leading nowhere.
	gitRepoAt(t, in("links/common.git"))
	link(t, filepath.Join(dropbox, "pack"), in("links/common.git/objects/pack"))
	gitFolderAt(t, in("links/wtgit"), "ref: refs/heads/wt\n")
	writeAt(t, in("links/wtgit/commondir"), in("links/common.git")+"\n")
	writeAt(t, in("links/wt/.git"), "gitdir: "+in("links/wtgit")+"\n")
	gitFolderAt(t, in("links/wtgit2"), "ref: refs/heads/wt\n")
	writeAt(t, in("links/wtgit2/commondir"), in("Backup/kit.git")+"\n")
	link(t, in("links/missing"), in("links/wtgit2/logs/HEAD"))
	writeAt(t, in("links/wt2/.git"), "gitdir: "+in("links/wtgit2")+"\n")
	// A repository whose hooks are a link to Documents, and one whose hooks
	// hold a link into the cloud; links from outside to a repository on this
	// Mac, and to its folder; a repository with a folder kit can't read.
	gitRepoAt(t, in("hooked/kit.git"))
	gitFolderAt(t, filepath.Join(home, "Documents", "hooks"), "")
	link(t, filepath.Join(home, "Documents", "hooks"), in("hooked/kit.git/hooks"))
	gitRepoAt(t, in("hookfolder/kit.git"))
	link(t, cloud, in("hookfolder/kit.git/hooks/pre-push"))
	link(t, in("Backup/kit.git"), in("tolocal"))
	link(t, in("Backup"), in("toparent"))
	gitRepoAt(t, in("locked/kit.git"))
	if err := os.Mkdir(in("locked/kit.git/objects/locked"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(in("locked/kit.git/objects/locked"), 0o700) })
	// A worktree in Documents whose git folder and repository are on this
	// Mac.
	gitFolderAt(t, in("wtlocal"), "ref: refs/heads/wt\n")
	writeAt(t, in("wtlocal/commondir"), in("Backup/kit.git")+"\n")
	writeAt(t, filepath.Join(home, "Documents", "wt", ".git"), "gitdir: "+in("wtlocal")+"\n")
	// A gitfile naming a folder that's no repository, beside a repository.
	gitRepoAt(t, in("badfile/kit"))
	writeAt(t, in("badfile/kit/.git"), "gitdir: "+in("nowhere")+"\n")
	// A link into the cloud, then .. from it, with a repository on this
	// Mac where .. read by name would lead.
	gitFolderAt(t, filepath.Join(dropbox, "sub"), "")
	link(t, filepath.Join(dropbox, "sub"), in("up"))
	// Repositories whose hooks hold each hook git runs for a push, one it
	// doesn't, one kit may not run, a sample, a folder named as one, and a
	// link to one elsewhere; one whose hooks are a link to a folder holding
	// one; a worktree whose repository's hooks hold one, and one whose own
	// git folder's do.
	for _, name := range append([]string{"pre-push"}, receiveHooks...) {
		executableAt(t, in("hooks/"+name+"/kit.git/hooks/"+name))
		gitRepoAt(t, in("hooks/"+name+"/kit.git"))
	}
	writeAt(t, in("hooks/unrunnable/kit.git/hooks/post-receive"), "#!/bin/sh\n")
	executableAt(t, in("hooks/sample/kit.git/hooks/post-receive.sample"))
	gitFolderAt(t, in("hooks/folder/kit.git/hooks/post-receive"), "")
	executableAt(t, in("deploy"))
	link(t, in("deploy"), in("hooks/linked/kit.git/hooks/post-receive"))
	executableAt(t, in("shared hooks/post-receive"))
	link(t, in("shared hooks"), in("hooks/linkedfolder/kit.git/hooks"))
	for _, d := range []string{"unrunnable", "sample", "folder", "linked", "linkedfolder"} {
		gitRepoAt(t, in("hooks/"+d+"/kit.git"))
	}
	gitFolderAt(t, in("hooks/wtgit"), "ref: refs/heads/wt\n")
	writeAt(t, in("hooks/wtgit/commondir"), "../post-receive/kit.git\n")
	writeAt(t, in("hooks/wt/.git"), "gitdir: "+in("hooks/wtgit")+"\n")
	gitFolderAt(t, in("hooks/wtgit2"), "ref: refs/heads/wt\n")
	writeAt(t, in("hooks/wtgit2/commondir"), in("Backup/kit.git")+"\n")
	executableAt(t, in("hooks/wtgit2/hooks/post-receive"))
	writeAt(t, in("hooks/wt2/.git"), "gitdir: "+in("hooks/wtgit2")+"\n")
	// Repositories holding a file hard-linked to one outside: a reflog, the
	// config, and one at the top named as objects begins; and, in objects, a
	// pack and a loose object.
	writeAt(t, in("outside/file"), "x")
	hardLinked := []struct {
		name, at string
		refused  bool
	}{
		{name: "a reflog", at: "logs/HEAD", refused: true},
		{name: "its config", at: "config", refused: true},
		{name: "a file named as objects begins", at: "objects.bak", refused: true},
		{name: "a pack", at: "objects/pack/pack-1.pack"},
		{name: "a loose object", at: "objects/ab/cdef"},
	}
	for i, h := range hardLinked {
		repo := in(fmt.Sprintf("hardlinks/%d/kit.git", i))
		gitRepoAt(t, repo)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, h.at)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(in("outside/file"), filepath.Join(repo, h.at)); err != nil {
			t.Fatal(err)
		}
	}
	// Repositories whose folder, and whose file, go as kit looks through
	// them; one in Documents with a folder kit can't read.
	gitRepoAt(t, in("going/1/kit.git"))
	gitFolderAt(t, in("going/1/kit.git/objects/aa"), "")
	writeAt(t, in("going/1/kit.git/objects/bb/cdef"), "x")
	gitRepoAt(t, in("going/2/kit.git"))
	gitFolderAt(t, in("going/2/kit.git/logs/refs"), "")
	writeAt(t, in("going/2/kit.git/logs/x"), "x")
	lockedDocs := filepath.Join(home, "Documents", "locked", "kit.git")
	gitRepoAt(t, lockedDocs)
	if err := os.Mkdir(filepath.Join(lockedDocs, "objects", "locked"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(lockedDocs, "objects", "locked"), 0o700) })

	onThisMac := gitrepo.Place{OnThisMac: true}
	syncs := func(in string) gitrepo.Place {
		return gitrepo.Place{Unproven: "git writes it in ~/" + in + ", which syncs off this Mac"}
	}
	mayLeave := func(in string) gitrepo.Place {
		return gitrepo.Place{Unproven: "git writes it in ~/" + in + ", which may sync to iCloud"}
	}
	noRepo := gitrepo.Place{Unproven: "git finds no repository there"}
	inDropbox := syncs("Library/CloudStorage/Dropbox")
	smb := func(_ string, fs *unix.Statfs_t) error {
		fs.Fstypename = [16]byte{'s', 'm', 'b', 'f', 's'}
		return nil
	}
	// smbAt says the filesystem at path, links resolved, alone is smbfs.
	smbAt := func(path string) func(string, *unix.Statfs_t) error {
		path, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		return func(p string, fs *unix.Statfs_t) error {
			if p == path {
				return smb(p, fs)
			}
			return unix.Statfs(p, fs)
		}
	}
	// resolved is path, its links resolved, as kit names it.
	resolved := func(path string) string {
		path, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	// aLink is the link at path, in a repository, said, its folder
	// resolved.
	aLink := func(path string) gitrepo.Place {
		return gitrepo.Place{Unproven: resolved(filepath.Dir(path)) + "/" + filepath.Base(path) + " is a link: kit proves a repository on this Mac only when nothing in it leads elsewhere"}
	}
	// elsewhereAt says the folder at path, resolved, and everything in it,
	// are on another device, as a filesystem mounted there is.
	elsewhereAt := func(path string) func(string, fs.FileInfo) uint64 {
		path = resolved(path)
		return func(p string, info fs.FileInfo) uint64 {
			if p == path || strings.HasPrefix(p, path+"/") {
				return devOf(info) + 1
			}
			return devOf(info)
		}
	}
	// removing removes gone as kit reads which device the folder at,
	// resolved, is on.
	removing := func(at, gone string) func(string, fs.FileInfo) uint64 {
		at = resolved(at)
		return func(p string, info fs.FileInfo) uint64 {
			if p == at {
				if err := os.RemoveAll(gone); err != nil {
					t.Error(err)
				}
			}
			return devOf(info)
		}
	}
	runsItsOwn := func(hook string) gitrepo.Place {
		return gitrepo.Place{Unproven: "the repository runs its own " + hook + " hook, which can take the push anywhere"}
	}
	updateInstead := gitrepo.Place{Unproven: "receive.denyCurrentBranch is updateInstead, so the push writes the repository's work tree, which can be anywhere"}
	hooksPath := gitrepo.Place{Unproven: "core.hooksPath is set, so the repository runs hooks from another folder, which can take the push anywhere"}
	gitsOwn, another := "git-receive-pack", "/opt/kit/receive"
	usualHome := []string{"HOME=" + home}
	for _, c := range []localCase{
		{name: "a local path", url: in("Backup/kit.git"), want: onThisMac},
		{name: "a local path, fetched from", url: in("Backup/kit.git"), way: gitrepo.Fetching, want: onThisMac},
		{name: "a relative path", url: "../Backup/kit.git", want: onThisMac},
		{name: "trailing slashes", url: in("plain/kit") + "//", want: inDropbox},
		{name: "a file:// URL", url: "file://" + root + "/my%20kit.git", want: onThisMac},
		{name: "a file:// URL with a host", url: "file://localhost" + root + "/Backup/kit.git", want: onThisMac},
		{name: "git's own receivepack, set", url: in("Backup/kit.git"), pack: &gitsOwn, want: onThisMac},
		{name: "another receivepack", url: in("Backup/kit.git"), pack: &another, want: gitrepo.Place{Unproven: "remote.origin.receivepack is set, so git runs a program of its own there, which can take it anywhere"}},
		{name: "another uploadpack, fetched from", url: in("Backup/kit.git"), way: gitrepo.Fetching, pack: &another, want: gitrepo.Place{Unproven: "remote.origin.uploadpack is set, so git runs a program of its own there, which can take it anywhere"}},
		{name: "git's own receivepack, without its dash", url: in("Backup/kit.git"), pack: new("git receive-pack"), want: onThisMac},
		{name: "git's own uploadpack, without its dash, fetched from", url: in("Backup/kit.git"), way: gitrepo.Fetching, pack: new("git upload-pack"), want: onThisMac},
		{name: "git's own uploadpack, without its dash, pushed to", url: in("Backup/kit.git"), pack: new("git upload-pack"), want: gitrepo.Place{Unproven: "remote.origin.receivepack is set, so git runs a program of its own there, which can take it anywhere"}},
		{name: "git's own uploadpack, pushed to", url: in("Backup/kit.git"), pack: new("git-upload-pack"), want: gitrepo.Place{Unproven: "remote.origin.receivepack is set, so git runs a program of its own there, which can take it anywhere"}},
		{name: "in a cloud drive", url: cloud, want: inDropbox},
		{name: "in iCloud Drive", url: icloud, want: syncs("Library/Mobile Documents/com~apple~CloudDocs")},
		{name: "on the Desktop", url: filepath.Join(home, "Desktop", "kit.git"), want: mayLeave("Desktop/kit.git")},
		{name: "in Documents", url: filepath.Join(home, "Documents", "Backups", "kit.git"), want: mayLeave("Documents/Backups")},
		{name: "a link into a cloud drive", url: in("linked"), want: inDropbox},
		{name: "a file:// URL into a cloud drive", url: "file://" + cloud, want: inDropbox},
		{name: "the cloud drives' folder", url: filepath.Join(home, "Library", "CloudStorage"), want: syncs("Library/CloudStorage")},
		{name: "on a filesystem that isn't local", url: in("Backup/kit.git"), statfs: smb, want: gitrepo.Place{Unproven: "git writes it on smbfs, which isn't local"}},
		{name: "not there", url: in("gone.git"), want: noRepo},
		{name: "a folder with no repository", url: repoDir, want: noRepo},
		{name: "a plain folder, with .git beside it", url: in("plain/kit"), want: inDropbox},
		{name: "a gitfile", url: in("gitfile/kit"), want: inDropbox},
		{name: "a repository before .git beside it", url: in("bare/kit"), want: onThisMac},
		{name: "a .git file before the repository holding it", url: in("both/kit"), want: inDropbox},
		{name: "a .git in the folder with .git", url: in("nested/kit"), want: inDropbox},
		{name: "a .git in the repository with .git", url: in("nestedrepo/kit"), want: inDropbox},
		{name: "an empty .git", url: in("empty/kit"), want: onThisMac},
		{name: "a .git with no objects", url: in("noobjects/kit"), want: inDropbox},
		{name: "a .git with no refs", url: in("norefs/kit"), want: inDropbox},
		{name: "a .git whose HEAD git can't read", url: in("garbage/kit"), want: inDropbox},
		{name: "a .git whose HEAD links out of refs/", url: in("linkedout/kit"), want: inDropbox},
		{name: "a .git whose HEAD is an object", url: in("hex/kit"), want: onThisMac},
		{name: "a .git whose HEAD links into refs/", url: in("linkedhead/kit"), want: aLink(in("linkedhead/kit/.git/HEAD"))},
		{name: "a worktree", url: in("wt"), want: inDropbox},
		{name: "a worktree in Documents", url: filepath.Join(home, "Documents", "wt"), want: onThisMac},
		{name: "a worktree whose repository is on a filesystem that isn't local", url: filepath.Join(home, "Documents", "wt"), statfs: smbAt(in("Backup/kit.git")), want: gitrepo.Place{Unproven: "git writes it on smbfs, which isn't local"}},
		{name: "a worktree whose repository holds a link", url: in("links/wt"), want: aLink(in("links/common.git/objects/pack"))},
		{name: "a worktree whose git folder holds a link", url: in("links/wt2"), want: aLink(in("links/wtgit2/logs/HEAD"))},
		{name: "its hooks a link", url: in("hooked/kit.git"), want: onThisMac},
		{name: "a link in its hooks", url: in("hookfolder/kit.git"), want: onThisMac},
		{name: "a link to a repository on this Mac", url: in("tolocal"), want: onThisMac},
		{name: "a link to its folder", url: in("toparent/kit.git"), want: onThisMac},
		{name: "a folder in it kit can't read", url: in("locked/kit.git"), want: gitrepo.Place{Unproven: "kit can't read " + resolved(in("locked/kit.git/objects")) + "/locked, to tell whether anything in the repository leads elsewhere"}},
		{name: "a .git whose HEAD has a \\v before its ref", url: in("vtab/kit"), want: inDropbox},
		{name: "a .git whose HEAD has a \\f before its ref", url: in("formfeed/kit"), want: inDropbox},
		{name: "a .git whose HEAD has git's space before its ref", url: in("tabbed/kit"), want: onThisMac},
		{name: "a gitfile naming no repository", url: in("badfile/kit"), want: noRepo},
		{name: ".. after a link", url: in("up") + "/../prefs.git", want: inDropbox},
		{name: "the home's", url: "~/kit.git", env: usualHome, want: onThisMac},
		{name: "the home's Documents", url: "~/Documents/Backups/kit.git", env: usualHome, want: mayLeave("Documents/Backups")},
		{name: "the home's, no HOME", url: "~/kit.git", want: gitrepo.Place{Unproven: "HOME isn't set where git runs, so git finds no repository there"}},
		{name: "another user's home", url: "~someone/kit.git", env: usualHome, want: gitrepo.Place{Unproven: "it's in ~someone, another user's home folder, which kit doesn't look up"}},
		{name: "a filesystem mounted in it", url: in("Backup/kit.git"), device: elsewhereAt(in("Backup/kit.git/objects")), want: gitrepo.Place{Unproven: resolved(in("Backup/kit.git/objects")) + " is another filesystem"}},
		{name: "a filesystem mounted in its hooks", url: in("hookfolder/kit.git"), device: elsewhereAt(in("hookfolder/kit.git/hooks")), want: onThisMac},
		{name: "a file said to be on another filesystem", url: in("Backup/kit.git"), device: elsewhereAt(in("Backup/kit.git/HEAD")), want: onThisMac},
		{name: "a worktree whose repository is on another filesystem", url: filepath.Join(home, "Documents", "wt"), device: elsewhereAt(in("Backup/kit.git")), want: onThisMac},
		{name: "a folder gone as kit looks", url: in("going/1/kit.git"), device: removing(in("going/1/kit.git/objects/aa"), in("going/1/kit.git/objects/bb")), want: onThisMac},
		{name: "a file gone as kit looks", url: in("going/2/kit.git"), device: removing(in("going/2/kit.git/logs/refs"), in("going/2/kit.git/logs/x")), want: onThisMac},
		{name: "in Documents, with a folder kit can't read", url: lockedDocs, want: mayLeave("Documents/locked")},
		{name: "updateInstead", url: in("Backup/kit.git"), receives: "receive.denycurrentbranch\nupdateInstead\x00", want: updateInstead},
		{name: "updateInstead in another case", url: in("Backup/kit.git"), receives: "receive.denycurrentbranch\nUPDATEINSTEAD\x00", want: updateInstead},
		{name: "updateInstead, then refuse", url: in("Backup/kit.git"), receives: "receive.denycurrentbranch\nupdateInstead\x00receive.denycurrentbranch\nrefuse\x00", want: onThisMac},
		{name: "refuse, then updateInstead", url: in("Backup/kit.git"), receives: "receive.denycurrentbranch\nrefuse\x00receive.denycurrentbranch\nupdateInstead\x00", want: updateInstead},
		{name: "denyCurrentBranch with no value", url: in("Backup/kit.git"), receives: "receive.denycurrentbranch\x00", want: onThisMac},
		{name: "updateInstead, fetched from", url: in("Backup/kit.git"), way: gitrepo.Fetching, receives: "receive.denycurrentbranch\nupdateInstead\x00", want: onThisMac},
		{name: "core.hooksPath", url: in("Backup/kit.git"), receives: "core.hookspath\n/opt/hooks\x00", want: hooksPath},
		{name: "core.hooksPath empty", url: in("Backup/kit.git"), receives: "core.hookspath\n\x00", want: hooksPath},
		{name: "core.hooksPath with no value", url: in("Backup/kit.git"), receives: "core.hookspath\x00", want: hooksPath},
		{name: "core.alternateRefsCommand", url: in("Backup/kit.git"), receives: "core.alternaterefscommand\ntrue\x00", want: gitrepo.Place{Unproven: "core.alternateRefsCommand is set, so the repository runs a program of its own, which can take the push anywhere"}},
		{name: "a hook configured, named in words", url: in("Backup/kit.git"), receives: "hook.lint.event\npre-commit\x00hook.my deploy.v2.event\npost-receive\x00", want: gitrepo.Place{Unproven: "the repository runs its own post-receive hook, hook.my deploy.v2 in git's config, which can take the push anywhere"}},
		{name: "a hook configured for another", url: in("Backup/kit.git"), receives: "hook.lint.event\npre-commit\x00", want: onThisMac},
		{name: "a hook configured in another case", url: in("Backup/kit.git"), receives: "hook.Deploy.event\nPOST-RECEIVE\x00", want: onThisMac},
		{name: "a hook another git doesn't run for a push", url: in("hooks/pre-push/kit.git"), want: onThisMac},
		{name: "a hook kit may not run", url: in("hooks/unrunnable/kit.git"), want: onThisMac},
		{name: "a sample", url: in("hooks/sample/kit.git"), want: onThisMac},
		{name: "a folder named as a hook", url: in("hooks/folder/kit.git"), want: onThisMac},
		{name: "a link to a hook elsewhere", url: in("hooks/linked/kit.git"), want: runsItsOwn("post-receive")},
		{name: "hooks a link to a folder holding one", url: in("hooks/linkedfolder/kit.git"), want: runsItsOwn("post-receive")},
		{name: "a worktree whose repository's hooks hold one", url: in("hooks/wt"), want: runsItsOwn("post-receive")},
		{name: "a worktree whose own git folder's hooks hold one", url: in("hooks/wt2"), want: onThisMac},
		{name: "a hook, fetched from", url: in("hooks/post-receive/kit.git"), way: gitrepo.Fetching, want: onThisMac},
	} {
		if got, err := c.place(t, repoDir, home); err != nil || got != c.want {
			t.Errorf("%s: Place(%q) = %+v, %v; want %+v", c.name, c.url, got, err, c.want)
		}
	}
	for i, l := range inRepo {
		repo := in(fmt.Sprintf("links/%d/kit.git", i))
		if got, err := (localCase{url: repo}).place(t, repoDir, home); err != nil || got != aLink(filepath.Join(repo, l.at)) {
			t.Errorf("%s: Place(%q) = %+v, %v; want the link said", l.name, repo, got, err)
		}
	}
	for _, name := range receiveHooks {
		repo := in("hooks/" + name + "/kit.git")
		if got, err := (localCase{url: repo}).place(t, repoDir, home); err != nil || got != runsItsOwn(name) {
			t.Errorf("Place(%q), its hooks holding %s = %+v, %v; want %+v", repo, name, got, err, runsItsOwn(name))
		}
		configured := gitrepo.Place{Unproven: "the repository runs its own " + name + " hook, hook.mine in git's config, which can take the push anywhere"}
		if got, err := (localCase{url: in("Backup/kit.git"), receives: "hook.mine.event\n" + name + "\x00"}).place(t, repoDir, home); err != nil || got != configured {
			t.Errorf("Place(), a hook configured for %s = %+v, %v; want %+v", name, got, err, configured)
		}
	}
	for i, h := range hardLinked {
		repo := in(fmt.Sprintf("hardlinks/%d/kit.git", i))
		want := onThisMac
		if h.refused {
			at := filepath.Join(repo, h.at)
			want = gitrepo.Place{Unproven: resolved(filepath.Dir(at)) + "/" + filepath.Base(at) + " is hard-linked, so git writes it wherever its other links are"}
		}
		if got, err := (localCase{url: repo}).place(t, repoDir, home); err != nil || got != want {
			t.Errorf("%s hard-linked: Place(%q) = %+v, %v; want %+v", h.name, repo, got, err, want)
		}
	}
	// The repository's path, or the home, spelled otherwise, through a
	// link, in another case, as a case-insensitive filesystem, macOS's by
	// default, takes it, or by the data volume's own path, is the same
	// folder, told by its file.
	link(t, home, in("linkedhome"))
	spellings := []struct{ name, root, home string }{
		{name: "the home a link", root: root, home: in("linkedhome")},
		{name: "the home in another case", root: root, home: filepath.Join(root, "HOME")},
		{name: "the path in another case", root: strings.ToLower(root), home: home},
		{name: "the path by the data volume", root: "/System/Volumes/Data" + root, home: home},
		{name: "the home by the data volume", root: root, home: "/System/Volumes/Data" + home},
	}
	for _, sp := range spellings {
		if !sameFile(sp.root, root) || !sameFile(sp.home, home) {
			continue
		}
		spelled := func(path string) string { return sp.root + strings.TrimPrefix(path, root) }
		for _, c := range []localCase{
			{url: spelled(cloud), want: syncs("Library/CloudStorage/" + filepath.Base(spelled(dropbox)))},
			{url: spelled(filepath.Join(home, "Desktop", "kit.git")), want: mayLeave(filepath.Base(spelled(filepath.Join(home, "Desktop"))) + "/kit.git")},
			{url: spelled(in("Backup/kit.git")), want: onThisMac},
		} {
			if got, err := c.place(t, repoDir, sp.home); err != nil || got != c.want {
				t.Errorf("%s: Place(%q) = %+v, %v; want %+v", sp.name, c.url, got, err, c.want)
			}
		}
	}
}

// sameFile reports whether the paths a and b are the same file, there.
func sameFile(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
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
		{usual + ":someone/kit.git", runs(sshSettings(gitrepo.Pushing, nil), sshG(said(usual, "github.com", "22"), usual, "git-receive-pack 'someone/kit.git'"), visibility("someone/kit", "PRIVATE"))},
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
// its config, ssh its, ssh naming no host, or failing asked of the
// sentinel; gh asked, or not signed in, which it says on two lines, the
// advice on the first, then exits 4; a runner that can't say what
// environment git runs in; a local path without the home to tell what
// syncs.
func TestPlaceFails(t *testing.T) {
	const ssh = usual + ":someone/kit.git"
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
		{name: "ssh's config unreadable", url: ssh, runs: sshSettings(gitrepo.Pushing, nil), fails: sshG("", usual, pushed), exits: 255, stderr: "/Users/someone/.ssh/config line 3: Bad configuration option: hostnme\n", says: "can't tell which host ssh reaches for github.com: "},
		{name: "ssh naming no host", url: ssh, runs: sshSettings(gitrepo.Pushing, nil), fails: sshG("user git\nport 22\n", usual, pushed), says: "can't tell which host ssh reaches for github.com"},
		{name: "ssh failing asked of the sentinel", url: ssh, runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(usual, "github.com", "22", "controlpath /tmp/kit-shared-socket\n"), usual, pushed)), fails: sshG("", "-p", "22", "git@"+sentinel, pushed), exits: 255, stderr: "/Users/someone/.ssh/config line 3: Bad configuration option: controlpth\n", says: "can't tell which host ssh reaches for github.com: "},
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

// A local path whose places kit can't look through all fails, never a
// place: a filesystem that won't say what it is, git's config unreadable
// for the remote's program, or the repository's own, the repository gone
// as kit looks through it.
func TestPlaceFailsOnThisMac(t *testing.T) {
	root := t.TempDir()
	readable := filepath.Join(root, "readable.git")
	gitRepoAt(t, readable)
	readableAt, err := filepath.EvalSymlinks(readable)
	if err != nil {
		t.Fatal(err)
	}
	going := filepath.Join(root, "going.git")
	gitRepoAt(t, going)
	refused := func(string, *unix.Statfs_t) error { return unix.EACCES }
	gone := func(_ string, st *unix.Statfs_t) error {
		if err := os.RemoveAll(going); err != nil {
			t.Error(err)
		}
		return unix.Statfs(root, st)
	}
	for _, c := range []struct {
		name, url string
		statfs    func(string, *unix.Statfs_t) error
		config    bool
		receiving bool
		says      string
	}{
		{name: "a filesystem that won't say", url: readable, statfs: refused, says: "can't tell what holds "},
		{name: "git's config unreadable", url: readable, config: true, says: "fatal: bad config line 3 in file .git/config"},
		{name: "the repository's config unreadable", url: readable, receiving: true, says: "can't read the config of the repository at " + readableAt + ": "},
		{name: "the repository gone as kit looks through it", url: going, statfs: gone, says: "can't tell where git writes " + going + ": "},
	} {
		fake := runnertest.New(t)
		fake.On("git", git("config", "--get", originVCS)...).Exits(1)
		pack := fake.On("git", git("config", "-z", "--get-all", receivePack)...).Exits(1)
		if c.config {
			pack.Exits(128).PrintsToStderr("fatal: bad config line 3 in file .git/config\n")
		}
		settings := fake.On("git", "--git-dir=.", "config", "-z", "--get-regexp", receiving).Exits(1)
		if c.receiving {
			settings.Exits(128).PrintsToStderr("fatal: bad config line 1 in file config\n")
		}
		place, err := gitrepo.WithStatfs(gitrepo.Repo{Dir: dir, Run: fake, Home: filepath.Join(root, "home")}, c.statfs).Place(context.Background(), "origin", c.url, gitrepo.Pushing)
		if err == nil || !strings.Contains(err.Error(), c.says) || place != (gitrepo.Place{}) {
			t.Errorf("%s: Place() = %+v, %v; want an error saying %q", c.name, place, err, c.says)
		}
	}
}

// The repository a push reaches is asked its settings as receive-pack
// reads them, in its git folder: a worktree's own, which reads its own
// settings after its repository's, never its common folder.
func TestPlaceAsksTheRepositoryInItsGitFolder(t *testing.T) {
	root := t.TempDir()
	gitRepoAt(t, filepath.Join(root, "kit.git"))
	gitFolderAt(t, filepath.Join(root, "wtgit"), "ref: refs/heads/wt\n")
	writeAt(t, filepath.Join(root, "wtgit", "commondir"), "../kit.git\n")
	writeAt(t, filepath.Join(root, "wt", ".git"), "gitdir: ../wtgit\n")
	asked := []string{"--git-dir=.", "config", "-z", "--get-regexp", receiving}
	for url, at := range map[string]string{filepath.Join(root, "wt"): "wtgit", filepath.Join(root, "kit.git"): "kit.git"} {
		fake := runnertest.New(t)
		fake.On("git", git("config", "--get", originVCS)...).Exits(1)
		fake.On("git", git("config", "-z", "--get-all", receivePack)...).Exits(1)
		fake.On("git", asked...).Exits(1)
		place, err := gitrepo.Repo{Dir: dir, Run: fake, Home: filepath.Join(root, "home")}.Place(context.Background(), "origin", url, gitrepo.Pushing)
		var in []string
		for _, cmd := range fake.Commands() {
			if slices.Equal(cmd.Args, asked) {
				in = append(in, cmd.Dir)
			}
		}
		want, err2 := filepath.EvalSymlinks(filepath.Join(root, at))
		if err2 != nil {
			t.Fatal(err2)
		}
		if err != nil || !place.OnThisMac || !slices.Equal(in, []string{want}) {
			t.Errorf("Place(%q) = %+v, %v, asking in %q; want on this Mac, asking in %q", url, place, err, in, want)
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
		{usual + ":someone/kit.git", "github.com:someone/kit.git"},
		{"x-access-token:T@" + "github.com:someone/kit", "github.com:someone/kit"},
		{"/Volumes/Backup/@kit/prefs.git", "/Volumes/Backup/@kit/prefs.git"},
	} {
		if got := gitrepo.Shown(c.url); got != c.want {
			t.Errorf("Shown(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
