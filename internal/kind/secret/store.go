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
// session open already, or one it opens, the person approving it
// (store.Driver.Unlock); not yet, signed out; or why not, in d's words.
func (s *Secrets) Ready(ctx context.Context, d store.Driver) error {
	switch d.Session(ctx, s.run) {
	case store.Open:
		return nil
	case store.SignedOut:
		return engine.NotYet(d.About().SignedOut)
	}
	if err := d.Unlock(ctx, s.run); err != nil {
		return errors.New(d.About().Name + " doesn't answer: " + d.About().NoAnswer)
	}
	return nil
}

// Free is nil when d's item keeps nothing at short: else that something's
// there already. It changes nothing.
func (s *Secrets) Free(ctx context.Context, d store.Driver, item, short string) error {
	return d.Free(ctx, s.run, item, short)
}

// Reads is nil when ref, in full, reads from d: else why not, the value
// never shown.
func (s *Secrets) Reads(ctx context.Context, d store.Driver, ref string) error {
	_, err := d.Read(ctx, s.run, ref)
	if err == nil {
		return nil
	}
	why := err.Error()
	if unread, ok := errors.AsType[store.Unread](err); ok {
		why = unread[ref]
	}
	return fmt.Errorf("%s doesn't read from %s: %s", ref, d.About().Name, why)
}

// Store keeps value in d's item, where field says, in d's words: its
// reference, short for item. A field there already is refused.
func (s *Secrets) Store(ctx context.Context, d store.Driver, item, field, value string) (string, error) {
	return d.Store(ctx, s.run, item, field, value)
}

// Attach keeps the file at path in d's item, named name: its reference,
// short for item.
func (s *Secrets) Attach(ctx context.Context, d store.Driver, item, name, path string) (string, error) {
	return d.Attach(ctx, s.run, item, name, path)
}

// Delete deletes the value short names from d's item.
func (s *Secrets) Delete(ctx context.Context, d store.Driver, item, short string) error {
	return d.Delete(ctx, s.run, item, short)
}

// ReadBack reads ref, in full, from d and compares it with want, byte for
// byte, never showing either.
func (s *Secrets) ReadBack(ctx context.Context, d store.Driver, ref, want string) error {
	return s.readBack(ctx, d.Read, ref, want, d.About().Name)
}

// ReadBackFile reads ref, in full, from d as a file holds it
// (store.Driver.ReadFile), and compares it with want, a file's contents,
// as ReadBack does.
func (s *Secrets) ReadBackFile(ctx context.Context, d store.Driver, ref, want string) error {
	return s.readBack(ctx, d.ReadFile, ref, want, d.About().Name)
}

// readBack reads ref with read, from the store called name, and compares
// it with want, byte for byte, never showing either.
func (s *Secrets) readBack(ctx context.Context, read func(context.Context, runner.Runner, string) (string, error), ref, want, name string) error {
	got, err := read(ctx, s.run, ref)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New(name + " gave back a different value")
	}
	return nil
}
