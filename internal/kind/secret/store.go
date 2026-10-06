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

// item is the vault and the item a reference to s.item names.
func (s *Secrets) itemArgs() ([]string, error) {
	rest, ok := strings.CutPrefix(s.item, "op://")
	vault, item, found := strings.Cut(rest, "/")
	if !ok || !found || vault == "" || item == "" {
		return nil, errors.New("kit.toml's secrets_item names no item to keep it in: give where it is with --ref")
	}
	return []string{item, "--vault", vault}, nil
}

// Short is ref as a secret's line takes it: short when it's in the item
// kit.toml names.
func (s *Secrets) Short(ref string) string {
	if rest, ok := strings.CutPrefix(ref, s.item+"/"); ok && s.item != "" {
		return rest
	}
	return ref
}

// Full is a reference as 1Password takes it: a short one in kit.toml's
// item.
func (s *Secrets) Full(ref string) string {
	if strings.HasPrefix(ref, "op://") {
		return ref
	}
	return s.item + "/" + ref
}

// Store keeps value in kit.toml's item, as the field section/field, a
// concealed one, by editing the item with its JSON on op's standard input,
// so the value is never on a command line. A field there already is
// refused. It's the short reference.
func (s *Secrets) Store(ctx context.Context, field, value string) (string, error) {
	section, label, ok := strings.Cut(field, "/")
	if !ok || section == "" || label == "" || strings.Contains(label, "/") {
		return "", fmt.Errorf("%q isn't where in 1Password it goes: a section and a field, as in GitHub/token", field)
	}
	args, err := s.itemArgs()
	if err != nil {
		return "", err
	}
	res, err := s.run.Run(ctx, runner.Command{Name: "op", Args: slices.Concat([]string{"item", "get"}, args, []string{"--format", "json"}), Timeout: opTimeout, Secret: true})
	if err != nil {
		return "", fmt.Errorf("read the item from 1Password: %w", err)
	}
	var item map[string]any
	if err := json.Unmarshal(res.Stdout, &item); err != nil {
		return "", fmt.Errorf("read the item from 1Password: %w", err)
	}
	sections, _ := item["sections"].([]any)
	sectionID := ""
	for _, sc := range sections {
		if m, _ := sc.(map[string]any); m["label"] == section {
			sectionID, _ = m["id"].(string)
		}
	}
	if sectionID == "" {
		sectionID = newID()
		item["sections"] = append(sections, map[string]any{"id": sectionID, "label": section})
	}
	fields, _ := item["fields"].([]any)
	for _, f := range fields {
		m, _ := f.(map[string]any)
		sec, _ := m["section"].(map[string]any)
		if m["label"] == label && sec["id"] == sectionID {
			return "", fmt.Errorf("%s is in 1Password already: change it there, or give it with --ref", field)
		}
	}
	item["fields"] = append(fields, map[string]any{"id": newID(), "type": "CONCEALED", "label": label, "value": value, "section": map[string]any{"id": sectionID}})
	edited, err := json.Marshal(item)
	if err != nil {
		return "", err
	}
	if _, err := s.run.Run(ctx, runner.Command{Name: "op", Args: slices.Concat([]string{"item", "edit"}, args), Input: string(edited), Timeout: opTimeout, Secret: true}); err != nil {
		return "", fmt.Errorf("keep it in 1Password: %w", err)
	}
	return section + "/" + label, nil
}

// Attach keeps the file at path in kit.toml's item, as an attachment named
// name. It's the short reference.
func (s *Secrets) Attach(ctx context.Context, name, path string) (string, error) {
	args, err := s.itemArgs()
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

// Delete deletes the field or attachment a short reference names from
// kit.toml's item.
func (s *Secrets) Delete(ctx context.Context, short string) error {
	args, err := s.itemArgs()
	if err != nil {
		return err
	}
	field := strings.ReplaceAll(short, "/", ".")
	if _, err := s.run.Run(ctx, runner.Command{Name: "op", Args: slices.Concat([]string{"item", "edit"}, args, []string{field + "[delete]"}), Timeout: opTimeout, Secret: true}); err != nil {
		return fmt.Errorf("delete it from 1Password: %w", err)
	}
	return nil
}

// ReadBack reads ref from 1Password and compares it with want, never
// showing either.
func (s *Secrets) ReadBack(ctx context.Context, ref, want string) error {
	got, err := s.read(ctx, s.Full(ref))
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
