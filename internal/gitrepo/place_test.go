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

// keygen is ssh-keygen asked whether file holds name's key, as real
// ssh-keygen answers: found, exiting 0, printing the line (found); else
// exiting 1, or 255 for a file that isn't there.
func keygen(name, file string, answer int) ran {
	r := ran{name: "ssh-keygen", args: []string{"-F", name, "-f", file}, exits: answer}
	if answer == 0 {
		r.prints = found(name, "1", "", name)
	}
	return r
}

// keygenFinds is ssh-keygen finding lines, each as found prints it, in
// file for name, exiting 0.
func keygenFinds(name, file string, lines ...string) ran {
	return ran{name: "ssh-keygen", args: []string{"-F", name, "-f", file}, prints: strings.Join(lines, "")}
}

// found is what real ssh-keygen -F prints of line n of a file of known
// hosts, found for name, holding a key for hosts: its header, then the
// line, which marker, REVOKED or CA after the header, says begins @revoked
// or @cert-authority, none when "".
func found(name, n, marker, hosts string) string {
	line := hosts + " ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"
	switch marker {
	case "REVOKED":
		line = "@revoked " + line
	case "CA":
		line = "@cert-authority " + line
	}
	return "# Host " + name + " found: line " + n + " " + marker + "\n" + line
}

