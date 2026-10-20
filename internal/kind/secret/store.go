package secret

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/leeovery/kit/internal/kind/secret/store"
)

// Ready says whether d answers, as a store keeping a value needs: nil once
// it does; not yet, signed out; or why not, in d's words.
func (s *Secrets) Ready(ctx context.Context, d store.Driver) error {
	return s.ready(ctx, d)
}

// Free is nil when d's item keeps nothing at short: else that something's
// there already. It changes nothing.
func (s *Secrets) Free(ctx context.Context, d store.Driver, item, short string) error {
	return d.Free(ctx, s.run, item, short)
}

// Reads is nil when ref, in full, reads from d: else why not, the value
// never shown.
func (s *Secrets) Reads(ctx context.Context, d store.Driver, ref string) error {
	if _, err := d.Read(ctx, s.run, ref); err != nil {
		return fmt.Errorf("%s doesn't read from %s: %w", ref, d.About().Name, err)
	}
	return nil
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

// ReadBack reads ref, in full, from d and compares it with want, never
// showing either.
func (s *Secrets) ReadBack(ctx context.Context, d store.Driver, ref, want string) error {
	got, err := d.Read(ctx, s.run, ref)
	if err != nil {
		return err
	}
	if got != strings.TrimRight(want, "\n") {
		return errors.New(d.About().Name + " gave back a different value")
	}
	return nil
}
