// Package secret is secrets as a kind: each declared in a secrets section,
// what it fills (an environment variable, in ~/.secrets.zsh, which the shell
// sources; or a file, by its path) then where 1Password keeps its value: a
// field or attachment of the item the section's header names, as in
// [secrets op://vault/item], or in full in a plain [secrets]. An option:
// --mode for a file's (600 unless it says).
//
// Checking never asks 1Password, which would ask for a fingerprint every
// hour: it reads only what's on the Mac. Syncing reads every value from
// 1Password in one sitting; a value that fails to read keeps its previous
// one. Values never pass through kit's events or log.
package secret

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// EnvFile is the file of environment variables kit writes, under the home
// folder, which the shell sources.
const EnvFile = ".secrets.zsh"

// opTimeout is how long 1Password may take to answer, or to read a value:
// it has hung after a restart and over remote connections.
const opTimeout = 15 * time.Second

// SignIn says how to have 1Password answer.
const SignIn = "1Password doesn't answer: unlock its app, with Settings › Developer › Integrate with 1Password CLI on; over a remote connection, sign in with a password in the shell kit runs from: eval $(OP_BIOMETRIC_UNLOCK_ENABLED=false op signin)"

// Secret is a declared secret.
type Secret struct {
	// Name is what it fills: an environment variable's name, or a file's
	// path as declared, ~ and all.
	Name string
	// Ref is where 1Password keeps its value, in full.
	Ref string
	// Mode is a file's permissions.
	Mode os.FileMode
	Note string
}

// IsFile reports whether the secret is a file's.
func (s Secret) IsFile() bool {
	return strings.HasPrefix(s.Name, "~/") || strings.HasPrefix(s.Name, "/")
}

// Secrets is the secrets, driven through op.
type Secrets struct {
	run      runner.Runner
	home     string
	declared []Secret
}

// New returns the secrets of the user whose home is home.
func New(run runner.Runner, home string) *Secrets {
	return &Secrets{run: run, home: home}
}

func (s *Secrets) Name() string    { return "secret" }
func (s *Secrets) Title() string   { return "Secrets" }
func (s *Secrets) Program() string { return "op" }

// Verb says a secret on the Mac as declared is synced.
func (s *Secrets) Verb() string { return "synced" }

// Values reads each secret's line: its reference, short in an item's
// section or full in the plain one, and its options.
func (s *Secrets) Values(list config.List) (config.List, error) {
	s.declared = nil
	for _, e := range list.Entries {
		sec, err := s.parse(e)
		if err != nil {
			return list, fmt.Errorf("%s: %s: %w", e.Pos(), e.Name, err)
		}
		s.declared = append(s.declared, sec)
	}
	return list, nil
}

func (s *Secrets) parse(e config.Entry) (Secret, error) {
	sec := Secret{Name: e.Name, Mode: 0o600, Note: e.Note}
	words, err := config.Words(e.Value)
	if err != nil {
		return sec, err
	}
	if len(words) == 0 {
		return sec, errors.New("where 1Password keeps it comes after the name")
	}
	switch ref := words[0]; {
	case e.Item != "" && strings.HasPrefix(ref, "op://"):
		return sec, fmt.Errorf("%s is in full: in [%s] a line names a field or attachment of its item, as in GitHub/token; a full reference goes in a plain [secrets]", ref, e.Section)
	case e.Item != "":
		sec.Ref = e.Item + "/" + ref
	case !strings.HasPrefix(ref, "op://"):
		return sec, fmt.Errorf("%q is short for a field of an item: in a plain [secrets], give where 1Password keeps it in full (op://vault/item/field), or put it in its item's section, as in [secrets op://vault/item]", ref)
	default:
		sec.Ref = ref
	}
	for i := 1; i < len(words); i++ {
		if i+1 >= len(words) {
			return sec, fmt.Errorf("%s needs its value", words[i])
		}
		if words[i] != "--mode" {
			return sec, fmt.Errorf("%s isn't an option: --mode is the one", words[i])
		}
		m, err := strconv.ParseUint(words[i+1], 8, 32)
		if err != nil || m > 0o777 {
			return sec, fmt.Errorf("--mode %s isn't a file's permissions, as in 600", words[i+1])
		}
		sec.Mode = os.FileMode(m)
		i++
	}
	if sec.Mode != 0o600 && !sec.IsFile() {
		return sec, errors.New("--mode is a file's")
	}
	return sec, nil
}

