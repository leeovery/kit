package secret

import (
	"context"
	"errors"
	"fmt"

	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner"
)

// Ready says whether d answers, as a store keeping a value needs: nil for a
// session open already, kit secret signin's among them, open in another
// terminal (standing), or one it opens, the person approving it
// (store.Driver.Unlock); not yet, signed out; or why not, in d's words, at
// once when the person can't approve it (cantAsk).
func (s *Secrets) Ready(ctx context.Context, d store.Driver) error {
	switch s.standing(ctx, d) {
	case store.Open:
		return nil
	case store.SignedOut:
		return engine.NotYet(d.About().SignedOut)
	}
	if err := s.cantAsk(ctx, d); err != nil {
		return err
	}
	if err := d.Unlock(ctx, s.run); err != nil {
		return errors.New(d.About().Name + " doesn't answer: " + d.About().NoAnswer)
	}
	return nil
}

// Free is nil when d's item keeps nothing at short: else that something's
// there already. It changes nothing.
func (s *Secrets) Free(ctx context.Context, d store.Driver, item, short string) error {
	return d.Free(ctx, s.through(ctx, d), item, short)
}

// Fetch reads ref, in full, from d, for the secret that fills name: as a
// file holds it for a file's (store.Driver.ReadFile), else as a variable's.
// When it doesn't read, it says why, the value never shown.
func (s *Secrets) Fetch(ctx context.Context, d store.Driver, name, ref string) (string, error) {
	read := d.Read
	if (Secret{Name: name}).IsFile() {
		read = d.ReadFile
	}
	value, err := read(ctx, s.through(ctx, d), ref)
	if err == nil {
		return value, nil
	}
	why := err.Error()
	if unread, ok := errors.AsType[store.Unread](err); ok {
		why = unread[ref]
	}
	return "", fmt.Errorf("%s doesn't read from %s: %s", ref, d.About().Name, why)
}

// Store keeps value in d's item, where field says, in d's words: its
// reference, short for item. A field there already is refused.
func (s *Secrets) Store(ctx context.Context, d store.Driver, item, field, value string) (string, error) {
	return d.Store(ctx, s.through(ctx, d), item, field, value)
}

// Attach keeps the file at path in d's item, named name: its reference,
// short for item.
func (s *Secrets) Attach(ctx context.Context, d store.Driver, item, name, path string) (string, error) {
	return d.Attach(ctx, s.through(ctx, d), item, name, path)
}

// Delete deletes the value short names from d's item, d ready (Ready).
func (s *Secrets) Delete(ctx context.Context, d store.Driver, item, short string) error {
	return d.Delete(ctx, s.through(ctx, d), item, short)
}

// ReadBack reads ref, in full, from d and compares it with want, byte for
// byte, never showing either.
func (s *Secrets) ReadBack(ctx context.Context, d store.Driver, ref, want string) error {
	return s.readBack(ctx, d, d.Read, ref, want)
}

// ReadBackFile reads ref, in full, from d as a file holds it
// (store.Driver.ReadFile), and compares it with want, a file's contents,
// as ReadBack does.
func (s *Secrets) ReadBackFile(ctx context.Context, d store.Driver, ref, want string) error {
	return s.readBack(ctx, d, d.ReadFile, ref, want)
}

// readBack reads ref from d with read, one of d's own, and compares it with
// want, byte for byte, never showing either.
func (s *Secrets) readBack(ctx context.Context, d store.Driver, read func(context.Context, runner.Runner, string) (string, error), ref, want string) error {
	got, err := read(ctx, s.through(ctx, d), ref)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New(d.About().Name + " gave back a different value")
	}
	return nil
}
