package testguard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// stubbed are the programs tests must never run for real: those kit drives,
// which change the Mac or reach real accounts, and those that drive launchd,
// open files and URLs, post notifications, reach the tmux server, restart
// apps, and change power, sharing, Spotlight and disks.
var stubbed = []string{"brew", "claude", "composer", "defaults", "diskutil", "gh", "git", "go", "killall", "launchctl", "mas", "mdfind", "mdutil", "npm", "op", "open", "osascript", "pmset", "sudo", "systemsetup", "tmutil", "tmux"}

// stubs is a directory of stand-ins for the stubbed programs. Each run of one
// adds a line to the record, its name and arguments, and fails.
type stubs struct {
	dir    string
	record string
}

// writeStubs creates dir, holding a stub of each stubbed program that notes
// its runs in the file at record.
func writeStubs(dir, record string) (stubs, error) {
	if err := os.Mkdir(dir, 0o700); err != nil {
		return stubs{}, fmt.Errorf("create the stubs' directory: %w", err)
	}
	for _, name := range stubbed {
		script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"%s $*\" >> %s\nexit 1\n", name, shellQuote(record))
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			return stubs{}, fmt.Errorf("write the %s stub: %w", name, err)
		}
	}
	return stubs{dir: dir, record: record}, nil
}

// runs lists the stubs' runs, a line each, such as "ran claude --version".
func (s stubs) runs() []string {
	data, err := os.ReadFile(s.record)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []string{fmt.Sprintf("can't tell which stubs ran: %v", err)}
	}
	var runs []string
	for line := range strings.Lines(string(data)) {
		runs = append(runs, "ran "+strings.TrimSpace(line))
	}
	return runs
}

// shellQuote quotes s for sh, whatever it holds.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
