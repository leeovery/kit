// Package store is the contract a secrets store's driver keeps, as
// 1Password's does. kit's secrets kind picks each secret's driver by its
// reference, so one config can keep secrets in several stores, and asks the
// driver for everything that's the store's own: reading, keeping and
// deleting values, whether it's signed in, its sign-in, a sign-in with a
// password when it has one (Passworded), and a command of its own run in
// that sign-in's session, its program and the environment that needs, and
// its words.
package store

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// Driver is a secrets store, as kit drives it: its commands go through the
// runner given.
type Driver interface {
	// About is what kit knows of it that's data.
	About() About
	// IsRef reports whether ref is where it keeps a value, in full.
	IsRef(ref string) bool
	// IsItem reports whether ref is one of its items, as a secrets
	// section's header names one, its lines short for the item's values.
	IsItem(ref string) bool
	// Join is the full reference of the value short names in item.
	Join(item, short string) string
	// Short is ref short for item, and whether ref is in item.
	Short(item, ref string) (string, bool)

	// Session says how it stands on this Mac, asking no one.
	Session(ctx context.Context, run runner.Runner) Session
	// Unlock opens a session, as reading a value needs, the person
	// approving it as the store asks them to: nil once it answers.
	Unlock(ctx context.Context, run runner.Runner) error
	// Read reads the value at ref, a field's text, as the store gives it
	// back: a file's bytes read whole with ReadFile alone.
	Read(ctx context.Context, run runner.Runner, ref string) (string, error)
	// ReadFile reads the value at ref as a file holds it: a file the store
	// keeps, an attachment, byte for byte; a field's text ended by one
	// newline, in place of any it ends with. It fails as Read does.
	ReadFile(ctx context.Context, run runner.Runner, ref string) (string, error)
	// Field is field short for item, where Store would keep a value, as
	// Store returns it: an error, in the store's own words (About.Field),
	// when it isn't where one can go. It asks the store nothing.
	Field(field string) (string, error)
	// Attachment is name short for item, where Attach would keep a file, as
	// Attach returns it, at the item's top or in a section, as the store
	// has them: an error when it isn't an attachment's name. It asks the
	// store nothing.
	Attachment(name string) (string, error)
	// Section is the section of its item short names, a value's reference
	// short for the item, and whether it names one. It asks the store
	// nothing.
	Section(short string) (string, bool)
	// Free is nil when item keeps nothing at short, where Field or
	// Attachment says a value would go: else that something's there
	// already. It changes nothing in the store.
	Free(ctx context.Context, run runner.Runner, item, short string) error
	// Store keeps value in item, where field says (Field), refusing a field
	// there already: its reference, short for item.
	Store(ctx context.Context, run runner.Runner, item, field, value string) (string, error)
	// Attach keeps the file at path in item, named name (Attachment): its
	// reference, short for item.
	Attach(ctx context.Context, run runner.Runner, item, name, path string) (string, error)
	// Delete deletes the value short names in item.
	Delete(ctx context.Context, run runner.Runner, item, short string) error
}

// Passworded is a store the person can sign in to with their password, in
// a terminal of their own, as away from the Mac. The session it gives back
// is the store's data, never shown or written down, and may hold only for
// what runs where they signed in: the kit there keeps it in memory, and
// runs the store's commands for every other kit, each in the session
// (Apply).
type Passworded interface {
	Driver
	// SignIn signs the person in, the store's program asking for their
	// password at kit's terminal itself (runner.Command's Terminal), kit
	// never seeing it: the session it gives back.
	SignIn(ctx context.Context, run runner.Runner) (string, error)
	// Apply is c, a command of the store's own as its driver built it,
	// run in session.
	Apply(session string, c runner.Command) runner.Command
	// Lasts reports whether session is open still: false, with no error,
	// once the store says it's over; an error when the store doesn't
	// answer, so it can't say.
	Lasts(ctx context.Context, run runner.Runner, session string) (bool, error)
	// SignOut ends session at the store.
	SignOut(ctx context.Context, run runner.Runner, session string) error
}

// Unread is a read that failed for the references it names, each with the
// store's words for why: the rest may read without them.
type Unread map[string]string

func (u Unread) Error() string {
	var said []string
	for _, ref := range slices.Sorted(maps.Keys(u)) {
		said = append(said, ref+": "+u[ref])
	}
	return strings.Join(said, "; ")
}

// Session is how a store stands on this Mac.
type Session int

const (
	// SignedOut: not signed in, so nothing reads till someone signs in.
	SignedOut Session = iota
	// SignedIn: signed in, with no session open, so reading asks the
	// person to approve it (Unlock).
	SignedIn
	// Open: a session's open, and values read without asking.
	Open
)

// About is what kit knows of a store that's data.
type About struct {
	// ID names it in kit's steps, as in 1password; Name is the store as the
	// person knows it, as in 1Password.
	ID, Name string
	// Program is the program it's driven with, which kit waits for on its
	// PATH.
	Program string
	// Ref, Item and Field are how its references read, as kit's words show
	// them: a value's in full, an item's, and where a value goes in an item
	// (Store), its form and an example.
	Ref, Item, Field string
	// NoAnswer says how to have it answer, signed in; SignedOut what's
	// wanting, when it isn't.
	NoAnswer, SignedOut string
	// AtScreen is whether its approval needs the person at the Mac's
	// screen, as a prompt only a fingerprint there answers: kit doesn't ask
	// for it while that screen's locked, and says WhenLocked instead, what
	// to do then.
	AtScreen   bool
	WhenLocked string
	// SignIn is signing in to it, as kit raises it at a terminal.
	SignIn SignIn
}

// SignIn is signing in to a store, as a card says it: its title and steps;
// and its app, by bundle id (App) and name, which kit waits for, installed,
// and opens as the card first comes up.
type SignIn struct {
	Title        string
	Steps        []string
	App, AppName string
	// Packages are what install its program and app, each as a line names
	// one of a step's things, kind:item (cask:1password): those the config
	// declares install first, as kit waits for them before the card.
	Packages []string
}
