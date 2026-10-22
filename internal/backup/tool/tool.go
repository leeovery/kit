// Package tool is the contract a backup tool's driver keeps, as Time
// Machine's and Arq's do. A Mac's [backups] section names the tools it
// uses, a line each, and kit's shared code asks each one's driver for
// everything that's the tool's own: whether it's set up, and setting it up
// when it isn't; when its backups last finished, and whether one's running;
// what's wrong, in its own words; and what it does with [backup
// exclusions].
package tool

import (
	"context"
	"time"

	"github.com/leeovery/kit/internal/runner"
)

// Driver is a backup tool, as kit checks it: its commands go through the
// runner given.
type Driver interface {
	// About is what kit knows of it that's data.
	About() About
	// Look says how its backups stand on this Mac at now, asking no one: an
	// error when it couldn't find out.
	Look(ctx context.Context, run runner.Runner, now time.Time) (Status, error)
	// Excluder keeps [backup exclusions] out of its backups: nil for a tool
	// keeping none of its own, whose exclusions come by another's list
	// (About.Follows).
	Excluder() Excluder
}

// About is what kit knows of a backup tool that's data.
type About struct {
	// ID names it in [backups] and in kit's steps, as in time-machine; Name
	// is the tool as the person knows it, as in Time Machine.
	ID, Name string
	// SetUp is setting it up on a Mac it isn't set up on, which is the
	// person's to do.
	SetUp SetUp
	// Follows names the tool, by its ID, whose exclusions it leaves out of
	// its backups too, when it keeps none of its own (no Excluder): kit
	// writes that tool's list for it, whether or not the Mac lists that
	// tool. "" for one keeping its own.
	Follows string
	// Package is what installs it, as a line names one of a step's things,
	// kind:item (cask:arq): "" for one that comes with macOS.
	Package string
}

// SetUp is setting a tool up, as a to-do says it: its title, why, its steps
// in order and what kit watches for; and Do, all of it in a line, as kit
// status says it.
type SetUp struct {
	Title, Why string
	Steps      []string
	Watch      string
	Do         string
}

// Status is how a tool's backups stand on a Mac.
type Status struct {
	// Missing is whether it isn't installed, so there's nothing to look
	// at: kit says how to install it, as what installs it (About.Package)
	// is declared or not.
	Missing bool
	// NotSetUp is whether it's never been set up on the Mac, so there's
	// nothing to look at: setting it up is the person's (About.SetUp).
	NotSetUp bool
	// Backups are when its backups last finished, as it shows them, each
	// one it backs up by, as a plan: Plan names it among several, "" for
	// the tool's only one; Last is zero when it has none finished.
	Backups []Backup
	// Running is whether a backup is under way now.
	Running bool
	// Summary is how its backups stand, in its own words, when the backups
	// don't say it: "" for kit to say it from them, as in "last backup
	// 11:20", or, none finished, "first backup under way" or "no backup
	// yet".
	Summary string
	// Problems are what's wrong, each in its own words.
	Problems []Problem
}

// Backup is one way a tool backs up, as a plan, and when its last backup
// finished.
type Backup struct {
	Plan string
	Last time.Time
}

// Problem is something wrong with a tool's backups: its key, the same from
// run to run, as in stale; what's wrong; and what to do about it.
type Problem struct {
	Key, What, Do string
}

// Excluder keeps paths out of a tool's backups, each a full path: one kept
// out fixed, wherever it is, so before its folder exists, as a path
// declared plainly is; or where it is now, as a glob's match is.
type Excluder interface {
	// Program is the program it's driven with.
	Program() string
	// Excluded are the paths it keeps out: every one fixed, and those of
	// here it keeps out where they are.
	Excluded(ctx context.Context, run runner.Runner, here []string) ([]string, error)
	// Exclude keeps path out, fixed or where it is.
	Exclude(ctx context.Context, run runner.Runner, path string, fixed bool) error
	// Include stops keeping out path, kept out fixed.
	Include(ctx context.Context, run runner.Runner, path string) error
	// NeedsAdmin is whether keeping a path out, fixed or where it is, needs
	// an administrator's password.
	NeedsAdmin(fixed bool) bool
}