// path is a file secret's full path.
func (s *Secrets) path(sec Secret) string {
	if rest, ok := strings.CutPrefix(sec.Name, "~/"); ok {
		return filepath.Join(s.home, rest)
	}
	return sec.Name
}

// envLines are ~/.secrets.zsh's exports, by name, each its whole line.
func (s *Secrets) envLines() (map[string]string, []string, error) {
	f, err := os.Open(filepath.Join(s.home, EnvFile))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	lines := map[string]string{}
	var order []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "export ")
		if !ok {
			continue
		}
		name, _, ok := strings.Cut(rest, "=")
		if ok {
			if _, seen := lines[name]; !seen {
				order = append(order, name)
			}
			lines[name] = strings.TrimSpace(sc.Text())
		}
	}
	return lines, order, sc.Err()
}

// Installed lists the secrets in place: each variable ~/.secrets.zsh
// exports (one no line declares is extra), and each file there with its
// declared mode. It asks 1Password nothing.
func (s *Secrets) Installed(context.Context) ([]kind.Installed, error) {
	_, order, err := s.envLines()
	if err != nil {
		return nil, err
	}
	var out []kind.Installed
	for _, name := range order {
		out = append(out, kind.Installed{Name: name, Explicit: true})
	}
	for _, sec := range s.declared {
		if !sec.IsFile() {
			continue
		}
		if info, err := os.Stat(s.path(sec)); err == nil && info.Mode().Perm() == sec.Mode {
			out = append(out, kind.Installed{Name: sec.Name, Explicit: true})
		}
	}
	return out, nil
}

// Resolve knows every secret.
func (s *Secrets) Resolve(_ context.Context, names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = name
	}
	return out, nil
}

// Value can't say where 1Password keeps a secret found on the Mac.
func (s *Secrets) Value(_ context.Context, name string) (string, error) {
	return "", fmt.Errorf("kit can't tell where 1Password keeps %s: declare it with kit add secret %s --ref op://vault/item/field", name, name)
}

// Install syncs every secret: those missing, and the rest, in one sitting.
func (s *Secrets) Install(ctx context.Context, _ []string) error {
	report, err := s.Sync(ctx)
	if err != nil {
		return err
	}
	if len(report.Failed) > 0 {
		return report
	}
	return nil
}

// Report is what a sync did: how many it synced, how many of those changed,
// and those that failed, each with why, keeping their previous values.
type Report struct {
	Synced, Changed int
	Failed          map[string]string
}

func (r Report) Error() string {
	var parts []string
	for _, name := range slices.Sorted(maps.Keys(r.Failed)) {
		parts = append(parts, name+": "+r.Failed[name])
	}
	return "couldn't sync " + strings.Join(parts, "; ") + " (each keeps its previous value)"
}

// Summary says what the sync did, values never shown.
func (r Report) Summary() string {
	text := fmt.Sprintf("%d synced, %d changed", r.Synced, r.Changed)
	if len(r.Failed) > 0 {
		text += fmt.Sprintf(", %d failed", len(r.Failed))
	}
	return text
}

// signInTimeout is how long 1Password's app may wait for a person to
// approve kit's use of it, a fingerprint at the prompt it shows.
const signInTimeout = time.Minute

// Answers reports whether 1Password answers: a session open already (op
// whoami, which never opens one), or one the app opens, asking for a
// fingerprint, within a minute (listing the vaults needs one).
func (s *Secrets) Answers(ctx context.Context) bool {
	if _, err := s.run.Run(ctx, runner.Command{Name: "op", Args: []string{"whoami"}, Timeout: opTimeout}); err == nil {
		return true
	}
	_, err := s.run.Run(ctx, runner.Command{Name: "op", Args: []string{"vault", "list", "--format", "json"}, Timeout: signInTimeout})
	return err == nil
}

// read reads a value from 1Password, its output kept from every report,
// without the newline op ends it with.
func (s *Secrets) read(ctx context.Context, ref string) (string, error) {
	res, err := s.run.Run(ctx, runner.Command{Name: "op", Args: []string{"read", ref}, Timeout: opTimeout, Secret: true})
	if err != nil {
		return "", errors.New("1Password couldn't read it")
	}
	return strings.TrimRight(string(res.Stdout), "\n"), nil
}

