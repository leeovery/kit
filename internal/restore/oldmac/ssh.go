package oldmac

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// connectTimeout is how long ssh waits for a Mac to answer, in seconds: on
// the same network, one that's there answers in well under it.
const connectTimeout = 5

// knownHostsFile is kit's own known-hosts file, in its state: the host keys
// of the Macs it's reached to restore from, each taken on first sight. The
// person's own is never read or written.
const knownHostsFile = "restore/known_hosts"

// KnownHosts is kit's own known-hosts file in its state, stateDir.
func KnownHosts(stateDir string) string {
	return filepath.Join(stateDir, filepath.FromSlash(knownHostsFile))
}

// sshOptions are ssh's options reaching a Mac at port, host keys in
// knownHosts: never a question asked, as there's no one to answer it; a
// host key taken on first sight, into kit's own file; and only ssh's
// errors said, so its notice of a host key added never stands in for
// rsync's reason.
func sshOptions(knownHosts string, port int) []string {
	opts := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeout),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + sshValue(knownHosts),
		"-o", "LogLevel=ERROR",
	}
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
// with the script, quoted for it.
func remote(opts []string, host, script string, timeout time.Duration) runner.Command {
	return runner.Command{Name: "ssh", Args: slices.Concat(opts, []string{host, "/bin/sh -c " + shellQuote(script)}), Timeout: timeout}
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

// sshFailed is err, from ssh's run, as what it means: errRefusedKey,
// errNoAnswer, or ssh's own words, else what the command there said.
func sshFailed(err error) error {
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok {
		return err
	}
	if exit.Code != sshExit {
		return fmt.Errorf("failed to answer, exit %d: %s", exit.Code, exit.Stderr)
	}
	lines := exit.Lines()
	switch {
	case slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "Permission denied") }):
		return errRefusedKey
	case slices.ContainsFunc(lines, func(l string) bool {
		return slices.ContainsFunc(unreachable, func(u string) bool { return strings.Contains(l, u) })
	}):
		return errNoAnswer
	}
	return fmt.Errorf("couldn't be reached over SSH: %s", exit.Stderr)
}

// The probe's answers, a line each, a key and its value.
const (
	keyUser      = "user"
	keyHome      = "home"
	keyKit       = "kit"
	keyDocuments = "documents"
	keyRsync     = "rsync"
)

// What the probe found of ~/Documents: read, SSH denied reading it (no full
// disk access), or there's none to read.
const (
	documentsRead   = "read"
	documentsDenied = "denied"
	documentsNone   = "none"
)

// probeScript asks a Mac, in one go: who SSH reaches it as, and their home
// folder; its kit name, as config.ReadMachine reads it, from kit's state
// where config.Locate puts it; whether SSH may read ~/Documents, which needs
// Remote Login's full disk access; and Homebrew's rsync's version. Each
// answer's a line, key=value, as the person's shell can print lines of its
// own.
var probeScript = strings.Join([]string{
	`d=${XDG_STATE_HOME:-}`,
	`case $d in /*) ;; *) d=$HOME/.local/state ;; esac`,
	`echo "` + keyUser + `=$(id -un)"`,
	`echo "` + keyHome + `=$HOME"`,
	`if [ -f "$d/kit/machine" ]; then echo "` + keyKit + `=$(head -n 1 "$d/kit/machine")"; fi`,
	`if [ ! -d "$HOME/Documents" ]; then echo ` + keyDocuments + `=` + documentsNone,
	`elif ls "$HOME/Documents" >/dev/null 2>&1; then echo ` + keyDocuments + `=` + documentsRead,
	`else echo ` + keyDocuments + `=` + documentsDenied + `; fi`,
	`r=` + shellQuote(RemoteRsync),
	`if [ -x "$r" ]; then "$r" --version 2>&1 | sed 's/^/` + keyRsync + `=/'; fi`,
}, "\n")

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
	res, err := f.Run.Run(ctx, remote(opts, host, probeScript, probeTimeout))
	if err != nil {
		return nil, sshFailed(err)
	}
	return readProbe(string(res.Stdout)), nil
}
