package oldmac

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/leeovery/kit/internal/runner"
)

// connectTimeout is how long ssh waits for a Mac to answer, in seconds: on
// the same network, one that's there answers in well under it.
const connectTimeout = 5

// knownHostsFile is kit's own known-hosts file, in its state: the host keys
// of the Macs it's reached to restore from, each taken on first sight. The
// person's own is never read or written. pinnedFile holds the one host key
// the old Mac must present, when kit's given it, under the Mac's kit name.
const (
	knownHostsFile = "restore/known_hosts"
	pinnedFile     = "restore/pinned_host"
)

// KnownHosts is kit's own known-hosts file in its state, stateDir.
func KnownHosts(stateDir string) string {
	return filepath.Join(stateDir, filepath.FromSlash(knownHostsFile))
}

// pinned is the file, in kit's state, stateDir, holding the old Mac's
// pinned host key.
func pinned(stateDir string) string {
	return filepath.Join(stateDir, filepath.FromSlash(pinnedFile))
}

// plainName is a name ssh and a known-hosts file take as itself, never as
// an option or a pattern: letters, digits, - and ., not starting with -.
var plainName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)

// keyTypes start the types of key a known-hosts line can hold.
var keyTypes = []string{"ssh-", "ecdsa-", "sk-"}

// pinLine is the known-hosts line holding key, a public key as a
// known-hosts line or a .pub file has it (its type, then the key, the host
// patterns before them and a comment after left out), under alias.
func pinLine(alias, key string) (string, error) {
	fields := strings.Fields(key)
	if len(fields) >= 3 && !isKeyType(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) < 2 || !isKeyType(fields[0]) {
		return "", fmt.Errorf("the old Mac's host key, %q, isn't a public key, its type then the key, as ssh-ed25519 AAAA…", key)
	}
	if _, err := base64.StdEncoding.DecodeString(fields[1]); err != nil {
		return "", fmt.Errorf("the old Mac's host key, %q, isn't a public key: %w", key, err)
	}
	return alias + " " + fields[0] + " " + fields[1] + "\n", nil
}

// isKeyType reports whether field is a type of key.
func isKeyType(field string) bool {
	return slices.ContainsFunc(keyTypes, func(t string) bool { return strings.HasPrefix(field, t) })
}

// sshOptions are ssh's options reaching a Mac at port: never a question
// asked, as there's no one to answer it; only ssh's errors said, so its
// notice of a host key added never stands in for rsync's reason; and the
// Mac's host key either the one pinned, in the file pinnedHosts, under
// alias (the old Mac's kit name), and no other file's; or, with no
// pinnedHosts, taken on first sight into kit's own file, knownHosts.
func sshOptions(knownHosts, pinnedHosts, alias string, port int) []string {
	opts := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(connectTimeout)}
	if pinnedHosts != "" {
		opts = append(opts,
			"-o", "StrictHostKeyChecking=yes",
			"-o", "UserKnownHostsFile="+sshValue(pinnedHosts),
			"-o", "GlobalKnownHostsFile=/dev/null",
			"-o", "HostKeyAlias="+alias)
	} else {
		opts = append(opts,
			"-o", "StrictHostKeyChecking=accept-new",
			"-o", "UserKnownHostsFile="+sshValue(knownHosts))
	}
	opts = append(opts, "-o", "LogLevel=ERROR")
	if port != 0 && port != 22 {
		opts = append(opts, "-p", strconv.Itoa(port))
	}
	return opts
}