// hashed is a host as a hashed line of known hosts holds it, made up.
const hashed = "|1|ZmFrZXNhbHRmYWtlc2FsdGZha2U=|ZmFrZWhhc2hmYWtlaGFzaGZha2U="

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
// none, or from DNS only on asking, or, a key taken on first sight, the
// host's own already known in one, on its port, in a line plain, hashed or
// a pattern, a revoked key or a certificate authority's beside it, in a
// file whose path holds a space too.
// A controlpath not the same for a host no config means, asked as git's
// call with the user and port ssh resolved, leaves it proven. Every answer
// is real git's, ssh's or ssh-keygen's.
func TestPlaceProvesPrivate(t *testing.T) {
	const repo = "someone/kit"
	private := gitrepo.Place{Repo: repo, Visibility: "private"}
	githubSaid := said(usual, "github.com", "22")
	firstSight := saidWith(githubSaid, "stricthostkeychecking", "accept-new")
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
		{name: "a key taken on first sight, the host's known", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "stricthostkeychecking", "accept-new"), usual, "git-receive-pack 'someone/kit.git'"), keygen("github.com", "/Users/someone/.ssh/known_hosts", 0), visibility(repo, "PRIVATE")), want: private},
		{name: "no file of known hosts", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "userknownhostsfile", "/dev/null"), usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "known hosts none", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "userknownhostsfile", "none"), usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "/dev/null among the files of known hosts", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "userknownhostsfile", "/Users/someone/.ssh/known_hosts /dev/null"), usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "the host's key from DNS on asking", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "verifyhostkeydns", "ask"), usual, "git-receive-pack 'someone/kit.git'"), visibility(repo, "PRIVATE")), want: private},
		{name: "a key taken on first sight, no file of the user's, the host's known in the system's", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(firstSight, "userknownhostsfile", "/dev/null"), usual, "git-receive-pack 'someone/kit.git'"), keygen("github.com", "/dev/null", 1), keygen("github.com", "/etc/ssh/ssh_known_hosts", 0), visibility(repo, "PRIVATE")), want: private},
		{name: "a key taken on first sight, the host's known in a hashed line", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(firstSight, usual, "git-receive-pack 'someone/kit.git'"), keygenFinds("github.com", "/Users/someone/.ssh/known_hosts", found("github.com", "1", "", hashed)), visibility(repo, "PRIVATE")), want: private},
		{name: "a key taken on first sight, the host's known by a pattern", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(firstSight, usual, "git-receive-pack 'someone/kit.git'"), keygenFinds("github.com", "/Users/someone/.ssh/known_hosts", found("github.com", "1", "", "*.com")), visibility(repo, "PRIVATE")), want: private},
		{name: "a key taken on first sight, a revoked key, then the host's", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(firstSight, usual, "git-receive-pack 'someone/kit.git'"), keygenFinds("github.com", "/Users/someone/.ssh/known_hosts", found("github.com", "1", "REVOKED", "github.com"), found("github.com", "2", "", "github.com")), visibility(repo, "PRIVATE")), want: private},
		{name: "a key taken on first sight, a certificate authority's, then the host's among others", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(firstSight, usual, "git-receive-pack 'someone/kit.git'"), keygenFinds("github.com", "/Users/someone/.ssh/known_hosts", found("github.com", "1", "CA", "*.com"), found("github.com", "2", "", "github.com,192.0.2.1")), visibility(repo, "PRIVATE")), want: private},
		{name: "a key taken on first sight, the host's known in a file whose path holds a space", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(firstSight, "userknownhostsfile", "/Users/someone/.ssh/known hosts /Users/someone/.ssh/known_hosts2"), usual, "git-receive-pack 'someone/kit.git'"), keygen("github.com", "/Users/someone/.ssh/known hosts", 0), visibility(repo, "PRIVATE")), want: private},
		{name: "a key taken on first sight, the host's known on its port in a hashed line", url: "ssh://" + usual + ":2222/someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(said(usual, "github.com", "2222"), "stricthostkeychecking", "accept-new"), "-p", "2222", usual, "git-receive-pack '/someone/kit.git'"), keygenFinds("[github.com]:2222", "/Users/someone/.ssh/known_hosts", found("[github.com]:2222", "1", "", hashed)), visibility(repo, "PRIVATE")), want: private},
		{name: "a key taken on first sight, the host's known on its port in the system's file", url: "ssh://" + usual + ":2222/someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(said(usual, "github.com", "2222"), "stricthostkeychecking", "accept-new"), "-p", "2222", usual, "git-receive-pack '/someone/kit.git'"), keygen("[github.com]:2222", "/Users/someone/.ssh/known_hosts", 1), keygen("[github.com]:2222", "/Users/someone/.ssh/known_hosts2", 255), keygen("[github.com]:2222", "/etc/ssh/ssh_known_hosts", 0), visibility(repo, "PRIVATE")), want: private},
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
// refuses, in the environment git runs in or git's config, a proxy in
// ssh's answer, a host name it canonicalizes, the host's key not checked,
// checked by another name, against a command's keys or from DNS, or a key
// taken on first sight for a host not yet known, in any file, on its port,
// a revoked key or a certificate authority's, for it or a pattern, not its
// own, or a controlpath the same for a host no config means,
// whatever its controlmaster says, as ssh uses a connection there either
// way; over https, a variable that undoes TLS's proof of the
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
	githubSaid := said(usual, "github.com", "22")
	pushed := "git-receive-pack 'someone/kit.git'"
	shared := func(path string) gitrepo.Place {
		return gitrepo.Place{Unproven: "ssh's controlpath, " + path + ", is the same for other hosts, so a connection to another host there can carry git's: name the host in it, as %C does"}
	}
	literal := "/tmp/sock-github.com"
	byUser := "/Users/someone/.ssh/cm-git"
	firstSight := saidWith(githubSaid, "stricthostkeychecking", "accept-new")
	unknown := gitrepo.Place{Unproven: "ssh's config takes a host's key on first sight (stricthostkeychecking accept-new), and it knows none for github.com yet, so another host can pass for it"}
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
		{name: "the host's key from DNS", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(githubSaid, "verifyhostkeydns", "true"), usual, pushed)), want: gitrepo.Place{Unproven: "ssh's config takes the host's key from DNS (verifyhostkeydns), which another host can answer for"}},
		{name: "a key taken on first sight, the host's key revoked", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(firstSight, usual, pushed), keygenFinds("github.com", "/Users/someone/.ssh/known_hosts", found("github.com", "1", "REVOKED", "github.com")), keygen("github.com", "/Users/someone/.ssh/known_hosts2", 255), keygen("github.com", "/etc/ssh/ssh_known_hosts", 255), keygen("github.com", "/etc/ssh/ssh_known_hosts2", 255)), want: unknown},
		{name: "a key taken on first sight, a certificate authority for the host alone", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(firstSight, usual, pushed), keygenFinds("github.com", "/Users/someone/.ssh/known_hosts", found("github.com", "1", "CA", "github.com")), keygen("github.com", "/Users/someone/.ssh/known_hosts2", 255), keygen("github.com", "/etc/ssh/ssh_known_hosts", 255), keygen("github.com", "/etc/ssh/ssh_known_hosts2", 255)), want: unknown},
		{name: "a key taken on first sight, a certificate authority for a pattern, in the system's file", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(firstSight, usual, pushed), keygen("github.com", "/Users/someone/.ssh/known_hosts", 1), keygen("github.com", "/Users/someone/.ssh/known_hosts2", 255), keygenFinds("github.com", "/etc/ssh/ssh_known_hosts", found("github.com", "1", "CA", "*.com")), keygen("github.com", "/etc/ssh/ssh_known_hosts2", 255)), want: unknown},
		{name: "a key taken on first sight, no file of the user's, none of the system's", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(saidWith(firstSight, "userknownhostsfile", "/dev/null"), "globalknownhostsfile", "none"), usual, pushed), keygen("github.com", "/dev/null", 1)), want: unknown},
		{name: "a key taken on first sight, known hosts none", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(saidWith(firstSight, "userknownhostsfile", "none"), "globalknownhostsfile", "none"), usual, pushed)), want: unknown},
		{name: "a key taken on first sight, the host's not known", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(firstSight, usual, pushed), keygen("github.com", "/Users/someone/.ssh/known_hosts", 1), keygen("github.com", "/Users/someone/.ssh/known_hosts2", 255), keygen("github.com", "/etc/ssh/ssh_known_hosts", 255), keygen("github.com", "/etc/ssh/ssh_known_hosts2", 255)), want: unknown},
		{name: "a key taken on first sight, the host known on another port", url: "ssh://" + usual + ":2222/someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(firstSight, "port", "2222"), "-p", "2222", usual, "git-receive-pack '/someone/kit.git'"), keygen("[github.com]:2222", "/Users/someone/.ssh/known_hosts", 1), keygen("[github.com]:2222", "/Users/someone/.ssh/known_hosts2", 255), keygen("[github.com]:2222", "/etc/ssh/ssh_known_hosts", 255), keygen("[github.com]:2222", "/etc/ssh/ssh_known_hosts2", 255)), want: unknown},
		{name: "a key taken on first sight, no system's file", url: usual + ":someone/kit.git", runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(firstSight, "globalknownhostsfile", "none"), usual, pushed), keygen("github.com", "/Users/someone/.ssh/known_hosts", 1), keygen("github.com", "/Users/someone/.ssh/known_hosts2", 255)), want: unknown},
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
// none), what the filesystem says of itself, and the place Place makes of
// it.
type localCase struct {
	name, url string
	way       gitrepo.Way
	env       []string
	pack      *string
	statfs    func(string, *unix.Statfs_t) error
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
	repo := gitrepo.WithStatfs(gitrepo.Repo{Dir: repoDir, Run: fake, Home: home}, c.statfs)
	return repo.Place(context.Background(), "origin", c.url, c.way)
}

