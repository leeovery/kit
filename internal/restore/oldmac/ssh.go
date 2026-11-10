package oldmac

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
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

// pinnedFile is kit's own known-hosts file, in its state, holding the one
// host key the old Mac must present, kit-config's, under the Mac's kit
// name. The person's own is never read or written.
const pinnedFile = "restore/pinned_host"

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

// PublicKey is key, a public key as a known-hosts line or a .pub file has
// it, as its type, then the key: the host patterns before them and a
// comment after left out.
func PublicKey(key string) (string, error) {
	fields := strings.Fields(key)
	// A known-hosts line's host patterns come before its type, where a .pub
	// file's key does: either's host can look like a type.
	if len(fields) >= 3 && isKeyType(fields[1]) {
		fields = fields[1:]
	}
	if len(fields) < 2 || !isKeyType(fields[0]) {
		return "", fmt.Errorf("%q isn't a public key, its type then the key, as ssh-ed25519 AAAA…", key)
	}
	if _, err := base64.StdEncoding.DecodeString(fields[1]); err != nil {
		return "", fmt.Errorf("%q isn't a public key: %w", key, err)
	}
	return fields[0] + " " + fields[1], nil
}

// pinLine is the known-hosts line holding key, as PublicKey has it, under
// alias.
func pinLine(alias, key string) (string, error) {
	public, err := PublicKey(key)
	if err != nil {
		return "", fmt.Errorf("%s's SSH host key in kit-config: %w", alias, err)
	}
	return alias + " " + public + "\n", nil
}

// isKeyType reports whether field is a type of key.
func isKeyType(field string) bool {
	return slices.ContainsFunc(keyTypes, func(t string) bool { return strings.HasPrefix(field, t) })
}

// sshOptions are ssh's options reaching a Mac at port: never a question
// asked, as there's no one to answer it; only ssh's errors said; and the
// Mac's host key the one pinned, in the file pinnedHosts, under alias (the
// old Mac's kit name), and nowhere else's (no other file, no command of the
// person's config, no DNS).
func sshOptions(pinnedHosts, alias string, port int) []string {
	opts := []string{
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(connectTimeout),
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + sshValue(pinnedHosts),
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "KnownHostsCommand=none",
		"-o", "VerifyHostKeyDNS=no",
		"-o", "HostKeyAlias=" + alias,
		"-o", "LogLevel=ERROR",
	}
	if port != 0 && port != 22 {
		opts = append(opts, "-p", strconv.Itoa(port))
	}
	return opts
}

// sshValue is v as ssh reads an option's value: one holding a space in
// double quotes, as ssh splits UserKnownHostsFile's at spaces into several
// files. v never holds what checkStateDir refuses.
func sshValue(v string) string {
	if !strings.ContainsAny(v, " \t") {
		return v
	}
	return `"` + v + `"`
}

// unsafeInPath are what a path in ssh's options can't hold: ssh expands %
// and ${…} in a known-hosts file's path, rsync can't split --rsh round a
// ', and " and \ would need quoting of their own.
const unsafeInPath = `%$'"\`

// checkStateDir says why kit's state folder, dir, can't hold the files
// ssh's options name: nil when it can.
func checkStateDir(dir string) error {
	if strings.ContainsAny(dir, unsafeInPath) {
		return fmt.Errorf("kit's state folder, %s, holds one of %s, which ssh's options can't name: copying from the old Mac needs it without them", dir, unsafeInPath)
	}
	return nil
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
// the one pinned.
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
	keyHostKey   = "hostkey"
)

// hostKeys are a Mac's SSH host keys' public halves, in SSHDir, as patterns,
// the one tried first first.
var hostKeys = []string{"ssh_host_ed25519_key.pub", "ssh_host_*_key.pub"}

// SSHDir is where macOS keeps its SSH host keys.
const SSHDir = "/etc/ssh"

// HostKey is this Mac's own SSH host key, as the probe says another's: the
// first of hostKeys in dir (SSHDir) anyone may read, as PublicKey has it;
// "" when there's none, as macOS makes them only once Remote Login first
// starts.
func HostKey(dir string) (string, error) {
	for _, pattern := range hostKeys {
		found, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return "", err
		}
		for _, path := range found {
			data, err := os.ReadFile(path)
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				continue
			}
			if err != nil {
				return "", fmt.Errorf("read this Mac's SSH host key: %w", err)
			}
			key, err := PublicKey(string(data))
			if err != nil {
				return "", fmt.Errorf("this Mac's SSH host key, in %s: %w", path, err)
			}
			return key, nil
		}
	}
	return "", nil
}

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
// protected folders there, which needs Remote Login's full disk access;
// the version of its rsync, the one at rsync; and its SSH host key, the
// first of hostKeys in keys, anyone may read, its type and key. Each
// answer's a line, key=value, as the person's shell can print lines of its
// own.
func probeScript(rsync, keys string) string {
	globs := make([]string, len(hostKeys))
	for i, k := range hostKeys {
		globs[i] = shellQuote(keys) + "/" + k
	}
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
		`for k in ` + strings.Join(globs, " ") + `; do`,
		`  if [ -r "$k" ]; then echo "` + keyHostKey + `=$(cut -d ' ' -f 1,2 "$k")"; break; fi`,
		`done`,
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
	res, err := f.Run.Run(ctx, remote(opts, host, probeScript(RemoteRsync, SSHDir), probeTimeout))
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
