package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

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

// Way is which way git goes to a remote: pushing to it, or fetching from
// it, each running its own program at the other end, which ssh's config can
// tell apart.
type Way int

// The ways git goes to a remote.
const (
	Pushing Way = iota
	Fetching
)

// pack is the program git runs at the remote's end, going w: the remote's
// setting for it, else git's own.
func (w Way) pack() (setting, program string) {
	if w == Fetching {
		return "uploadpack", "git-upload-pack"
	}
	return "receivepack", "git-receive-pack"
}

// builtIn are the schemes git reaches by itself, in lower case, as it
// takes them: any other it hands to a helper, which alone knows where it
// goes.
var builtIn = []string{"ssh", "git+ssh", "ssh+git", "git", "http", "https", "ftp", "ftps", "file"}

// overSSH are the schemes git reaches over ssh.
var overSSH = []string{"ssh", "git+ssh", "ssh+git"}

// githubHosts are the hosts GitHub serves repositories from: no other host
// under github.com, as gist.github.com, holds one git pushes to as
// owner/name.
var githubHosts = []string{"github.com", "ssh.github.com"}

// syncRoots are the folders in the home macOS syncs off the Mac: cloud
// drives' (File Provider's, as Dropbox's) and iCloud Drive's.
var syncRoots = []string{"Library/CloudStorage", "Library/Mobile Documents"}

// tlsUndone are the variables in git's environment that undo TLS's proof of
// the host an https URL reaches: checking turned off, or certificate
// authorities of their own, git's and OpenSSL's.
var tlsUndone = []string{"GIT_SSL_NO_VERIFY", "GIT_SSL_CAINFO", "GIT_SSL_CAPATH", "SSL_CERT_FILE", "SSL_CERT_DIR"}

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

// ghSignedOut is what gh exits with when it isn't signed in to GitHub.
const ghSignedOut = 4

// address is a remote's URL as git reads it: its scheme, "file" for a path
// on this Mac and "ssh" for ssh's [user@]host:path; who it signs in as, its
// host and port, as written; and its path there, as git takes it.
type address struct {
	scheme, user, host, port, path string
}

// Place is where git takes the repository going way to the remote called
// remote, its URL url, as far as kit can prove it private (Place). Proof is
// positive: this Mac's, a local path or a file:// URL that stays on it
// (onThisMac); or a repository on GitHub (githubHosts), reached over https
// with TLS's proof of the host intact (overTLS), or over ssh to the host
// ssh's config resolves for what git gives ssh (sshHost), and gh asked its
// visibility. Anything else is unproven: a URL git hands to a helper, by
// its scheme, a :: or the remote's vcs setting; git's own protocol, http or
// ftp; what can take it elsewhere, set in git's config, ssh's or the
// environment git runs in; another host; or one kit can't read. An error is
// a failure to ask: git, ssh or gh.
func (r Repo) Place(ctx context.Context, remote, url string, way Way) (Place, error) {
	vcs, err := r.setting(ctx, "remote."+remote+".vcs")
	switch {
	case err != nil:
		return Place{}, err
	case vcs != "":
		return unproven("remote.%s.vcs hands it to a helper, which alone knows where it goes", remote)
	}
	a, why := read(url)
	switch {
	case why != "":
		return unproven("%s", why)
	case a.scheme == "file":
		return r.onThisMac(a.path)
	}
	env, ok := runner.Environ(ctx, r.Run, r.command())
	if !ok {
		return Place{}, errors.New("can't tell what environment git runs in")
	}
	switch {
	case slices.Contains(overSSH, a.scheme):
		if a.host, why, err = r.sshHost(ctx, remote, way, a, env); err != nil || why != "" {
			return Place{Unproven: why}, err
		}
	case a.scheme != "https":
		return unproven("git reaches it over %s://, and kit proves a repository on GitHub only over https or ssh", a.scheme)
	}
	repo, why := onGitHub(a.host, a.path)
	if why != "" {
		return unproven("%s", why)
	}
	if a.scheme == "https" {
		if why, err = r.overTLS(ctx, url, env); err != nil || why != "" {
			return Place{Unproven: why}, err
		}
	}
	return r.visibility(ctx, repo)
}

// unproven is a place kit can't prove private, for why.
func unproven(format string, args ...any) (Place, error) {
	return Place{Unproven: fmt.Sprintf(format, args...)}, nil
}

