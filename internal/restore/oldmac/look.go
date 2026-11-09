package oldmac

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/restore"
)

// lookTimeout ends a look that's taken longer: walking a listed folder of a
// million files takes under a minute.
const lookTimeout = 10 * time.Minute

// excludedAttr is the extended attribute marking what Time Machine and Arq
// leave out of their backups, as Asimov marks dependency folders and apps
// their caches.
const excludedAttr = "com.apple.metadata:com_apple_backup_excludeItem"

// Kind is what's at a listed path on the old Mac.
type Kind int

const (
	Missing Kind = iota
	File
	Folder
)

// What lookScript says is at each path, ahead of what's skipped in it.
var kindSays = map[string]Kind{"none": Missing, "file": File, "folder": Folder}

// Listed is what's at a listed path on the old Mac, and, for a folder, what
// in it the old Mac's backups leave out, and so the restore: each relative
// to it, as it is.
type Listed struct {
	Kind  Kind
	Skips []string
}

// Patterns are l's skips as a line's skips are (restore.Line's Skips), for
// its filter: each relative to the listed path, which the filter anchors
// them at, escaped where rsync would take one as a pattern.
func (l Listed) Patterns() []string {
	patterns := make([]string, len(l.Skips))
	for i, s := range l.Skips {
		patterns[i] = restore.Escape(s)
	}
	return patterns
}

// lookScript says, of each path, a path from ~, what's at it there, in one
// go, each answer ending in a NUL: a file (a link, as rsync copies one, is
// one), a folder, or none; and after a folder, what in it is marked left out
// of the backups, each from ./. find never looks inside what's marked, so a
// folder copied whole is copied whole. One that holds git checkouts (a
// folder in it with a .git, as agents' worktrees) isn't said, so it's copied
// whole, its dependency folders and all, and works on the new Mac at once.
// The listed path itself is never judged: it's listed to be restored. A
// folder find can't read is left as it is: it's copied, so rsync says what's
// wrong.
func lookScript(paths []string) string {
	lines := []string{
		`cd "$HOME" || exit 1`,
		`look() {`,
		`  if [ -L "$1" ]; then printf '%s\0' file`,
		`  elif [ -d "$1" ]; then`,
		`    printf '%s\0' folder`,
		`    (cd -- "$1" && find . -mindepth 1 -xattrname ` + excludedAttr + ` -prune ! -exec sh -c 'for c in "$1"/*/.git; do [ -e "$c" ] && exit 0; done; exit 1' sh {} \; -print0) 2>/dev/null`,
		`  elif [ -e "$1" ]; then printf '%s\0' file`,
		`  else printf '%s\0' none; fi`,
		`}`,
	}
	for _, p := range paths {
		lines = append(lines, "look "+shellQuote(strings.TrimPrefix(p, "~/")))
	}
	return strings.Join(lines, "\n")
}

// Look says what's at each of paths, each from ~, on m, and what in each
// folder its backups leave out, keyed by path: in one go, as each look over
// SSH costs a connection.
func (m *Mac) Look(ctx context.Context, paths []string) (map[string]Listed, error) {
	if len(paths) == 0 {
		return map[string]Listed{}, nil
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, "~/") {
			return nil, fmt.Errorf("look at %s on %s: the path isn't from ~", p, m.Name)
		}
	}
	res, err := m.run.Run(ctx, remote(m.opts, m.Host, lookScript(paths), lookTimeout))
	if err != nil {
		return nil, fmt.Errorf("look at the listed paths on %s: %w", m.Name, sshFailed(err))
	}
	return readLook(paths, string(res.Stdout), m.Name)
}

// readLook reads what lookScript printed, out, of paths on the Mac called
// name.
func readLook(paths []string, out, name string) (map[string]Listed, error) {
	listed := make(map[string]Listed, len(paths))
	i := -1
	answers := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if out == "" {
		answers = nil
	}
	for _, answer := range answers {
		if skip, ok := strings.CutPrefix(answer, "./"); ok && i >= 0 {
			l := listed[paths[i]]
			l.Skips = append(l.Skips, skip)
			listed[paths[i]] = l
			continue
		}
		kind, ok := kindSays[answer]
		if !ok || i+1 == len(paths) {
			return nil, fmt.Errorf("look at the listed paths on %s: it said %q, which isn't an answer", name, answer)
		}
		i++
		listed[paths[i]] = Listed{Kind: kind}
	}
	if i+1 < len(paths) {
		return nil, fmt.Errorf("look at the listed paths on %s: it said nothing of %s", name, paths[i+1])
	}
	return listed, nil
}
