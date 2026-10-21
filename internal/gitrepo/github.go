package gitrepo

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// githubName is an owner's or a repository's name on GitHub, never one gh
// would take for a flag.
var githubName = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)

// helper is a URL git hands to a remote helper, as in gcrypt::…, which
// alone knows where it goes.
var helper = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*::`)

// GitHubVisibility is the visibility on GitHub (private, internal or
// public) of the repository at remote, a URL as git takes it, asked through
// gh, and its owner/name. repo is "" for a remote that isn't on GitHub, so
// isn't public there: another host, or this Mac, a local path or a file://
// URL, as private as the Mac. Where remote is that can't be told, an error:
// never taken for not on GitHub.
func GitHubVisibility(ctx context.Context, run runner.Runner, remote string) (repo, visibility string, err error) {
	remote = strings.TrimSpace(remote)
	host, path, err := reaches(ctx, run, remote)
	if err != nil || !onGitHub(host) {
		return "", "", err
	}
	parts := strings.Split(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
	if len(parts) != 2 || !githubName.MatchString(parts[0]) || !githubName.MatchString(parts[1]) {
		return "", "", fmt.Errorf("can't tell which GitHub repository %s is", shown(remote))
	}
	repo = parts[0] + "/" + parts[1]
	res, err := run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "view", repo, "--json", "visibility", "--jq", ".visibility"}})
	if err != nil {
		return repo, "", fmt.Errorf("couldn't ask GitHub: %w", err)
	}
	return repo, strings.ToLower(strings.TrimSpace(string(res.Stdout))), nil
}

// onGitHub reports whether host is GitHub's: github.com, or a host of its,
// as www.github.com, ssh.github.com, its ssh endpoint on port 443, or
// gist.github.com, whose repositories aren't an owner's, so can't be asked
// after.
func onGitHub(host string) bool {
	return host == "github.com" || strings.HasSuffix(host, ".github.com")
}

// reaches is the host remote, a URL as git takes it, reaches, in lower
// case, and its path there: no host for this Mac. An ssh host is the one
// ssh's config names for it (sshHost), as for an alias.
func reaches(ctx context.Context, run runner.Runner, remote string) (host, path string, err error) {
	host, path, overSSH, err := parseRemote(remote)
	if err != nil || !overSSH {
		return host, path, err
	}
	host, err = sshHost(ctx, run, host)
	return host, path, err
}

// parseRemote reads remote as git does: a URL with a scheme, its host
// without its user or port; else, a colon before any slash, ssh's
// [user@]host:path; else a local path. overSSH is whether git reaches it
// over ssh. A host's in lower case, without the dot a full name may end
// in. An error where it can't tell the host: one for a remote helper, one
// in brackets, as git takes ssh's [user@host:port]:path, or one Go can't
// read, as when it's escaped.
func parseRemote(remote string) (host, path string, overSSH bool, err error) {
	unread := fmt.Errorf("can't tell where %s is", shown(remote))
	if helper.MatchString(remote) {
		return "", "", false, unread
	}
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return "", "", false, unread
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme == "file" {
			return "", u.Path, false, nil
		}
		host = strings.ToLower(u.Hostname())
		overSSH = scheme == "ssh" || scheme == "git+ssh" || scheme == "ssh+git"
		path = u.Path
	} else {
		colon, slash := strings.Index(remote, ":"), strings.Index(remote, "/")
		if colon < 0 || slash >= 0 && slash < colon {
			return "", remote, false, nil
		}
		host = remote[:colon]
		if strings.Contains(host, "[") {
			return "", "", false, unread
		}
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		host, path, overSSH = strings.ToLower(host), remote[colon+1:], true
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" || strings.HasPrefix(host, "-") {
		return "", "", false, unread
	}
	return host, path, overSSH, nil
}

// shown is remote as kit names it: without who a URL signs in as, which
// may carry a token.
func shown(remote string) string {
	scheme, rest, ok := strings.Cut(remote, "://")
	if !ok {
		return remote
	}
	authority := rest
	if slash := strings.Index(rest, "/"); slash >= 0 {
		authority = rest[:slash]
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	return scheme + "://" + rest
}

// sshHost is the host ssh reaches for host, as its config resolves it, an
// alias's HostName, in lower case: ssh -G reads the config, connecting
// nowhere.
func sshHost(ctx context.Context, run runner.Runner, host string) (string, error) {
	res, err := run.Run(ctx, runner.Command{Name: "ssh", Args: []string{"-G", host}})
	if err != nil {
		return "", fmt.Errorf("can't tell which host ssh reaches for %s: %w", host, err)
	}
	for line := range strings.Lines(string(res.Stdout)) {
		if name, ok := strings.CutPrefix(line, "hostname "); ok && strings.TrimSpace(name) != "" {
			return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), "."), nil
		}
	}
	return "", fmt.Errorf("can't tell which host ssh reaches for %s", host)
}