// read reads remote, a URL, as git does: a helper's, for :: or a scheme git
// doesn't reach itself; a URL with a scheme, by net/url; else, with a colon
// before any slash, ssh's [user@]host:path; else a path on this Mac. why
// is why kit can't tell where it goes: "" when it can. A URL with a query
// or a fragment it can't, nor an ssh URL with a password: git takes each
// into the path or the user it gives ssh, net/url apart.
func read(remote string) (a address, why string) {
	const unread = "kit can't read where it goes"
	if helper.MatchString(remote) {
		return address{}, "git hands it to a helper, which alone knows where it goes"
	}
	if m := scheme.FindStringSubmatch(remote); m != nil {
		if !slices.Contains(builtIn, m[1]) {
			return address{}, fmt.Sprintf("git hands %s:// URLs to a helper, which alone knows where they go", m[1])
		}
		u, err := url.Parse(remote)
		if err != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return address{}, unread
		}
		_, password := u.User.Password()
		switch {
		case m[1] == "file" && u.Path != "":
			return address{scheme: "file", path: u.Path}, ""
		case m[1] == "file" || u.Hostname() == "" || password && slices.Contains(overSSH, m[1]):
			return address{}, unread
		}
		return address{scheme: m[1], user: u.User.Username(), host: u.Hostname(), port: u.Port(), path: u.Path}, ""
	}
	colon, slash := strings.Index(remote, ":"), strings.Index(remote, "/")
	if colon < 0 || slash >= 0 && slash < colon {
		return address{scheme: "file", path: remote}, ""
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

// onThisMac is the place of path, a local path, the repository's folder's
// unless it's absolute: this Mac's when, links resolved, it stays on the
// Mac: outside the folders in the home macOS syncs off it (syncRoots), and
// on a filesystem that's local (MNT_LOCAL); else unproven, saying where it
// syncs or that it isn't local.
func (r Repo) onThisMac(path string) (Place, error) {
	if r.Home == "" {
		return Place{}, errors.New("kit doesn't know the home folder, to tell a path that syncs off this Mac")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.Dir, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return unproven("kit can't find it on this Mac")
	}
	if root, in := syncedIn(r.Home, resolved); root != "" {
		return unproven("it's in ~/%s, which syncs off this Mac", filepath.Join(root, in))
	}
	var fs unix.Statfs_t
	if err := r.statfsOf(resolved, &fs); err != nil {
		return Place{}, fmt.Errorf("can't tell what holds %s: %w", path, err)
	}
	if fs.Flags&unix.MNT_LOCAL == 0 {
		return unproven("it's on %s, which isn't local", unix.ByteSliceToString(fs.Fstypename[:]))
	}
	return Place{OnThisMac: true}, nil
}

// syncedIn is the folder of syncRoots in home that path is in, and the
// folder in it on the way to path, "." for the root itself: root is "" for
// none. Each folder is told by its file, so by any name a case-insensitive
// filesystem gives it.
func syncedIn(home, path string) (root, in string) {
	for _, root := range syncRoots {
		rootInfo, err := os.Stat(filepath.Join(home, root))
		if err != nil {
			continue
		}
		in := "."
		for p := path; ; p = filepath.Dir(p) {
			if info, err := os.Stat(p); err == nil && os.SameFile(info, rootInfo) {
				return root, in
			}
			if p == filepath.Dir(p) {
				break
			}
			in = filepath.Base(p)
		}
	}
	return "", ""
}

// statfsOf reads what the filesystem path is on says of itself into fs.
func (r Repo) statfsOf(path string, fs *unix.Statfs_t) error {
	if r.statfs != nil {
		return r.statfs(path, fs)
	}
	return unix.Statfs(path, fs)
}

// sshHost is the host ssh reaches for a, the URL of the remote called
// remote, git going way in env: as ssh -G resolves it given what git gives
// ssh (sshArgs), which a Match in ssh's config can tell apart, by the
// command for the other end among the rest. why is why kit can't tell
// where ssh takes it: an ssh command or variant set (sshCommand); a host
// not in plain ASCII, or like a flag; or what in ssh's answer can take it
// elsewhere: a proxy, a host name canonicalized, which looks it up anew, or
// a connection shared with other hosts (shared). ssh -G connects nowhere,
// but it runs the commands the config's Match exec lines name, as ssh does
// to read its config.
func (r Repo) sshHost(ctx context.Context, remote string, way Way, a address, env []string) (host, why string, err error) {
	if why, err := r.sshCommand(ctx, env); err != nil || why != "" {
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
	args, err := r.sshArgs(ctx, remote, way, a, dest)
	if err != nil {
		return "", "", err
	}
	said, err := r.askSSH(ctx, a.host, args...)
	if err != nil {
		return "", "", err
	}
	host = said["hostname"]
	switch {
	case host == "":
		return "", "", fmt.Errorf("can't tell which host ssh reaches for %s", a.host)
	case said["proxycommand"] != "" && said["proxycommand"] != "none":
		return "", "ssh's config takes it through a proxy (proxycommand), which can take it anywhere", nil
	case said["proxyjump"] != "" && said["proxyjump"] != "none":
		return "", "ssh's config takes it through a proxy (proxyjump), which can take it anywhere", nil
	case said["canonicalizehostname"] != "false":
		return "", "ssh's config canonicalizes its host name (canonicalizehostname), looking it up anew, which can take it elsewhere", nil
	}
	if path := said["controlpath"]; path != "" {
		why, err = r.shared(ctx, a.host, host, path, args)
	}
	return host, why, err
}

// sshArgs are what git gives ssh going way to a, the URL of the remote
// called remote, signed in as dest: the option to send git's protocol
// version (SendEnv), which git passes fetching at any version but 0 and
// pushing at version 1 alone, protocol.version 2 when it isn't set; a's
// port; dest; and the command for the other end: the remote's program for
// way, its first setting, as git takes it, then a's path, quoted as git
// quotes it (sqQuoted), from its second letter when that's a ~, as git
// has it.
func (r Repo) sshArgs(ctx context.Context, remote string, way Way, a address, dest string) ([]string, error) {
	setting, program := way.pack()
	set, err := r.firstSetting(ctx, "remote."+remote+"."+setting)
	if err != nil {
		return nil, err
	}
	if set != nil {
		program = *set
	}
	version, err := r.setting(ctx, "protocol.version")
	if err != nil {
		return nil, err
	}
	var args []string
	if way == Fetching && version != "0" || way == Pushing && version == "1" {
		args = append(args, "-o", "SendEnv=GIT_PROTOCOL")
	}
	if a.port != "" {
		args = append(args, "-p", a.port)
	}
	path := a.path
	if len(path) > 1 && path[1] == '~' {
		path = path[1:]
	}
	return append(args, dest, program+" "+sqQuoted(path)), nil
}

// sqQuoted is s as git quotes it for the shell ssh runs a command in: in
// single quotes, a quote and a ! each escaped outside them.
func sqQuoted(s string) string {
	return "'" + strings.NewReplacer(`'`, `'\''`, `!`, `'\!'`).Replace(s) + "'"
}

// firstSetting is git's setting key, its first value, as git takes a
// setting it uses the first of, as a remote's receivepack and uploadpack:
// nil when it isn't set.
func (r Repo) firstSetting(ctx context.Context, key string) (*string, error) {
	res, err := r.git(ctx, "config", "-z", "--get-all", key)
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	first, _, _ := strings.Cut(string(res.Stdout), "\x00")
	return &first, nil
}

// askSSH is ssh's config for host, as ssh -G says it given args: each
// setting's first value, by its name.
func (r Repo) askSSH(ctx context.Context, host string, args ...string) (map[string]string, error) {
	res, err := r.Run.Run(ctx, runner.Command{Name: "ssh", Args: append([]string{"-G"}, args...)})
	if err != nil {
		return nil, fmt.Errorf("can't tell which host ssh reaches for %s: %w", host, err)
	}
	said := map[string]string{}
	for line := range strings.Lines(string(res.Stdout)) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		if _, ok := said[key]; !ok {
			said[key] = value
		}
	}
	return said, nil
}

// shared is why kit can't tell where a connection ssh shares takes it, for
// asked, given args: a controlpath, path, naming neither host, the one ssh
// reaches, nor the connection, so a connection another host has open there
// can carry git's, as ssh uses one there whatever its controlmaster says.
// ssh -G says the path with its tokens expanded, so kit looks in it for
// host, and for the connection's hash (%C), asked of ssh.
func (r Repo) shared(ctx context.Context, asked, host, path string, args []string) (string, error) {
	if strings.Contains(path, host) {
		return "", nil
	}
	said, err := r.askSSH(ctx, asked, slices.Concat([]string{"-o", "ControlPath=%C"}, args)...)
	if err != nil {
		return "", err
	}
	if hash := said["controlpath"]; hash != "" && strings.Contains(path, hash) {
		return "", nil
	}
	return fmt.Sprintf("ssh's controlpath, %s, names no host, so a connection to another host there can carry it: name the host in it, as %%C does", path), nil
}

// sshCommand is why kit can't tell what git gives ssh, in env: an ssh
// command git runs in ssh's place, set in the environment (GIT_SSH_COMMAND,
// GIT_SSH) or git's config (core.sshCommand); or a variant of ssh other
// than ssh's own, the environment's (GIT_SSH_VARIANT) over the config's
// (ssh.variant), which git gives other options; "" with none.
func (r Repo) sshCommand(ctx context.Context, env []string) (string, error) {
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		if _, set := runner.LookupEnv(env, name); set {
			return name + " is set, so git may not reach it with ssh as its config says", nil
		}
	}
	command, err := r.setting(ctx, "core.sshCommand")
	switch {
	case err != nil:
		return "", err
	case command != "":
		return "core.sshCommand is set, so git may not reach it with ssh as its config says", nil
	}
	name := "GIT_SSH_VARIANT"
	variant, set := runner.LookupEnv(env, name)
	if !set {
		name = "ssh.variant"
		if variant, err = r.setting(ctx, name); err != nil {
			return "", err
		}
	}
	if variant != "" && variant != "auto" && variant != "ssh" {
		return fmt.Sprintf("%s is %s, so git gives ssh other options", name, variant), nil
	}
	return "", nil
}

