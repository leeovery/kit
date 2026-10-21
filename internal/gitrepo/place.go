package gitrepo

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// Place is where a remote's URL takes the repository, as far as kit can
// prove it private: this Mac (OnThisMac), or a repository on GitHub (Repo, as
// owner/name) with its visibility there, as gh says it; else Unproven says
// why kit can't tell where it goes.
type Place struct {
	OnThisMac        bool
	Repo, Visibility string
	Unproven         string
}

// Private reports whether the place is proven private: on this Mac, or a
// repository GitHub says is private.
func (p Place) Private() bool {
	return p.OnThisMac || p.Repo != "" && p.Visibility == "private"
}

// builtIn are the schemes git reaches by itself, in lower case, as it
// takes them: any other it hands to a helper, which alone knows where it
// goes.
var builtIn = []string{"ssh", "git+ssh", "ssh+git", "git", "http", "https", "ftp", "ftps", "file"}

// overSSH are the schemes git reaches over ssh.
var overSSH = []string{"ssh", "git+ssh", "ssh+git"}

// scheme is a URL's scheme, as git tells a URL; helper a URL git hands to
// the remote helper it names, as in gcrypt::….
var (
	scheme = regexp.MustCompile(`^([A-Za-z0-9+.-]+)://`)
	helper = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*::`)
)

// plainHost is a host name in plain ASCII, lower case: never one a
// look-alike letter could pass for GitHub's.
var plainHost = regexp.MustCompile(`^[a-z0-9.-]+$`)

// githubName is an owner's or a repository's name on GitHub, never one gh
// would take for a flag.
var githubName = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)

// address is a remote's URL as git reads it: its scheme, "file" for a path
// on this Mac and "ssh" for ssh's [user@]host:path; who it signs in as, its
// host and port, as written; and its path there.
type address struct {
	scheme, user, host, port, path string
}

// Place is where the remote called remote, its URL url, as git takes it,
// takes the repository, as far as kit can prove it private (Place). Proof
// is positive: this Mac's, a local path or a file:// URL; or a repository
// on GitHub, the host git reaches github.com or a host under it, as ssh's
// config resolves an ssh host, given as git gives it, its user and port
// with it, and gh asked its visibility. Anything else is unproven: a URL
// git hands to a helper, by its scheme, a :: or the remote's vcs setting;
// an ssh command set, or a proxy in ssh's config, which can take it
// anywhere; another host; or one kit can't read. An error is a failure to
// ask: git, ssh or gh.
func (r Repo) Place(ctx context.Context, remote, url string) (Place, error) {
	vcs, err := r.setting(ctx, "remote."+remote+".vcs")
	switch {
	case err != nil:
		return Place{}, err
	case vcs != "":
		return unproven("remote.%s.vcs hands it to a helper, which alone knows where it goes", remote)
	}
	a, why := read(url)
	if why != "" {
		return unproven("%s", why)
	}
	var host string
	switch {
	case a.scheme == "file":
		return Place{OnThisMac: true}, nil
	case slices.Contains(overSSH, a.scheme):
		if host, why, err = r.sshHost(ctx, a); err != nil || why != "" {
			return Place{Unproven: why}, err
		}
	case a.scheme == "git":
		if why, err = r.proxied(ctx); err != nil || why != "" {
			return Place{Unproven: why}, err
		}
		host = strings.ToLower(a.host)
	default:
		host = strings.ToLower(a.host)
	}
	return r.onGitHub(ctx, strings.TrimSuffix(host, "."), a.path)
}

// unproven is a place kit can't prove private, for why.
func unproven(format string, args ...any) (Place, error) {
	return Place{Unproven: fmt.Sprintf(format, args...)}, nil
}

// read reads remote, a URL, as git does: a helper's, for :: or a scheme git
// doesn't reach itself; a URL with a scheme, by net/url; else, with a colon
// before any slash, ssh's [user@]host:path; else a path on this Mac. why
// is why kit can't tell where it goes: "" when it can.
func read(remote string) (a address, why string) {
	const unread = "kit can't read where it goes"
	if helper.MatchString(remote) {
		return address{}, "git hands it to a helper, which alone knows where it goes"
	}
	if m := scheme.FindStringSubmatch(remote); m != nil {
		switch {
		case !slices.Contains(builtIn, m[1]):
			return address{}, fmt.Sprintf("git hands %s:// URLs to a helper, which alone knows where they go", m[1])
		case m[1] == "file":
			return address{scheme: "file"}, ""
		}
		u, err := url.Parse(remote)
		if err != nil || u.Hostname() == "" {
			return address{}, unread
		}
		return address{scheme: m[1], user: u.User.Username(), host: u.Hostname(), port: u.Port(), path: u.Path}, ""
	}
	colon, slash := strings.Index(remote, ":"), strings.Index(remote, "/")
	if colon < 0 || slash >= 0 && slash < colon {
		return address{scheme: "file"}, ""
	}
	a = address{scheme: "ssh", host: remote[:colon], path: remote[colon+1:]}
	if at := strings.LastIndex(a.host, "@"); at >= 0 {
		a.user, a.host = a.host[:at], a.host[at+1:]
	}
	if a.host == "" || strings.Contains(a.host, "[") {
		return address{}, unread
	}
	return a, ""
}

// sshHost is the host ssh reaches for a, as its config resolves a's host,
// given as git gives it, with its user and port, in lower case; why, when
// kit can't tell where ssh takes it: an ssh command set, a host not in
// plain ASCII or like a flag, or a proxy in ssh's config. ssh -G reads the
// config alone, connecting nowhere.
func (r Repo) sshHost(ctx context.Context, a address) (host, why string, err error) {
	if why, err := r.sshCommand(ctx); err != nil || why != "" {
		return "", why, err
	}
	dest := a.host
	if a.user != "" {
		dest = a.user + "@" + a.host
	}
	switch {
	case strings.HasPrefix(dest, "-"):
		return "", "kit can't read where it goes", nil
	case strings.ContainsFunc(a.host, func(c rune) bool { return c > '~' }):
		return "", fmt.Sprintf("its host, %+q, isn't plain ASCII", a.host), nil
	}
	args := []string{"-G"}
	if a.port != "" {
		args = append(args, "-p", a.port)
	}
	res, err := r.Run.Run(ctx, runner.Command{Name: "ssh", Args: append(args, dest)})
	if err != nil {
		return "", "", fmt.Errorf("can't tell which host ssh reaches for %s: %w", a.host, err)
	}
	for line := range strings.Lines(string(res.Stdout)) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch {
		case key == "hostname":
			host = strings.ToLower(value)
		case (key == "proxycommand" || key == "proxyjump") && value != "none":
			return "", fmt.Sprintf("ssh's config takes it through a proxy (%s), which can take it anywhere", key), nil
		}
	}
	if host == "" {
		return "", "", fmt.Errorf("can't tell which host ssh reaches for %s", a.host)
	}
	return host, "", nil
}

// sshCommand is why kit can't tell where ssh takes a push: an ssh command
// git runs in ssh's place, set in the environment (GIT_SSH_COMMAND,
// GIT_SSH) or git's config (core.sshCommand); "" with none.
func (r Repo) sshCommand(ctx context.Context) (string, error) {
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		if r.getenv(name) != "" {
			return name + " is set, so git may not reach it with ssh as its config says", nil
		}
	}
	command, err := r.setting(ctx, "core.sshCommand")
	if err != nil || command == "" {
		return "", err
	}
	return "core.sshCommand is set, so git may not reach it with ssh as its config says", nil
}

// proxied is why kit can't tell where git's own protocol takes a fetch: a
// proxy command git runs for it (GIT_PROXY_COMMAND, core.gitProxy); "" with
// none.
func (r Repo) proxied(ctx context.Context) (string, error) {
	if r.getenv("GIT_PROXY_COMMAND") != "" {
		return "GIT_PROXY_COMMAND is set, which can take it anywhere", nil
	}
	command, err := r.setting(ctx, "core.gitProxy")
	if err != nil || command == "" {
		return "", err
	}
	return "core.gitProxy is set, which can take it anywhere", nil
}

// onGitHub is the place at path on host, in lower case: a repository on
// GitHub, its visibility asked through gh, when host is github.com or a
// host under it, in plain ASCII, and path an owner's repository; else
// unproven.
func (r Repo) onGitHub(ctx context.Context, host, path string) (Place, error) {
	switch {
	case !plainHost.MatchString(host):
		return unproven("%+q isn't GitHub", host)
	case host != "github.com" && !strings.HasSuffix(host, ".github.com"):
		return unproven("%s isn't GitHub", host)
	}
	parts := strings.Split(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
	if len(parts) != 2 || !githubName.MatchString(parts[0]) || !githubName.MatchString(parts[1]) {
		return unproven("it isn't an owner's repository on GitHub")
	}
	repo := parts[0] + "/" + parts[1]
	// The URL names github.com to gh, whatever host GH_HOST names.
	res, err := r.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "view", "https://github.com/" + repo, "--json", "visibility", "--jq", ".visibility"}})
	if err != nil {
		return Place{}, fmt.Errorf("couldn't ask GitHub: %w", err)
	}
	return Place{Repo: repo, Visibility: strings.ToLower(strings.TrimSpace(string(res.Stdout)))}, nil
}

// Shown is remote, a URL, as kit names it: without who it signs in as,
// which a URL's sign-in may carry a token in.
func Shown(remote string) string {
	scheme, rest, ok := strings.Cut(remote, "://")
	if !ok {
		head, _, _ := strings.Cut(remote, "/")
		return remote[strings.LastIndex(head, "@")+1:]
	}
	authority, _, _ := strings.Cut(rest, "/")
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return remote
	}
	opened := ""
	if strings.HasPrefix(authority, "[") {
		opened = "["
	}
	return scheme + "://" + opened + rest[at+1:]
}
