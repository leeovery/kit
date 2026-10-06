package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/cli"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// twoMacs is a kit.toml knowing two Macs.
const twoMacs = `format = 1
primary = "laptop"

[macs.laptop]
description = "MacBook Pro"

[macs.studio]
description = "Mac Studio"
`

// world is a home of a test's own, with kit's config repository in it, a
// clock, and a stand-in for every program kit runs.
type world struct {
	home string
	env  map[string]string
	now  time.Time
	fake *runnertest.Fake
	// environ is kit's environment, whole, beside env's lookups.
	environ []string
	// path and childEnv are what kit last made its runner with.
	path     []string
	childEnv []string
	// terminal is whether kit's output is a terminal.
	terminal bool
	// choose answers kit's questions at a terminal; asked notes each.
	choose func(question string, options []string) (int, error)
	asked  []string
}

// newWorld makes a home holding a config repository of files, by name.
func newWorld(t *testing.T, files map[string]string) *world {
	t.Helper()
	w := &world{home: t.TempDir(), env: map[string]string{}, now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), fake: runnertest.New(t)}
	for name, content := range files {
		w.write(t, filepath.Join(".config", "kit", name), content)
	}
	return w
}

// write writes a file at path, from the home, making its directories.
func (w *world) write(t *testing.T, path, content string) {
	t.Helper()
	full := filepath.Join(w.home, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// read reads the file at path, from the home: "" when there isn't one.
func (w *world) read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(w.home, path))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// run runs kit with args in the world, returning what it printed and the
// status it exits with.
func (w *world) run(t *testing.T, args ...string) (stdout, stderr string, status int) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := cli.NewRootCommand(cli.Deps{
		Version:  "0.1.0",
		Getenv:   func(name string) string { return w.env[name] },
		Environ:  func() []string { return w.environ },
		HomeDir:  func() (string, error) { return w.home, nil },
		Now:      func() time.Time { return w.now },
		Stdout:   &out,
		Stderr:   &errOut,
		Terminal: func(io.Writer) bool { return w.terminal },
		Width:    func(io.Writer) int { return 80 },
		Runner: func(path, env []string) runner.Runner {
			w.path, w.childEnv = path, env
			return w.fake
		},
		Scratch:   filepath.Join(w.home, "Scratch"),
		SudoLocal: filepath.Join(w.home, "etc", "sudo_local"),
		UID:       501,
		Choose: func(_ context.Context, question string, options []string) (int, error) {
			w.asked = append(w.asked, question)
			if w.choose == nil {
				t.Errorf("kit asked %q, which this test doesn't answer", question)
				return 0, errors.New("unanswered")
			}
			return w.choose(question, options)
		},
	})
	root.SetArgs(args)
	status = cli.Execute(t.Context(), root)
	return out.String(), errOut.String(), status
}

// errNotFound is what the runner says of a program that isn't installed.
var errNotFound = runner.ErrNotFound

// expectSync scripts the config repository's commit of files with message,
// and its push.
func (w *world) expectSync(files []string, message string) {
	dir := filepath.Join(w.home, ".config", "kit")
	git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
	w.fake.On("git", git(append([]string{"status", "--porcelain", "--"}, files...)...)...).Prints(" M " + files[0] + "\n")
	w.fake.On("git", git(append([]string{"add", "--"}, files...)...)...)
	w.fake.On("git", git(append([]string{"commit", "--quiet", "-m", message, "--"}, files...)...)...)
	w.fake.On("git", git("remote")...).Prints("origin\n")
	w.fake.On("git", git("pull", "--rebase", "--autostash", "--quiet")...)
	w.fake.On("git", git("push", "--quiet")...)
}

// writeSection sets the section headed header, in scope's declarations
// file, to lines, keeping the file's other sections; the section is added at
// the end when the file hasn't one.
func (w *world) writeSection(t *testing.T, scope, header, lines string) {
	t.Helper()
	path := filepath.Join(".config", "kit", config.DeclFile(scope))
	type section struct{ header, body string }
	var sections []section
	for line := range strings.Lines(w.read(t, path)) {
		if strings.HasPrefix(line, "[") {
			sections = append(sections, section{header: strings.TrimSpace(line)})
		} else if len(sections) > 0 {
			sections[len(sections)-1].body += line
		}
	}
	want := "[" + header + "]"
	i := slices.IndexFunc(sections, func(s section) bool { return s.header == want })
	if i < 0 {
		sections = append(sections, section{header: want})
		i = len(sections) - 1
	}
	sections[i].body = lines
	var b strings.Builder
	for j, s := range sections {
		if j > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s.header + "\n" + strings.TrimRight(s.body, "\n") + "\n")
	}
	w.write(t, path, b.String())
}

// readSection reads the lines of the section headed header in scope's
// declarations file: "" when there's no such section.
func (w *world) readSection(t *testing.T, scope, header string) string {
	t.Helper()
	var b strings.Builder
	in := false
	for line := range strings.Lines(w.read(t, filepath.Join(".config", "kit", config.DeclFile(scope)))) {
		if strings.HasPrefix(line, "[") {
			in = strings.TrimSpace(line) == "["+header+"]"
			continue
		}
		if in {
			b.WriteString(line)
		}
	}
	if text := strings.TrimRight(b.String(), "\n"); text != "" {
		return text + "\n"
	}
	return ""
}
