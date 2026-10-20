// Package secret is secrets as a kind: each declared in a secrets section,
// what it fills (an environment variable, in ~/.secrets.zsh, which the shell
// sources; or a file, by its path) then where a secrets store keeps its
// value: a field or attachment of the item the section's header names, as
// in [secrets op://vault/item], or in full in a plain [secrets]. An option:
// --mode for a file's (600 unless it says). The reference picks the store,
// its driver (Stores): a config may keep secrets in several.
//
// Checking never asks a store, which may ask for a fingerprint every hour:
// it reads only what's on the Mac. Syncing reads each store's values at
// once, as a store may have the person approve each read; a value that
// fails to read keeps its previous one, and a store that's signed out fails
// its own secrets alone. Values never pass through kit's events or log.
package secret

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner"
)

// EnvFile is the file of environment variables kit writes, under the home
// folder, which the shell sources.
const EnvFile = ".secrets.zsh"

// Secret is a declared secret.
type Secret struct {
	// Name is what it fills: an environment variable's name, or a file's
	// path as declared, ~ and all.
	Name string
	// Ref is where its store keeps its value, in full.
	Ref string
	// Mode is a file's permissions.
	Mode os.FileMode
	Note string
	// store is the store keeping its value, as Ref says.
	store store.Driver
}

// IsFile reports whether the secret is a file's.
func (s Secret) IsFile() bool {
	return strings.HasPrefix(s.Name, "~/") || strings.HasPrefix(s.Name, "/")
}

// Secrets is the secrets, each read from its store.
type Secrets struct {
	run      runner.Runner
	home     string
	declared []Secret
}

// kindName names the kind.
const kindName = "secret"

// Descriptor is secrets' kind, as kit's shared code knows it: kit secret is
// a command of its own.
var Descriptor = kind.Descriptor{
	Name:    kindName,
	Label:   "Secrets",
	Group:   kind.Secrets,
	Section: &config.Section{Kind: kindName, Header: "secrets", Form: config.Secrets, Sorted: true, Items: checkItem, Line: checkLine},
	Row:     "Secrets",
	New:     func(d *kind.Deps) kind.Kind { return New(d.Run, d.Home) },
}

// New returns the secrets of the user whose home is home, in the stores kit
// knows (Stores).
func New(run runner.Runner, home string) *Secrets {
	return &Secrets{run: run, home: home}
}

func (s *Secrets) Name() string { return kindName }

// Program is the program of the store the secrets declared are in: none
// when they're in several, or none.
func (s *Secrets) Program() string {
	if used := s.used(); len(used) == 1 {
		return used[0].About().Program
	}
	return ""
}

// Command is none: syncing waits for each store's program itself, so one
// store's missing program holds back only that store's secrets.
func (s *Secrets) Command() string { return "" }

// used are the stores the secrets declared are in, each once, in turn.
func (s *Secrets) used() []store.Driver {
	var out []store.Driver
	for _, sec := range s.declared {
		if !slices.ContainsFunc(out, func(d store.Driver) bool { return sameStore(d, sec.store) }) {
			out = append(out, sec.store)
		}
	}
	return out
}

// sameStore reports whether a and b are one store, by its ID.
func sameStore(a, b store.Driver) bool { return a.About().ID == b.About().ID }

// Verb says a secret on the Mac as declared is synced.
func (s *Secrets) Verb() string { return "synced" }

// Values reads each secret's line: its reference, short in an item's
// section or full in the plain one, picking its store, and its options. A
// list that doesn't read declares nothing.
func (s *Secrets) Values(list config.List) (config.List, error) {
	s.declared = nil
	for _, e := range list.Entries {
		sec, err := parse(e)
		if err != nil {
			s.declared = nil
			return list, fmt.Errorf("%s: %s: %w", e.Pos(), e.Name, err)
		}
		s.declared = append(s.declared, sec)
	}
	return list, nil
}

