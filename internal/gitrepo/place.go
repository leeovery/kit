package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// syncRoot is a folder in the home whose files may leave the Mac, and what
// it does with them.
type syncRoot struct{ path, does string }

// syncRoots are the folders in the home whose files may leave the Mac:
// cloud drives' (File Provider's, as Dropbox's) and iCloud Drive's, which
// sync off it, and the Desktop and Documents, which iCloud syncs in place
// when its Desktop & Documents is on. The list is partial: a program such
// as Syncthing syncs any folder, which kit can't tell.
var syncRoots = []syncRoot{
	{"Library/CloudStorage", "syncs off this Mac"},
	{"Library/Mobile Documents", "syncs off this Mac"},
	{"Desktop", "may sync to iCloud"},
	{"Documents", "may sync to iCloud"},
}

// repoSuffixes are what git adds to a local path, in turn, to find the
// repository at the other end of a push or fetch (enter_repo): the first
// that's a file, or a folder that's a repository (isGitDir), is the one.
var repoSuffixes = []string{"/.git", "", ".git/.git", ".git"}

// noRepo is why a local path holds no repository git takes.
const noRepo = "git finds no repository there"

// objectHash is an object's hash at the start of a HEAD, as git reads one
// (its first 40 hex digits, a SHA-1's, or a SHA-256's start).
var objectHash = regexp.MustCompile(`^[0-9a-fA-F]{40}`)

// otherVariants are the variants of ssh git knows besides ssh's own and
// simple, each given other options; a value it doesn't know, in its case,
// it takes for ssh's own.
var otherVariants = []string{"plink", "putty", "tortoiseplink"}

// sentinel is a host no config means and no lookup finds (.invalid), asked
// of ssh -G to tell a controlpath that's the same for other hosts.
const sentinel = "kit-sentinel.invalid"

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
	if why != "" {
		return unproven("%s", why)
	}
	env, ok := runner.Environ(ctx, r.Run, r.command())
	if !ok {
		return Place{}, errors.New("can't tell what environment git runs in")
	}
	switch {
	case a.scheme == "file":
		return r.onThisMac(ctx, remote, way, a.path, env)
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
// unless it's absolute, git going way to the remote called remote in env:
// this Mac's when git runs its own program at the other end, not one the
// remote sets (pack), and every place git writes the repository it finds
// there (localRepo), links followed (followed), stays on the Mac (leaves);
// else unproven, saying why.
func (r Repo) onThisMac(ctx context.Context, remote string, way Way, path string, env []string) (Place, error) {
	if r.Home == "" {
		return Place{}, errors.New("kit doesn't know the home folder, to tell a path that syncs off this Mac")
	}
	setting, program := way.pack()
	set, err := r.firstSetting(ctx, "remote."+remote+"."+setting)
	switch {
	case err != nil:
		return Place{}, err
	case set != nil && *set != program:
		return unproven("remote.%s.%s is set, so git runs a program of its own there, which can take it anywhere", remote, setting)
	}
	roots, why := localRepo(path, r.Dir, env)
	if why != "" {
		return unproven("%s", why)
	}
	written, err := followed(roots)
	if err != nil {
		return Place{}, fmt.Errorf("can't tell where git writes %s: %w", path, err)
	}
	for _, p := range written {
		if why, err := r.leaves(p); err != nil || why != "" {
			return Place{Unproven: why}, err
		}
	}
	return Place{OnThisMac: true}, nil
}

// localRepo is where git finds the repository at path, a local path, dir's
// unless it's absolute, git running in env, as git does for the other end
// of a push or fetch: trailing slashes dropped, a leading ~ the home in
// git's environment (homeIn), each of repoSuffixes added in turn, a gitfile
// followed to the git folder it names (gitfile); roots are what it found,
// the git folder and its common folder (commonDir), a worktree's elsewhere,
// which is where git writes. Nothing's resolved by name, as .. after a link
// leads where the link does. why is why it finds none: "" when it does.
func localRepo(path, dir string, env []string) (roots []string, why string) {
	for len(path) > 1 && strings.HasSuffix(path, "/") {
		path = path[:len(path)-1]
	}
	if path, why = homeIn(path, env); why != "" {
		return nil, why
	}
	if !strings.HasPrefix(path, "/") {
		path = dir + "/" + path
	}
	for _, suffix := range repoSuffixes {
		found := path + suffix
		info, err := os.Stat(found)
		switch {
		case err != nil:
			continue
		case info.Mode().IsRegular():
			gitDir, ok := gitfile(found)
			if !ok {
				return nil, noRepo
			}
			return []string{found, gitDir, commonDir(gitDir)}, ""
		case info.IsDir() && isGitDir(found):
			return []string{found, commonDir(found)}, ""
		}
	}
	return nil, noRepo
}

// homeIn is path with a leading ~ the home folder, as git takes it at the
// other end: HOME in git's environment, env; why is why kit can't tell
// which folder it is: another user's home (~name), or no HOME.
func homeIn(path string, env []string) (expanded, why string) {
	rest, ok := strings.CutPrefix(path, "~")
	if !ok {
		return path, ""
	}
	if user, _, _ := strings.Cut(rest, "/"); user != "" {
		return "", "it's in ~" + user + ", another user's home folder, which kit doesn't look up"
	}
	home, set := runner.LookupEnv(env, "HOME")
	if !set {
		return "", "HOME isn't set where git runs, so " + noRepo
	}
	return home + rest, ""
}

// gitfile is the git folder the gitfile at path names, as git reads one
// (read_gitfile): "gitdir: " then the folder, from path's own unless it's
// absolute; ok is false unless it's a repository's, as git then stops.
func gitfile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	named, ok := strings.CutPrefix(strings.TrimRight(string(data), "\r\n"), "gitdir: ")
	if !ok || named == "" {
		return "", false
	}
	if !strings.HasPrefix(named, "/") {
		named = path[:strings.LastIndex(path, "/")+1] + named
	}
	return named, isGitDir(named)
}