// overTLS is why kit can't trust TLS's proof of the host the https URL u
// reaches, git running in env: a variable that undoes it (tlsUndone); or,
// as git's config has it for u, http.sslVerify false, certificate
// authorities of its own (http.sslCAInfo, http.sslCAPath), or addresses of
// its own (http.curloptResolve, a list an empty last value clears); "" with
// none.
func (r Repo) overTLS(ctx context.Context, u string, env []string) (string, error) {
	for _, name := range tlsUndone {
		if _, set := runner.LookupEnv(env, name); set {
			return name + " is set, so TLS doesn't prove the host git reaches", nil
		}
	}
	u = withoutPassword(u)
	verify, _, err := r.urlSetting(ctx, "http.sslVerify", u, "--type=bool")
	switch {
	case err != nil:
		return "", err
	case verify == "false":
		return "http.sslVerify is false for it, so TLS doesn't prove the host git reaches", nil
	}
	for _, key := range []string{"http.sslCAInfo", "http.sslCAPath"} {
		if _, set, err := r.urlSetting(ctx, key, u); err != nil || set {
			return key + " is set for it, so TLS doesn't prove the host git reaches", err
		}
	}
	resolve, _, err := r.urlSetting(ctx, "http.curloptResolve", u)
	if err != nil || resolve == "" {
		return "", err
	}
	return "http.curloptResolve sends it to an address of its own", nil
}

