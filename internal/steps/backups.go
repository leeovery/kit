package steps

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/backup"
	"github.com/leeovery/kit/internal/backup/tool"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner"
)

// rowBackups is the row of applying's screen the backup tools' steps sit
// in.
const rowBackups = "Backups"

// BackupTool is a backup tool a Mac uses, as its [backups] line says: its
// driver, and what its step is checked after (a step, or one of a step's
// things, as in cask:arq) and before.
type BackupTool struct {
	Driver        tool.Driver
	After, Before []string
	// Pos is where its line is, as in laptop/declarations:12.
	Pos string
}

// ParseBackup reads a [backups] line, as ReadBackup does, saying where it
// is when it doesn't read.
func ParseBackup(e config.Entry) (BackupTool, error) {
	b, err := ReadBackup(e.Name, e.Value)
	if err != nil {
		return b, fmt.Errorf("%s: %w", e.Pos(), err)
	}
	b.Pos = e.Pos()
	return b, nil
}

// ReadBackup reads a backup tool's line: name, a tool kit knows, by its
// ID; options, --after and --before, each with a step's name (--after one
// of a step's things too, as in cask:arq) and as often as wanted.
func ReadBackup(name, options string) (BackupTool, error) {
	d, ok := backup.Named(name)
	if !ok {
		return BackupTool{}, fmt.Errorf("kit doesn't know the backup tool %s: one of %s", name, strings.Join(backup.IDs(), ", "))
	}
	b := BackupTool{Driver: d}
	words, err := config.Words(options)
	if err != nil {
		return b, err
	}
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w != "--after" && w != "--before" {
			return b, fmt.Errorf("%q isn't one of a backup tool's options: --after and --before", w)
		}
		name, err := orderedBy(w, words, i)
		if err != nil {
			return b, err
		}
		i++
		if w == "--after" {
			b.After = append(b.After, name)
		} else {
			b.Before = append(b.Before, name)
		}
	}
	return b, nil
}

// Packages says of one of a step's things, as in cask:arq, whether this Mac
// declares it, and whether it's installed.
type Packages func(ctx context.Context, thing string) (declared, installed bool)

// Backup checks a backup tool's backups, as its driver says they stand:
// installed, or how to install it, as packages says what the tool comes as
// (About.Package) stands; set up, or the person's to set up; then what's
// wrong, in the tool's words, and how they stand, when the tool's words
// don't say, from when they last finished.
func Backup(run runner.Runner, d tool.Driver, now func() time.Time, packages Packages) engine.Step {
	about := d.About()
	return engine.Step{
		Name: about.ID, Title: about.Name, Area: AreaBackups, Row: rowBackups,
		Check: func(ctx context.Context) check.Result {
			at := now()
			st, err := d.Look(ctx, run, at)
			switch {
			case err != nil:
				return failed(err)
			case st.Missing:
				what, do := missing(ctx, about, packages)
				return problem(about.ID, "not installed", [3]string{"missing", what, do})
			case st.NotSetUp:
				s := about.SetUp
				return yours(about.ID, "not set up", "none", s.Do, check.ToDo{Title: s.Title, Why: s.Why, Steps: s.Steps, Watch: s.Watch, Group: groupBackups})
			}
			summary := backupSummary(st, at)
			if len(st.Problems) > 0 {
				problems := make([][3]string, len(st.Problems))
				for i, p := range st.Problems {
					problems[i] = [3]string{p.Key, p.What, p.Do}
				}
				return problem(about.ID, summary, problems...)
			}
			glance := about.Name + "'s " + summary
			if times := finished(st.Backups, func(b tool.Backup) string { return check.When(b.Last, at) }); len(times) > 0 {
				glance = about.Name + " " + strings.Join(times, ", ")
			}
			return check.Result{State: check.OK, Summary: summary, Glance: glance}
		},
	}
}

// missing is what's wrong with the tool about tells of, not installed, and
// what to do, as what it comes as (its Package, one of a step's things, as
// in cask:arq) stands: not declared, it's declared, as kit cask add arq
// does; declared, kit apply installs it; declared and installed, there's
// nothing for kit to do, so it says so plainly.
func missing(ctx context.Context, about tool.About, packages Packages) (what, do string) {
	what = about.Name + " isn't installed"
	kindName, item, ok := strings.Cut(about.Package, ":")
	if !ok {
		return what, "install it"
	}
	switch declared, installed := packages(ctx, about.Package); {
	case !declared:
		return what, "install it: kit " + kindName + " add " + item
	case installed:
		return what + ", though its " + kindName + " is", ""
	}
	return what, "kit apply installs it"
}

// backupSummary is how a tool's backups stand: in its words, when it has
// some; else when each last finished, the plan named when it has one, or,
// none finished yet, the first under way or none yet.
func backupSummary(st tool.Status, now time.Time) string {
	if st.Summary != "" {
		return st.Summary
	}
	last := finished(st.Backups, func(b tool.Backup) string {
		return strings.TrimSpace(b.Plan + " " + check.When(b.Last, now))
	})
	switch {
	case len(last) > 0:
		return "last backup " + strings.Join(last, ", ")
	case st.Running:
		return "first backup under way"
	}
	return "no backup yet"
}

// finished are the backups that have finished, each as say has it, sorted.
func finished(backups []tool.Backup, say func(tool.Backup) string) []string {
	var out []string
	for _, b := range backups {
		if !b.Last.IsZero() {
			out = append(out, say(b))
		}
	}
	slices.Sort(out)
	return out
}