// isGitDir reports whether dir is a repository's git folder, as git tells
// one (is_git_directory): a HEAD it can read (validHead), and objects and
// refs folders it can search in its common folder (commonDir).
func isGitDir(dir string) bool {
	if !validHead(dir + "/HEAD") {
		return false
	}
	common := commonDir(dir)
	return common != "" && searchable(common+"/objects") && searchable(common+"/refs")
}

// commonDir is the common folder of the git folder dir, as git reads it:
// the folder its commondir file names, from dir unless it's absolute, as a
// worktree's does; else dir; "" for a commondir git can't read.
func commonDir(dir string) string {
	data, err := os.ReadFile(dir + "/commondir")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return dir
	case err != nil:
		return ""
	}
	named := strings.TrimRight(string(data), "\r\n")
	switch {
	case named == "":
		return ""
	case strings.HasPrefix(named, "/"):
		return named
	}
	return dir + "/" + named
}

// validHead reports whether the file at path is a HEAD git can read
// (validate_headref): a link to a path in refs/, or, in its first 255
// bytes, "ref:" then, after any spaces, a path in refs/, or an object's
// hash.
func validHead(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		to, err := os.Readlink(path)
		return err == nil && strings.HasPrefix(to, "refs/")
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 255))
	if err != nil {
		return false
	}
	if ref, ok := strings.CutPrefix(string(data), "ref:"); ok && strings.HasPrefix(strings.TrimLeft(ref, " \t\n\v\f\r"), "refs/") {
		return true
	}
	return objectHash.Match(data)
}

// searchable reports whether the folder at path is one git can search, as
// it checks (access's X_OK).
func searchable(path string) bool {
	return unix.Access(path, unix.X_OK) == nil
}