// Sync reads every declared secret from 1Password and puts each in place:
// ~/.secrets.zsh written whole (a variable that fails to read keeps its
// line, so a partial read never shrinks it), each file written beside its
// place then renamed into it. It fails at once when 1Password doesn't
// answer.
func (s *Secrets) Sync(ctx context.Context) (Report, error) {
	report := Report{Failed: map[string]string{}}
	if !s.Answers(ctx) {
		return report, errors.New(SignIn)
	}
	old, oldOrder, err := s.envLines()
	if err != nil {
		return report, err
	}
	values := map[string]string{}
	for _, sec := range s.declared {
		if _, ok := values[sec.Ref]; ok {
			continue
		}
		if v, err := s.read(ctx, sec.Ref); err == nil {
			values[sec.Ref] = v
		}
	}
	var env []string
	for _, sec := range s.declared {
		v, ok := values[sec.Ref]
		switch {
		case !ok:
			report.Failed[sec.Name] = "1Password couldn't read " + sec.Ref
			if line, kept := old[sec.Name]; kept && !sec.IsFile() {
				env = append(env, line)
			}
			continue
		case sec.IsFile():
			changed, err := s.writeFile(sec, v)
			if err != nil {
				report.Failed[sec.Name] = err.Error()
				continue
			}
			report.Changed += changed
		default:
			line := "export " + sec.Name + "=" + quote(v)
			if sec.Note != "" {
				line += "   # " + sec.Note
			}
			if old[sec.Name] != line {
				report.Changed++
			}
			env = append(env, line)
		}
		report.Synced++
	}
	// A variable no line declares stays: taking it out is kit reconcile's.
	for _, name := range oldOrder {
		if !slices.ContainsFunc(s.declared, func(sec Secret) bool { return sec.Name == name }) {
			env = append(env, old[name])
		}
	}
	header := "# Written by kit from 1Password: kit-config's [secrets]. kit apply secret and\n# kit secrets sync rewrite it whole; change a secret in 1Password, or with kit.\n"
	if err := writeFile(filepath.Join(s.home, EnvFile), header+strings.Join(env, "\n")+"\n", 0o600); err != nil {
		return report, err
	}
	return report, nil
}

// writeFile puts a file secret's value in place, with its mode, ended by a
// newline: 1 when it changed, else 0.
func (s *Secrets) writeFile(sec Secret, value string) (int, error) {
	path := s.path(sec)
	if old, err := os.ReadFile(path); err == nil && string(old) == value+"\n" {
		if info, err := os.Stat(path); err == nil && info.Mode().Perm() == sec.Mode {
			return 0, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return 0, err
	}
	return 1, writeFile(path, value+"\n", sec.Mode)
}

// Remove takes each secret off the Mac: a variable out of ~/.secrets.zsh, a
// file deleted. 1Password keeps the value.
func (s *Secrets) Remove(_ context.Context, names []string) error {
	var errs []error
	drop := map[string]bool{}
	for _, name := range names {
		i := slices.IndexFunc(s.declared, func(sec Secret) bool { return sec.Name == name })
		if i < 0 || !s.declared[i].IsFile() {
			drop[name] = true
			continue
		}
		if err := os.Remove(s.path(s.declared[i])); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if len(drop) > 0 {
		errs = append(errs, s.dropEnv(drop))
	}
	return errors.Join(errs...)
}

// dropEnv rewrites ~/.secrets.zsh without the variables named in drop.
func (s *Secrets) dropEnv(drop map[string]bool) error {
	path := filepath.Join(s.home, EnvFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var kept []string
	for line := range strings.Lines(string(data)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "export "); ok {
			if name, _, _ := strings.Cut(rest, "="); drop[name] {
				continue
			}
		}
		kept = append(kept, line)
	}
	return writeFile(path, strings.Join(kept, ""), 0o600)
}

// quote is a value as zsh reads it back exactly, sourced: in single quotes,
// each single quote in it closed, escaped and reopened, so nothing in it
// ($, a backtick, a backslash) is expanded or run.
func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// writeFile writes data to path whole or not at all, with mode: beside it,
// mode set before anything's written, then renamed into place.
func writeFile(path, data string, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".kit-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteString(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var _ interface {
	kind.Kind
	kind.Valued
	kind.Verber
} = (*Secrets)(nil)
