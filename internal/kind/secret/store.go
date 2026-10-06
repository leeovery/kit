package secret

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// itemArgs are op's arguments naming item (op://vault/item): the item, and
// its vault.
func itemArgs(item string) ([]string, error) {
	rest, ok := strings.CutPrefix(item, "op://")
	vault, name, found := strings.Cut(rest, "/")
	if !ok || !found || vault == "" || name == "" || strings.Contains(name, "/") {
		return nil, fmt.Errorf("%q isn't a 1Password item's reference, as in op://vault/item", item)
	}
	return []string{name, "--vault", vault}, nil
}

// Store keeps value in item (op://vault/item), as the field section/field,
// a concealed one, by editing the item with its JSON on op's standard
// input, so the value is never on a command line. A field there already is
// refused. It's the short reference.
func (s *Secrets) Store(ctx context.Context, item, field, value string) (string, error) {
	section, label, ok := strings.Cut(field, "/")
	if !ok || section == "" || label == "" || strings.Contains(label, "/") {
		return "", fmt.Errorf("%q isn't where in 1Password it goes: a section and a field, as in GitHub/token", field)
	}
	args, err := itemArgs(item)
	if err != nil {
		return "", err
	}
	res, err := s.run.Run(ctx, runner.Command{Name: "op", Args: slices.Concat([]string{"item", "get"}, args, []string{"--format", "json"}), Timeout: opTimeout, Secret: true})
	if err != nil {
		return "", fmt.Errorf("read the item from 1Password: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(res.Stdout, &doc); err != nil {
		return "", fmt.Errorf("read the item from 1Password: %w", err)
	}
	sections, _ := doc["sections"].([]any)
	sectionID := ""
	for _, sc := range sections {
		if m, _ := sc.(map[string]any); m["label"] == section {
			sectionID, _ = m["id"].(string)
		}
	}
	if sectionID == "" {
		sectionID = newID()
		doc["sections"] = append(sections, map[string]any{"id": sectionID, "label": section})
	}
	fields, _ := doc["fields"].([]any)
	for _, f := range fields {
		m, _ := f.(map[string]any)
		sec, _ := m["section"].(map[string]any)
		if m["label"] == label && sec["id"] == sectionID {
			return "", fmt.Errorf("%s is in 1Password already: change it there, or give it with --ref", field)
		}
	}
	doc["fields"] = append(fields, map[string]any{"id": newID(), "type": "CONCEALED", "label": label, "value": value, "section": map[string]any{"id": sectionID}})
	edited, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	if _, err := s.run.Run(ctx, runner.Command{Name: "op", Args: slices.Concat([]string{"item", "edit"}, args), Input: string(edited), Timeout: opTimeout, Secret: true}); err != nil {
		return "", fmt.Errorf("keep it in 1Password: %w", err)
	}
	return section + "/" + label, nil
}

// Attach keeps the file at path in item (op://vault/item), as an attachment
// named name. It's the short reference.
func (s *Secrets) Attach(ctx context.Context, item, name, path string) (string, error) {
	args, err := itemArgs(item)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	if _, err := s.run.Run(ctx, runner.Command{Name: "op", Args: slices.Concat([]string{"item", "edit"}, args, []string{name + "[file]=" + path}), Timeout: opTimeout, Secret: true}); err != nil {
		return "", fmt.Errorf("attach it in 1Password: %w", err)
	}
	return name, nil
}

// Delete deletes the field or attachment a short reference names from item
// (op://vault/item).
func (s *Secrets) Delete(ctx context.Context, item, short string) error {
	args, err := itemArgs(item)
	if err != nil {
		return err
	}
	field := strings.ReplaceAll(short, "/", ".")
	if _, err := s.run.Run(ctx, runner.Command{Name: "op", Args: slices.Concat([]string{"item", "edit"}, args, []string{field + "[delete]"}), Timeout: opTimeout, Secret: true}); err != nil {
		return fmt.Errorf("delete it from 1Password: %w", err)
	}
	return nil
}

// ReadBack reads ref, in full, from 1Password and compares it with want,
// never showing either.
func (s *Secrets) ReadBack(ctx context.Context, ref, want string) error {
	got, err := s.read(ctx, ref)
	if err != nil {
		return err
	}
	if got != strings.TrimRight(want, "\n") {
		return errors.New("1Password gave back a different value")
	}
	return nil
}

// newID is an id for a new field or section, as 1Password's are: 26
// lower-case letters and digits.
func newID() string {
	b := make([]byte, 13)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
