package secret

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/whole"
)

// Put puts the secret declared as name in place, its value in hand, asking
// no store: a file, as a sync writes it, with its mode; a variable, its line
// in ~/.secrets.zsh, as a sync writes it and where (putEnv), every other
// line kept byte for byte. A variable whose value holds a line break is
// refused (OneLine).
func (s *Secrets) Put(name, value string) error {
	i := slices.IndexFunc(s.declared, func(sec Secret) bool { return sec.Name == name })
	switch {
	case i < 0:
		return fmt.Errorf("%s isn't declared", name)
	case s.declared[i].IsFile():
		_, err := s.writeFile(s.declared[i], value)
		return err
	}
	if err := OneLine(name, value); err != nil {
		return err
	}
	return s.putEnv(i, envLine(s.declared[i], value))
}

// putEnv sets line, exporting the variable declared nth, in ~/.secrets.zsh:
// in place of each line exporting it, else before the first line exporting
// one a sync writes after it, one declared after it or by no line, else at
// the end. Made with the header and that line alone when there's no file:
// the rest come with the next sync.
func (s *Secrets) putEnv(nth int, line string) error {
	path := filepath.Join(s.home, EnvFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return whole.Write(path, []byte(header(s.used())+line+"\n"), 0o600)
	}
	if err != nil {
		return err
	}
	name := s.declared[nth].Name
	lines := slices.Collect(strings.Lines(string(data)))
	replaced := false
	for i, l := range lines {
		if v, ok := exported(l); ok && v == name {
			lines[i], replaced = line+"\n", true
		}
	}
	if !replaced {
		before := map[string]bool{}
		for _, sec := range s.declared[:nth] {
			before[sec.Name] = true
		}
		at := slices.IndexFunc(lines, func(l string) bool {
			v, ok := exported(l)
			return ok && !before[v]
		})
		if at < 0 {
			at = len(lines)
			if at > 0 && !strings.HasSuffix(lines[at-1], "\n") {
				lines[at-1] += "\n"
			}
		}
		lines = slices.Insert(lines, at, line+"\n")
	}
	return whole.Write(path, []byte(strings.Join(lines, "")), 0o600)
}

// envLine is the line of ~/.secrets.zsh exporting sec's value, v, without
// the newlines it ends with, and its note.
func envLine(sec Secret, v string) string {
	line := "export " + sec.Name + "=" + quote(strings.TrimRight(v, "\n"))
	if sec.Note != "" {
		line += "   # " + sec.Note
	}
	return line
}

// OneLine is nil unless name is a variable's and value holds a line break,
// but those it ends with: then why it can't be one, as a sync says.
func OneLine(name, value string) error {
	if !(Secret{Name: name}).IsFile() && manyLines(value) {
		return errors.New(oneLine)
	}
	return nil
}

// manyLines reports whether value holds a line break, but those it ends
// with.
func manyLines(value string) bool {
	return strings.Contains(strings.TrimRight(value, "\n"), "\n")
}