// A local path, or a file:// URL, is this Mac's only while git runs its own
// program at the other end, never one the remote sets, and everywhere git
// writes the repository it finds there stays on the Mac: on a filesystem
// that's local, and outside the folders in the home whose files may leave
// it, cloud drives' and iCloud Drive's, which sync off it, and the Desktop
// and Documents, which iCloud may sync, told by their files, so by any
// name; else unproven, saying where it syncs or that it isn't local. git
// finds the repository as real git does at the other end of a push: the
// path's trailing slashes dropped, a leading ~ the home in git's
// environment, then the path with /.git, nothing, .git/.git and .git added
// in turn, the first a file, or a folder git takes for a repository (a
// HEAD naming a branch, by a ref or a link into refs/, or an object, and
// objects and refs, a worktree's in its common folder; space before a
// HEAD's ref as git takes space, never \v or \f), a gitfile followed to
// the folder it names, with no other tried should that be none, and never
// itself a place git writes; links followed, and .. after a link where the
// link leads; in the repository, every link in the folders git writes all
// through, objects, refs, logs and worktrees, and where each link at its
// top leads, never in it, a link that leads nowhere the nearest folder on
// its way there, as git makes the file it names; never hooks or info,
// which a push or fetch doesn't write. git's own program at the other end
// is git's too without its dash. A relative path is the repository's; a file:// URL's path is
// decoded, its host ignored, as git takes them.
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
	// A repository whose objects are a link into the cloud; one whose
	// packs are; one with a link that leads nowhere.
	gitRepoAt(t, in("objects/kit.git"))
	gitFolderAt(t, filepath.Join(dropbox, "objects"), "")
	if err := os.Remove(in("objects/kit.git/objects")); err != nil {
		t.Fatal(err)
	}
	link(t, filepath.Join(dropbox, "objects"), in("objects/kit.git/objects"))
	gitRepoAt(t, in("packs/kit.git"))
	gitFolderAt(t, filepath.Join(dropbox, "pack"), "")
	link(t, filepath.Join(dropbox, "pack"), in("packs/kit.git/objects/pack"))
	gitRepoAt(t, in("smbpacks/kit.git"))
	gitFolderAt(t, in("elsewhere/pack"), "")
	link(t, in("elsewhere/pack"), in("smbpacks/kit.git/objects/pack"))
	gitRepoAt(t, in("dangling/kit.git"))
	link(t, in("nowhere/deeper"), in("dangling/kit.git/objects/info"))
	// A repository with a link that leads nowhere, in its logs, into the
	// cloud, and one with such a link from its own folder; one with a link
	// in its worktrees into the cloud; one with its branches a link into
	// the cloud; one whose
	// packed-refs is; one with a link at its top to /, and another to a
	// folder holding a link into the cloud; one whose hooks are a link to
	// Documents and whose info is one into the cloud.
	gitRepoAt(t, in("lost/kit.git"))
	link(t, filepath.Join(dropbox, "logs", "side"), in("lost/kit.git/logs/refs/heads/side"))
	gitRepoAt(t, in("lostnear/kit.git"))
	link(t, "../../../home/Library/CloudStorage/Dropbox/lost-head", in("lostnear/kit.git/logs/HEAD"))
	gitRepoAt(t, in("wts/kit.git"))
	link(t, cloud, in("wts/kit.git/worktrees/wt/logs"))
	gitRepoAt(t, in("branches/kit.git"))
	gitFolderAt(t, filepath.Join(dropbox, "heads"), "")
	link(t, filepath.Join(dropbox, "heads"), in("branches/kit.git/refs/heads"))
	gitRepoAt(t, in("packed/kit.git"))
	link(t, filepath.Join(dropbox, "packed-refs"), in("packed/kit.git/packed-refs"))
	gitRepoAt(t, in("rooted/kit.git"))
	link(t, "/", in("rooted/kit.git/lfs"))
	gitRepoAt(t, in("ontop/kit.git"))
	link(t, cloud, in("aside/cloud"))
	link(t, in("aside"), in("ontop/kit.git/lfs"))
	gitRepoAt(t, in("hooked/kit.git"))
	gitFolderAt(t, filepath.Join(home, "Documents", "hooks"), "")
	link(t, filepath.Join(home, "Documents", "hooks"), in("hooked/kit.git/hooks"))
	link(t, cloud, in("hooked/kit.git/info"))
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
		{name: "objects on a filesystem that isn't local", url: in("smbpacks/kit.git"), statfs: smbAt(in("elsewhere/pack")), want: gitrepo.Place{Unproven: "git writes it on smbfs, which isn't local"}},
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
		{name: "a .git whose HEAD links into refs/", url: in("linkedhead/kit"), want: onThisMac},
		{name: "a worktree", url: in("wt"), want: inDropbox},
		{name: "objects linked", url: in("objects/kit.git"), want: inDropbox},
		{name: "packs linked", url: in("packs/kit.git"), want: inDropbox},
		{name: "a link to nowhere on this Mac in it", url: in("dangling/kit.git"), want: onThisMac},
		{name: "a link to nowhere in the cloud in its logs", url: in("lost/kit.git"), want: inDropbox},
		{name: "a link to nowhere in the cloud, from its own folder", url: in("lostnear/kit.git"), want: inDropbox},
		{name: "a link in its worktrees into the cloud", url: in("wts/kit.git"), want: inDropbox},
		{name: "its branches linked", url: in("branches/kit.git"), want: inDropbox},
		{name: "its packed-refs linked", url: in("packed/kit.git"), want: inDropbox},
		{name: "a link at its top to /", url: in("rooted/kit.git"), want: onThisMac},
		{name: "a link at its top to a folder holding a link into the cloud", url: in("ontop/kit.git"), want: onThisMac},
		{name: "its hooks and info linked", url: in("hooked/kit.git"), want: onThisMac},
		{name: "a worktree in Documents", url: filepath.Join(home, "Documents", "wt"), want: onThisMac},
		{name: "a .git whose HEAD has a \\v before its ref", url: in("vtab/kit"), want: inDropbox},
		{name: "a .git whose HEAD has a \\f before its ref", url: in("formfeed/kit"), want: inDropbox},
		{name: "a .git whose HEAD has git's space before its ref", url: in("tabbed/kit"), want: onThisMac},
		{name: "a gitfile naming no repository", url: in("badfile/kit"), want: noRepo},
		{name: ".. after a link", url: in("up") + "/../prefs.git", want: inDropbox},
		{name: "the home's", url: "~/kit.git", env: usualHome, want: onThisMac},
		{name: "the home's Documents", url: "~/Documents/Backups/kit.git", env: usualHome, want: mayLeave("Documents/Backups")},
		{name: "the home's, no HOME", url: "~/kit.git", want: gitrepo.Place{Unproven: "HOME isn't set where git runs, so git finds no repository there"}},
		{name: "another user's home", url: "~someone/kit.git", env: usualHome, want: gitrepo.Place{Unproven: "it's in ~someone, another user's home folder, which kit doesn't look up"}},
	} {
		if got, err := c.place(t, repoDir, home); err != nil || got != c.want {
			t.Errorf("%s: Place(%q) = %+v, %v; want %+v", c.name, c.url, got, err, c.want)
		}
	}
	// A case-insensitive filesystem, as macOS's is by default, takes a
	// synced folder by any case.
	if lower := strings.ToLower(cloud); fileThere(lower) {
		if got, err := (localCase{url: lower}).place(t, repoDir, home); err != nil || got != syncs("Library/CloudStorage/dropbox") {
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

// git, ssh, ssh-keygen or gh failing to answer is an error, never a place:
// git reading its config, ssh its, ssh naming no host, or failing asked of
// the sentinel; ssh-keygen not there to say whether a host's key is known;
// gh asked, or not signed in, which it says on two lines, the advice on the
// first, then exits 4; a runner that can't say what environment git runs
// in; a local path without the home to tell what syncs.
func TestPlaceFails(t *testing.T) {
	const ssh = usual + ":someone/kit.git"
	const tls = "https://github.com/someone/kit"
	pushed := "git-receive-pack 'someone/kit.git'"
	for _, c := range []struct {
		name, url string
		// runs are what Place runs before what fails, which exits exits,
		// saying stderr, or isn't found; repo the repository, as a test's
		// but for runs.
		runs     []ran
		fails    ran
		exits    int
		stderr   string
		notFound bool
		repo     func(*runnertest.Fake) gitrepo.Repo
		says     string
		exactly  bool
	}{
		{name: "git's config unreadable", url: ssh, fails: setting(originVCS, ""), exits: 128, stderr: "fatal: bad config line 3 in file .git/config\n", says: "fatal: bad config line 3 in file .git/config"},
		{name: "ssh's config unreadable", url: ssh, runs: sshSettings(gitrepo.Pushing, nil), fails: sshG("", usual, pushed), exits: 255, stderr: "/Users/someone/.ssh/config line 3: Bad configuration option: hostnme\n", says: "can't tell which host ssh reaches for github.com: "},
		{name: "ssh naming no host", url: ssh, runs: sshSettings(gitrepo.Pushing, nil), fails: sshG("user git\nport 22\n", usual, pushed), says: "can't tell which host ssh reaches for github.com"},
		{name: "ssh failing asked of the sentinel", url: ssh, runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(said(usual, "github.com", "22", "controlpath /tmp/kit-shared-socket\n"), usual, pushed)), fails: sshG("", "-p", "22", "git@"+sentinel, pushed), exits: 255, stderr: "/Users/someone/.ssh/config line 3: Bad configuration option: controlpth\n", says: "can't tell which host ssh reaches for github.com: "},
		{name: "no ssh-keygen", url: ssh, runs: runs(sshSettings(gitrepo.Pushing, nil), sshG(saidWith(said(usual, "github.com", "22"), "stricthostkeychecking", "accept-new"), usual, pushed)), fails: keygen("github.com", "/Users/someone/.ssh/known_hosts", 0), notFound: true, says: "can't tell whether ssh knows github.com's key: "},
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
		switch {
		case c.notFound:
			c.fails.script(fake).Fails(runner.ErrNotFound)
		case c.fails.name != "":
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
// place: a folder in the repository it can't read, a filesystem that won't
// say what it is, git's config unreadable for the remote's program.
func TestPlaceFailsOnThisMac(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "kit.git")
	gitRepoAt(t, repo)
	locked := filepath.Join(repo, "objects", "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	readable := filepath.Join(root, "readable.git")
	gitRepoAt(t, readable)
	refused := func(string, *unix.Statfs_t) error { return unix.EACCES }
	for _, c := range []struct {
		name, url string
		statfs    func(string, *unix.Statfs_t) error
		config    bool
		says      string
	}{
		{name: "a folder kit can't read", url: repo, says: "can't tell where git writes " + repo + ": "},
		{name: "a filesystem that won't say", url: readable, statfs: refused, says: "can't tell what holds "},
		{name: "git's config unreadable", url: readable, config: true, says: "fatal: bad config line 3 in file .git/config"},
	} {
		fake := runnertest.New(t)
		fake.On("git", git("config", "--get", originVCS)...).Exits(1)
		pack := fake.On("git", git("config", "-z", "--get-all", receivePack)...).Exits(1)
		if c.config {
			pack.Exits(128).PrintsToStderr("fatal: bad config line 3 in file .git/config\n")
		}
		place, err := gitrepo.WithStatfs(gitrepo.Repo{Dir: dir, Run: fake, Home: filepath.Join(root, "home")}, c.statfs).Place(context.Background(), "origin", c.url, gitrepo.Pushing)
		if err == nil || !strings.Contains(err.Error(), c.says) || place != (gitrepo.Place{}) {
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
		{usual + ":someone/kit.git", "github.com:someone/kit.git"},
		{"x-access-token:T@" + "github.com:someone/kit", "github.com:someone/kit"},
		{"/Volumes/Backup/@kit/prefs.git", "/Volumes/Backup/@kit/prefs.git"},
	} {
		if got := gitrepo.Shown(c.url); got != c.want {
			t.Errorf("Shown(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