// withoutPassword is the URL u without the password in its sign-in, its
// user kept, as git matches a setting's URL by its user.
func withoutPassword(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	if _, has := parsed.User.Password(); !has {
		return u
	}
	parsed.User = url.User(parsed.User.Username())
	return parsed.String()
}

// urlSetting is git's setting key for the URL u, as git config
// --get-urlmatch reads it with args before it: set is false when it isn't.
func (r Repo) urlSetting(ctx context.Context, key, u string, args ...string) (value string, set bool, err error) {
	res, err := r.git(ctx, slices.Concat([]string{"config"}, args, []string{"--get-urlmatch", key, u})...)
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == 1 {
		return "", false, nil
	}
	return strings.TrimSuffix(string(res.Stdout), "\n"), err == nil, err
}

// onGitHub is the repository on GitHub at path on host, as owner/name, the
// host in any case, with a full name's last dot or without: GitHub's when
// host is one of githubHosts, in plain ASCII, and path an owner's
// repository; why, when it isn't.
func onGitHub(host, path string) (repo, why string) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	switch {
	case !plainHost.MatchString(host):
		return "", fmt.Sprintf("%+q isn't GitHub", host)
	case strings.HasSuffix(host, ".github.com") && !slices.Contains(githubHosts, host):
		return "", host + " holds none of GitHub's repositories, which are on github.com"
	case !slices.Contains(githubHosts, host):
		return "", host + " isn't GitHub"
	}
	parts := strings.Split(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
	if len(parts) != 2 || !githubName.MatchString(parts[0]) || !githubName.MatchString(parts[1]) {
		return "", "it isn't an owner's repository on GitHub"
	}
	return parts[0] + "/" + parts[1], ""
}

// visibility is the place of repo, on GitHub: its visibility, as gh says
// it, asked by its URL, so of github.com whatever host GH_HOST names.
func (r Repo) visibility(ctx context.Context, repo string) (Place, error) {
	res, err := r.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "view", "https://github.com/" + repo, "--json", "visibility", "--jq", ".visibility"}})
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == ghSignedOut {
		return Place{}, errors.New("gh isn't signed in to GitHub: run gh auth login")
	}
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