// checkLine says why e's line doesn't read as a secret's: nil when it does.
func checkLine(e config.Entry) error {
	_, err := parse(e)
	return err
}

// parse reads e's line as a secret's: its reference, then its options.
func parse(e config.Entry) (Secret, error) {
	sec := Secret{Name: e.Name, Mode: 0o600, Note: e.Note}
	words, err := config.Words(e.Value)
	if err != nil {
		return sec, err
	}
	if len(words) == 0 {
		return sec, errors.New("where its store keeps it comes after the name")
	}
	ref := words[0]
	if err := place(&sec, e, ref); err != nil {
		return sec, err
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

// place reads ref, a secret's reference on e's line, into sec: short for a
// field of the item e's section names, or, in a plain section, in full, its
// form picking its store. In an item's section, a reference of a store's
// own, a value's or an item's, is refused: it's not short for a field.
func place(sec *Secret, e config.Entry, ref string) error {
	ds := stores()
	if e.Item == "" {
		d, ok := ds.forRef(ref)
		if !ok {
			return fmt.Errorf("%q isn't a reference kit knows: in a plain [secrets], give where its store keeps it in full (%s), or put it in its item's section", ref, ds.refs())
		}
		sec.Ref, sec.store = ref, d
		return nil
	}
	d, ok := ds.forItem(e.Item)
	if !ok {
		return checkItem(e.Item)
	}
	if ds.owns(ref) {
		return fmt.Errorf("%s is in full: in [%s] a line names a field or attachment of its item, as in %s; a full reference goes in a plain [secrets]", ref, e.Section, d.About().Field)
	}
	sec.Ref, sec.store = d.Join(e.Item, ref), d
	return nil
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
// declared mode. It asks no store anything.
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

// Has reports whether the secret that fills name is in place, as Installed
// finds it.
func (s *Secrets) Has(ctx context.Context, name string) bool {
	have, err := s.Installed(ctx)
	return err == nil && slices.ContainsFunc(have, func(in kind.Installed) bool { return in.Name == name })
}

// Variable is the value ~/.secrets.zsh holds for the variable name, as kit
// wrote it, syncing: ok is false when it holds none. It asks no store
// anything, and reads no environment.
func (s *Secrets) Variable(name string) (value string, ok bool, err error) {
	lines, _, err := s.envLines()
	if err != nil {
		return "", false, err
	}
	line, ok := lines[name]
	if !ok {
		return "", false, nil
	}
	if value, ok = unquote(strings.TrimPrefix(line, "export "+name+"=")); !ok {
		return "", false, fmt.Errorf("~/%s holds %s otherwise than kit writes it: run kit secret sync", EnvFile, name)
	}
	return value, true, nil
}

// Resolve knows every secret.
func (s *Secrets) Resolve(_ context.Context, names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = name
	}
	return out, nil
}

// Value can't say where a secret found on the Mac is kept.
func (s *Secrets) Value(_ context.Context, name string) (string, error) {
	return "", fmt.Errorf("kit can't tell where %s is kept: declare it with kit secret add %s --ref <where its store keeps it, in full> (%s)", name, name, stores().refs())
}

// Remove takes each secret off the Mac: a variable out of ~/.secrets.zsh, a
// file deleted. Its store keeps the value.
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

// unquote reads a value as quote wrote it, and what may follow it on its
// line, a note: false when that's not what text is.
func unquote(text string) (string, bool) {
	var value strings.Builder
	for {
		rest, ok := strings.CutPrefix(text, "'")
		end := strings.IndexByte(rest, '\'')
		if !ok || end < 0 {
			return "", false
		}
		value.WriteString(rest[:end])
		if text, ok = strings.CutPrefix(rest[end+1:], `\'`); !ok {
			text = rest[end+1:]
			break
		}
		value.WriteByte('\'')
	}
	if note := strings.TrimSpace(text); note != "" && !strings.HasPrefix(note, "#") {
		return "", false
	}
	return value.String(), true
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
	kind.Commanded
	kind.Carded
} = (*Secrets)(nil)
