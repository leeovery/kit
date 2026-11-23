package cli

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/leeovery/kit/internal/kind"
)

// kit's own flags take no values: lineAfter reads a word that isn't a
// flag, before a line, as a command's name.
func TestKitsOwnFlagsTakeNoValues(t *testing.T) {
	root := NewRootCommand(Deps{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.NoOptDefVal == "" {
			t.Errorf("kit's --%s takes a value: a line copied for a program can't then tell it from a command's name", f.Name)
		}
	})
}

// A program's verb of two words is a command of kit's for its first,
// holding one for its second, under its aliases too, as in kit gh
// extension install; the line is what's after the verb, kit's own flags
// before the kind's name read as kit's.
func TestALinesVerbOfTwoWords(t *testing.T) {
	a := &app{}
	root := &cobra.Command{Use: "kit"}
	root.PersistentFlags().BoolVar(&a.plain, "plain", false, "")
	names := func(args []string) ([]string, error) { return args, nil }
	root.AddCommand(newKindCommand(a, kind.Descriptor{Name: "gh", Help: &kind.Help{What: "extensions", Add: "add", Remove: "remove", Line: &kind.LineHelp{
		Program:   "gh",
		Install:   kind.LineVerb{Words: []string{"extension", "install"}, Aliases: []string{"i"}},
		Uninstall: kind.LineVerb{Words: []string{"extension", "remove"}, Aliases: []string{"rm"}},
		Names:     names, Kinds: func([]string) []string { return []string{"gh"} },
	}}}))
	for _, path := range [][]string{{"gh", "extension", "install"}, {"gh", "extension", "i"}, {"gh", "extension", "remove"}, {"gh", "extension", "rm"}} {
		cmd, _, err := root.Find(path)
		if err != nil || cmd.Parent().Name() != "extension" || cmd.Parent().Parent().Name() != "gh" {
			t.Errorf("Find(%q) = %v, %v; want a command under kit gh extension", path, cmd, err)
		}
	}
	if extensions := slices.DeleteFunc(slices.Clone(root.Commands()[0].Commands()), func(c *cobra.Command) bool { return c.Name() != "extension" }); len(extensions) != 1 {
		t.Errorf("kit gh has %d extension commands, want one holding both verbs", len(extensions))
	}
	cmd, _, _ := root.Find([]string{"gh", "extension", "i"})
	a.args = []string{"--plain", "gh", "extension", "i", "--force", "owner/gh-tool"}
	line, err := a.lineAfter(cmd)
	if err != nil || !slices.Equal(line, []string{"--force", "owner/gh-tool"}) || !a.plain {
		t.Errorf("lineAfter = %q, %v, --plain read %v; want the line after the verb, kit's flag read", line, err, a.plain)
	}
}

// --note is an install's alone.
func TestReadLine(t *testing.T) {
	l, err := readLine([]string{"--cask", "--note", "why", "--shared", "firefox"}, true)
	if err != nil || l.note != "why" || !l.shared || !slices.Equal(l.args, []string{"--cask", "firefox"}) {
		t.Errorf("readLine = %+v, %v", l, err)
	}
	if _, err := readLine([]string{"--note=why", "firefox"}, false); !errors.Is(err, errNoteUninstalling) {
		t.Errorf("readLine of an uninstall's --note = %v, want it refused", err)
	}
}