// sshValue is v as ssh reads an option's value: one holding a space in
// double quotes, as ssh splits UserKnownHostsFile's at spaces into several
// files.
func sshValue(v string) string {
	if !strings.ContainsAny(v, " \t") {
		return v
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// remote is ssh running script, a /bin/sh script, on host through opts:
// ssh hands its command to the person's own shell there, which runs sh
// with the script, quoted for it. -- ends ssh's options, so a host, which
// a device on the network names, is never taken as one.
func remote(opts []string, host, script string, timeout time.Duration) runner.Command {
	return runner.Command{Name: "ssh", Args: slices.Concat(opts, []string{"--", host, "/bin/sh -c " + shellQuote(script)}), Timeout: timeout}
}

// shellQuote is s as a shell takes it, whole, as itself.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sshExit is ssh's own exit for a failure of its own, never the command's.
const sshExit = 255

// unreachable are ssh's words for a Mac that isn't there to answer.
var unreachable = []string{"Connection refused", "Operation timed out", "Connection timed out", "No route to host", "Host is down", "Could not resolve hostname", "Network is unreachable"}

// errRefusedKey is a Mac answering but refusing this Mac's key.
var errRefusedKey = errors.New("refused this Mac's key, which the old Mac's ~/.ssh/authorized_keys must hold")

// errNoAnswer is a Mac not answering SSH.
var errNoAnswer = errors.New("didn't answer")

// hostKeyError is a Mac presenting a host key ssh won't trust: another than
// the one kit took on first sight, or than the one pinned.
type hostKeyError struct{ said string }

func (e *hostKeyError) Error() string { return "couldn't be reached over SSH: " + e.said }

// sshFailed is err, from ssh's run, as what it means: errRefusedKey,
// errNoAnswer, or ssh's own words, else what the command there said.
func sshFailed(err error) error {
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok {
		return err
	}
	if exit.Code != sshExit {
		return fmt.Errorf("failed to answer, exit %d: %s", exit.Code, printable(exit.Stderr))
	}
	lines := exit.Lines()
	switch {
	case slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "Permission denied") }):
		return errRefusedKey
	case slices.ContainsFunc(lines, func(l string) bool {
		return slices.ContainsFunc(unreachable, func(u string) bool { return strings.Contains(l, u) })
	}):
		return errNoAnswer
	case slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "Host key verification failed") }):
		return &hostKeyError{said: printable(exit.Stderr)}
	}
	return fmt.Errorf("couldn't be reached over SSH: %s", printable(exit.Stderr))
}

// The probe's answers, a line each, a key and its value.
const (
	keyUser      = "user"
	keyHome      = "home"
	keyKit       = "kit"
	keyProtected = "protected"
	keyRsync     = "rsync"
)

// protected are the folders in the home folder SSH may read only with
// Remote Login's full disk access, the first there the one tried.
var protected = []string{"Documents", "Desktop", "Downloads"}

// Whether SSH read the protected folder tried, as the probe says it before
// the folder's name: read, or denied (no full disk access); or none, when
// there's none of them to try.
const (
	protectedRead   = "read"
	protectedDenied = "denied"
	protectedNone   = "none"
)

// probeScript asks a Mac, in one go: who SSH reaches it as, and their home
// folder; its kit name, as config.ReadMachine reads it, from kit's state
// where config.Locate puts it; whether SSH may read the first of the
// protected folders there, which needs Remote Login's full disk access; and
// the version of its rsync, the one at rsync. Each answer's a line,
// key=value, as the person's shell can print lines of its own.
func probeScript(rsync string) string {
	return strings.Join([]string{
		`d=${XDG_STATE_HOME:-}`,
		`case $d in /*) ;; *) d=$HOME/.local/state ;; esac`,
		`echo "` + keyUser + `=$(id -un)"`,
		`echo "` + keyHome + `=$HOME"`,
		`if [ -f "$d/kit/machine" ]; then echo "` + keyKit + `=$(head -n 1 "$d/kit/machine")"; fi`,
		`p=` + protectedNone,
		`for f in ` + strings.Join(protected, " ") + `; do`,
		`  if [ -d "$HOME/$f" ]; then`,
		`    if ls "$HOME/$f" >/dev/null 2>&1; then p="` + protectedRead + ` $f"; else p="` + protectedDenied + ` $f"; fi`,
		`    break`,
		`  fi`,
		`done`,
		`echo "` + keyProtected + `=$p"`,
		`r=` + shellQuote(rsync),
		`if [ -x "$r" ]; then "$r" --version 2>&1 | sed 's/^/` + keyRsync + `=/'; fi`,
	}, "\n")
}

// probed is what the probe found: each key's values, in order.
type probed map[string][]string

// readProbe reads the probe's answers from what it printed.
func readProbe(out string) probed {
	p := probed{}
	for line := range strings.Lines(out) {
		key, value, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "=")
		if ok {
			p[key] = append(p[key], value)
		}
	}
	return p
}

// one is key's value, its first: "" for none.
func (p probed) one(key string) string {
	if v := p[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// probe asks the Mac at host, through opts, what probeScript does.
func (f Finder) probe(ctx context.Context, opts []string, host string) (probed, error) {
	res, err := f.Run.Run(ctx, remote(opts, host, probeScript(RemoteRsync), probeTimeout))
	if err != nil {
		return nil, sshFailed(err)
	}
	return readProbe(string(res.Stdout)), nil
}

// printable is s, said by a Mac on the network, without control characters,
// so nothing it says can reach the terminal as an escape.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