// followed is every place git writes in roots, links resolved: each root,
// and every link in it, and in the folders those lead to, as git follows a
// link writing into it. A link that leads nowhere it skips, as git writes
// nothing through one.
func followed(roots []string) ([]string, error) {
	var all []string
	seen := map[string]bool{}
	for len(roots) > 0 {
		root := roots[0]
		roots = roots[1:]
		resolved, err := filepath.EvalSymlinks(root)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, err
		case seen[resolved]:
			continue
		}
		seen[resolved] = true
		all = append(all, resolved)
		err = filepath.WalkDir(resolved, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type()&fs.ModeSymlink != 0 {
				roots = append(roots, p)
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	return all, nil
}

// leaves is why what git writes at p, resolved, may leave this Mac: it's in
// one of the home's syncRoots, or on a filesystem that isn't local
// (MNT_LOCAL); "" when it stays.
func (r Repo) leaves(p string) (string, error) {
	if root, in := syncedIn(r.Home, p); root.path != "" {
		return fmt.Sprintf("git writes it in ~/%s, which %s", filepath.Join(root.path, in), root.does), nil
	}
	var st unix.Statfs_t
	if err := r.statfsOf(p, &st); err != nil {
		return "", fmt.Errorf("can't tell what holds %s: %w", p, err)
	}
	if st.Flags&unix.MNT_LOCAL == 0 {
		return fmt.Sprintf("git writes it on %s, which isn't local", unix.ByteSliceToString(st.Fstypename[:])), nil
	}
	return "", nil
}

// syncedIn is the folder of syncRoots in home that path is in, and the
// folder in it on the way to path, "." for the root itself: root's path is
// "" for none. Each folder is told by its file, so by any name a
// case-insensitive filesystem gives it.
func syncedIn(home, path string) (root syncRoot, in string) {
	for _, root := range syncRoots {
		rootInfo, err := os.Stat(filepath.Join(home, root.path))
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
	return syncRoot{}, ""
}

// statfsOf reads what the filesystem path is on says of itself into st.
func (r Repo) statfsOf(path string, st *unix.Statfs_t) error {
	if r.statfs != nil {
		return r.statfs(path, st)
	}
	return unix.Statfs(path, st)
}

// sshHost is the host ssh reaches for a, the URL of the remote called
// remote, git going way in env: as ssh -G resolves it given what git gives
// ssh (toSSH), which a Match in ssh's config can tell apart, by the
// command for the other end among the rest. why is why kit can't tell
// where ssh takes it: an ssh command set (sshCommand) or a variant kit
// can't follow (sshVariant); a host not in plain ASCII, or like a flag;
// what in ssh's answer can take it elsewhere (elsewhere); ssh's proof of
// the host undone (hostKey); or a connection shared with other hosts
// (shared). ssh -G connects nowhere, but it runs the commands the config's
// Match exec lines name, as ssh does to read its config.
func (r Repo) sshHost(ctx context.Context, remote string, way Way, a address, env []string) (host, why string, err error) {
	if why, err := r.sshCommand(ctx, env); err != nil || why != "" {
		return "", why, err
	}
	simple, why, err := r.sshVariant(ctx, env, a.port)
	if err != nil || why != "" {
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
	call, err := r.toSSH(ctx, remote, way, a, dest, simple)
	if err != nil {
		return "", "", err
	}
	said, err := r.askSSH(ctx, a.host, call.args()...)
	if err != nil {
		return "", "", err
	}
	if host = said["hostname"]; host == "" {
		return "", "", fmt.Errorf("can't tell which host ssh reaches for %s", a.host)
	}
	if why := elsewhere(said); why != "" {
		return "", why, nil
	}
	if why, err := r.hostKey(ctx, said); err != nil || why != "" {
		return "", why, err
	}
	if said["controlpath"] != "" {
		why, err = r.shared(ctx, a.host, call, said)
	}
	return host, why, err
}

// sshCall is what git gives ssh: the option to send git's protocol version
// (sendEnv), a port, "" for none, who it signs in as and where (dest), and
// the command for the other end.
type sshCall struct {
	sendEnv             bool
	port, dest, command string
}

// args are the call's arguments, in git's order.
func (c sshCall) args() []string {
	var args []string
	if c.sendEnv {
		args = append(args, "-o", "SendEnv=GIT_PROTOCOL")
	}
	if c.port != "" {
		args = append(args, "-p", c.port)
	}
	return append(args, c.dest, c.command)
}

// toSSH is what git gives ssh going way to a, the URL of the remote
// called remote, signed in as dest, ssh's own variant or simple: the
// option to send git's protocol version (SendEnv), which git passes
// fetching at any version but 0 and pushing at version 1 alone,
// protocol.version 2 when it isn't set, never simple; a's port; dest; and
// the command for the other end: the remote's program for way, its first
// setting, as git takes it, then a's path, quoted as git quotes it
// (sqQuoted), from its second letter when that's a ~, as git has it.
func (r Repo) toSSH(ctx context.Context, remote string, way Way, a address, dest string, simple bool) (sshCall, error) {
	setting, program := way.pack()
	set, err := r.firstSetting(ctx, "remote."+remote+"."+setting)
	if err != nil {
		return sshCall{}, err
	}
	if set != nil {
		program = *set
	}
	version, err := r.setting(ctx, "protocol.version")
	if err != nil {
		return sshCall{}, err
	}
	path := a.path
	if len(path) > 1 && path[1] == '~' {
		path = path[1:]
	}
	return sshCall{
		sendEnv: !simple && (way == Fetching && version != "0" || way == Pushing && version == "1"),
		port:    a.port,
		dest:    dest,
		command: program + " " + sqQuoted(path),
	}, nil
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

// elsewhere is why ssh's answer, said, can take the connection elsewhere:
// a proxy, or a host name canonicalized, which looks it up anew; "" for
// neither.
func elsewhere(said map[string]string) string {
	switch {
	case said["proxycommand"] != "" && said["proxycommand"] != "none":
		return "ssh's config takes it through a proxy (proxycommand), which can take it anywhere"
	case said["proxyjump"] != "" && said["proxyjump"] != "none":
		return "ssh's config takes it through a proxy (proxyjump), which can take it anywhere"
	case said["canonicalizehostname"] != "false":
		return "ssh's config canonicalizes its host name (canonicalizehostname), looking it up anew, which can take it elsewhere"
	}
	return ""
}

// hostKey is why kit can't trust ssh's proof of the host it reaches, as
// said, ssh -G's answer, has it: the host's key not checked
// (stricthostkeychecking no), checked by another name (hostkeyalias) or
// against keys a command gives (knownhostscommand), or no file of known
// hosts (userknownhostsfile /dev/null or none); or a key taken on first
// sight (stricthostkeychecking accept-new) for a host whose key ssh doesn't
// know yet (keyKnown); "" with none.
func (r Repo) hostKey(ctx context.Context, said map[string]string) (string, error) {
	files := strings.Fields(said["userknownhostsfile"])
	switch {
	case said["stricthostkeychecking"] == "false":
		return "ssh's config doesn't check the host's key (stricthostkeychecking), so another host can pass for it", nil
	case said["hostkeyalias"] != "":
		return "ssh's config checks the host's key by another name (hostkeyalias), so another host can pass for it", nil
	case said["knownhostscommand"] != "":
		return "ssh's config takes the keys it knows from a command (knownhostscommand), which can give any", nil
	case slices.Contains(files, "/dev/null") || slices.Contains(files, "none"):
		return "ssh's config keeps no file of hosts' keys (userknownhostsfile), so another host can pass for it", nil
	case said["stricthostkeychecking"] != "accept-new":
		return "", nil
	}
	known, err := r.keyKnown(ctx, said)
	if err != nil || known {
		return "", err
	}
	return "ssh's config takes a host's key on first sight (stricthostkeychecking accept-new), and it knows none for " + said["hostname"] + " yet, so another host can pass for it", nil
}

// keyKnown reports whether ssh knows a key of the host said, ssh -G's
// answer, names, on its port, as ssh names a host on another port than
// 22 ([host]:port): in any of its files of known hosts, the user's and the
// system's, as ssh-keygen finds it. A file that isn't there, or doesn't
// hold the host, is no.
func (r Repo) keyKnown(ctx context.Context, said map[string]string) (bool, error) {
	name := said["hostname"]
	if port := said["port"]; port != "22" {
		name = "[" + name + "]:" + port
	}
	for _, file := range slices.Concat(strings.Fields(said["userknownhostsfile"]), strings.Fields(said["globalknownhostsfile"])) {
		if file == "none" {
			continue
		}
		_, err := r.Run.Run(ctx, runner.Command{Name: "ssh-keygen", Args: []string{"-F", name, "-f", file}})
		if _, no := errors.AsType[*runner.ExitError](err); no {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("can't tell whether ssh knows %s's key: %w", name, err)
		}
		return true, nil
	}
	return false, nil
}

// shared is why kit can't tell where a connection ssh shares takes it, for
// asked, given call, what git gives ssh, and said, ssh's answer: a
// controlpath the same for the sentinel, a host no config means, asked as
// call, signed in as the user ssh resolved, on the port it resolved, so
// the same for other hosts, as a connection another host has open there
// can carry git's, whatever controlmaster says. A path shared on purpose
// between hosts the config names, or an alias for the host whose proxy
// opens the connection there, kit can't see.
func (r Repo) shared(ctx context.Context, asked string, call sshCall, said map[string]string) (string, error) {
	call.port, call.dest = said["port"], said["user"]+"@"+sentinel
	other, err := r.askSSH(ctx, asked, call.args()...)
	if err != nil || other["controlpath"] != said["controlpath"] {
		return "", err
	}
	return fmt.Sprintf("ssh's controlpath, %s, is the same for other hosts, so a connection to another host there can carry git's: name the host in it, as %%C does", said["controlpath"]), nil
}

// sshCommand is why kit can't tell what git gives ssh, in env: an ssh
// command git runs in ssh's place, set in the environment (GIT_SSH_COMMAND,
// GIT_SSH) or git's config (core.sshCommand); "" with none.
func (r Repo) sshCommand(ctx context.Context, env []string) (string, error) {
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		if _, set := runner.LookupEnv(env, name); set {
			return name + " is set, so git may not reach it with ssh as its config says", nil
		}
	}
	command, err := r.setting(ctx, "core.sshCommand")
	if err != nil || command == "" {
		return "", err
	}
	return "core.sshCommand is set, so git may not reach it with ssh as its config says", nil
}

// sshVariant is whether the variant of ssh git gives its options as, in
// env, the environment's (GIT_SSH_VARIANT) over the config's (ssh.variant),
// is simple, which git gives the host and command alone; any other is
// ssh's own, as git takes a value it doesn't know. why is why kit can't
// follow it: a variant given other options (otherVariants), or simple with
// a port, which git refuses.
func (r Repo) sshVariant(ctx context.Context, env []string, port string) (simple bool, why string, err error) {
	name := "GIT_SSH_VARIANT"
	variant, set := runner.LookupEnv(env, name)
	if !set {
		name = "ssh.variant"
		if variant, err = r.setting(ctx, name); err != nil {
			return false, "", err
		}
	}
	switch {
	case slices.Contains(otherVariants, variant):
		return false, fmt.Sprintf("%s is %s, so git gives ssh other options", name, variant), nil
	case variant == "simple" && port != "":
		return false, name + " is simple, with which git reaches no port", nil
	}
	return variant == "simple", "", nil
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

// Remembering is r asking gh each repository's visibility once, as a check
// asking of one repository by several URLs, or both ways, does.
func (r Repo) Remembering() Repo {
	r.seen = map[string]string{}
	return r
}

// visibility is the place of repo, on GitHub: its visibility, as gh says
// it, asked by its URL, so of github.com whatever host GH_HOST names; once
// a repository, when r is remembering.
func (r Repo) visibility(ctx context.Context, repo string) (Place, error) {
	if said, ok := r.seen[repo]; ok {
		return Place{Repo: repo, Visibility: said}, nil
	}
	res, err := r.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "view", "https://github.com/" + repo, "--json", "visibility", "--jq", ".visibility"}})
	if exit, ok := errors.AsType[*runner.ExitError](err); ok && exit.Code == ghSignedOut {
		return Place{}, errors.New("gh isn't signed in to GitHub: run gh auth login")
	}
	if err != nil {
		return Place{}, fmt.Errorf("couldn't ask GitHub: %w", err)
	}
	said := strings.ToLower(strings.TrimSpace(string(res.Stdout)))
	if r.seen != nil {
		r.seen[repo] = said
	}
	return Place{Repo: repo, Visibility: said}, nil
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
