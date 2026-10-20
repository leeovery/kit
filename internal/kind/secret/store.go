package secret

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/leeovery/kit/internal/kind/secret/store"
)

// StoreOf is the store whose item, or value in full, ref is.
func (s *Secrets) StoreOf(ref string) (store.Driver, error) {
	if d, ok := s.stores.ofItem(ref); ok {
		return d, nil
	}
	if d, ok := s.stores.ofRef(ref); ok {
		return d, nil
	}
	return nil, fmt.Errorf("%q isn't a reference kit knows: an item's (%s), or a value's in full (%s)", ref, s.stores.items(), s.stores.refs())
}

// ItemOf is the store whose item ref is: an error, naming the items kit
// knows, when it's no store's.
func (s *Secrets) ItemOf(ref string) (store.Driver, error) {
	if d, ok := s.stores.ofItem(ref); ok {
		return d, nil
	}
	return nil, s.stores.itemOf(ref)
}

// RefOf is the store whose value ref is, in full: an error, naming the
// references kit knows, when it's no store's.
func (s *Secrets) RefOf(ref string) (store.Driver, error) {
	if d, ok := s.stores.ofRef(ref); ok {
		return d, nil
	}
	return nil, fmt.Errorf("%q isn't where a store keeps a value, in full: %s", ref, s.stores.refs())
}

// Items says how the stores' items' references read, as in 1Password's,
// op://vault/item.
func (s *Secrets) Items() string { return s.stores.items() }

// Store keeps value in item, where field says, in its store's words: its
// reference, short for item. A field there already is refused.
func (s *Secrets) Store(ctx context.Context, item, field, value string) (string, error) {
	d, err := s.ItemOf(item)
	if err != nil {
		return "", err
	}
	return d.Store(ctx, s.run, item, field, value)
}

// Attach keeps the file at path in item, named name: its reference, short
// for item.
func (s *Secrets) Attach(ctx context.Context, item, name, path string) (string, error) {
	d, err := s.ItemOf(item)
	if err != nil {
		return "", err
	}
	return d.Attach(ctx, s.run, item, name, path)
}

// Delete deletes the value short names from item.
func (s *Secrets) Delete(ctx context.Context, item, short string) error {
	d, err := s.ItemOf(item)
	if err != nil {
		return err
	}
	return d.Delete(ctx, s.run, item, short)
}

// ReadBack reads ref, in full, from its store and compares it with want,
// never showing either.
func (s *Secrets) ReadBack(ctx context.Context, ref, want string) error {
	d, err := s.RefOf(ref)
	if err != nil {
		return err
	}
	got, err := d.Read(ctx, s.run, ref)
	if err != nil {
		return err
	}
	if got != strings.TrimRight(want, "\n") {
		return errors.New(d.About().Name + " gave back a different value")
	}
	return nil
}
