package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/leeovery/kit/internal/cli"
)

// twoMacs is a kit.toml knowing two Macs.
const twoMacs = `format = 1
primary = "laptop"

[macs.laptop]
description = "MacBook Pro"

[macs.studio]
description = "Mac Studio"
`

// world is a home of a test's own, with kit's config repository in it.
type world struct {
	home string
	env  map[string]string
}

// newWorld makes a home holding a config repository of files, by name.
func newWorld(t *testing.T, files map[string]string) *world {
	t.Helper()
	w := &world{home: t.TempDir(), env: map[string]string{}}
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
		Version: "0.1.0",
		Getenv:  func(name string) string { return w.env[name] },
		HomeDir: func() (string, error) { return w.home, nil },
		Stdout:  &out,
		Stderr:  &errOut,
	})
	root.SetArgs(args)
	status = cli.Execute(t.Context(), root)
	return out.String(), errOut.String(), status
}
