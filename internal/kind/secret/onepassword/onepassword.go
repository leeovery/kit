// Package onepassword is 1Password as a secrets store, driven through its
// CLI, op: a value's reference is op://vault/item/field, an item's
// op://vault/item, and a field kit stores is a section and a field, as in
// GitHub/token. Values pass through op's standard input and output, never
// a command line, and op's output is kept from every report.
package onepassword

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
	"time"

	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner"
)

// Driver is 1Password's driver.
type Driver struct{}

// scheme starts each of 1Password's references.
const scheme = "op://"

// program is 1Password's CLI.
const program = "op"

// opTimeout is how long 1Password may take to answer, or to read a value:
// it has hung after a restart and over remote connections.
const opTimeout = 15 * time.Second

// signInTimeout is how long 1Password's app may wait for a person to
// approve kit's use of it, a fingerprint at the prompt it shows: for each
// run of op that reads a value, as each command kit runs is in a session of
// its own (runner.Exec), and 1Password asks once a session.
const signInTimeout = time.Minute

// about is what kit knows of 1Password. A session signed in with a
// password, over a remote connection, is a variable named for the account.
var about = store.About{
	ID: "1password", Name: "1Password", Program: program,
	Env:       []string{"OP_SESSION_*", "OP_ACCOUNT", "OP_BIOMETRIC_UNLOCK_ENABLED"},
	Ref:       "op://vault/item/field",
	Item:      "op://vault/item",
	Field:     "<section>/<field>, as in GitHub/token",
	NoAnswer:  "1Password doesn't answer: unlock its app, with Settings › Developer › Integrate with 1Password CLI on; over a remote connection, sign in with a password in the shell kit runs from: eval $(OP_BIOMETRIC_UNLOCK_ENABLED=false op signin)",
	SignedOut: "1Password isn't signed in, or its CLI isn't turned on: sign in to its app, then turn on Settings › Developer › Integrate with 1Password CLI",
	SignIn: store.SignIn{
		Title: "Sign in to 1Password",
		Steps: []string{
			"Scan the QR code 1Password shows with 1Password on your phone",
			"Settings › Developer › turn on Integrate with 1Password CLI",
		},
		App: "com.1password.1password", AppName: "1Password",
	},
}

func (Driver) About() store.About { return about }

// path splits ref, after its scheme, into its parts: false when it isn't
// one of 1Password's references.
func path(ref string) ([]string, bool) {
	rest, ok := strings.CutPrefix(ref, scheme)
	if !ok {
		return nil, false
	}
	parts := strings.Split(rest, "/")
	return parts, !slices.Contains(parts, "")
}

// IsRef reports whether ref is a value's, in full: a vault, an item, and a
// field or attachment, a section's field as section/field.
func (Driver) IsRef(ref string) bool {
	parts, ok := path(ref)
	return ok && len(parts) >= 3
}

// IsItem reports whether ref is an item's: a vault and an item.
func (Driver) IsItem(ref string) bool {
	parts, ok := path(ref)
	return ok && len(parts) == 2
}

func (Driver) Join(item, short string) string { return item + "/" + short }

func (Driver) Short(item, ref string) (string, bool) {
	return strings.CutPrefix(ref, item+"/")
}

// Session is open for a session open already (op whoami, which never opens
// one); signed in for an account listed; else signed out, or the CLI isn't
// on in the app.
func (Driver) Session(ctx context.Context, run runner.Runner) store.Session {
	if _, err := run.Run(ctx, runner.Command{Name: program, Args: []string{"whoami"}, Timeout: opTimeout}); err == nil {
		return store.Open
	}
	res, err := run.Run(ctx, runner.Command{Name: program, Args: []string{"account", "list", "--format", "json"}, Timeout: opTimeout})
	var accounts []json.RawMessage
	if err != nil || json.Unmarshal(res.Stdout, &accounts) != nil || len(accounts) == 0 {
		return store.SignedOut
	}
	return store.SignedIn
}

// Unlock has the app open a session, asking for a fingerprint, within a
// minute: listing the vaults needs one.
func (Driver) Unlock(ctx context.Context, run runner.Runner) error {
	_, err := run.Run(ctx, runner.Command{Name: program, Args: []string{"vault", "list", "--format", "json"}, Timeout: signInTimeout})
	return err
}

// Read reads a value with op read, without the newline op ends it with.
func (Driver) Read(ctx context.Context, run runner.Runner, ref string) (string, error) {
	res, err := run.Run(ctx, runner.Command{Name: program, Args: []string{"read", ref}, Timeout: signInTimeout, Secret: true})
	if err != nil {
		return "", errors.New("1Password couldn't read it")
	}
	return strings.TrimRight(string(res.Stdout), "\n"), nil
}

// ReadMany reads refs in one run of op inject, as 1Password has the person
// approve each run: a template on its standard input, each ref between
// lines of a mark no value holds, so that a value of many lines reads back
// whole, and each value without the newlines it ends with, as Read has it.
// A ref with a $ or }} in it isn't taken: a template would read it as a
// variable, or as the reference's end.
func (Driver) ReadMany(ctx context.Context, run runner.Runner, refs []string) (map[string]string, error) {
	mark := "kit-" + newID()
	var template strings.Builder
	for _, ref := range refs {
		if strings.Contains(ref, "$") || strings.Contains(ref, "}}") {
			return nil, fmt.Errorf("op inject would read %s otherwise", ref)
		}
		template.WriteString(mark + "\n{{ " + ref + " }}\n")
	}
	template.WriteString(mark + "\n")
	res, err := run.Run(ctx, runner.Command{Name: program, Args: []string{"inject"}, Input: template.String(), Timeout: signInTimeout, Secret: true})
	if err != nil {
		return nil, errors.New("1Password couldn't read them")
	}
	parts := strings.Split(string(res.Stdout), mark)
	if len(parts) != len(refs)+2 {
		return nil, errors.New("op inject gave back other than its template")
	}
	values := make(map[string]string, len(refs))
	for i, ref := range refs {
		values[ref] = strings.TrimRight(strings.TrimPrefix(parts[i+1], "\n"), "\n")
	}
	return values, nil
}

// itemArgs are op's arguments naming item (op://vault/item): the item, and
// its vault.
func itemArgs(item string) ([]string, error) {
	parts, ok := path(item)
	if !ok || len(parts) != 2 {
		return nil, fmt.Errorf("%q isn't a 1Password item's reference, as in %s", item, about.Item)
	}
	return []string{parts[1], "--vault", parts[0]}, nil
}

// Store keeps value in item as the field section/field, a concealed one,
// by editing the item with its JSON on op's standard input, so the value is
// never on a command line. A field there already is refused.
func (d Driver) Store(ctx context.Context, run runner.Runner, item, field, value string) (string, error) {
	short, err := d.Field(field)
	if err != nil {
		return "", err
	}
	section, label, _ := strings.Cut(short, "/")
	args, doc, err := getItem(ctx, run, item)
	if err != nil {
		return "", err
	}
	if holds(doc, short) {
		return "", taken(short)
	}
	sections, _ := doc["sections"].([]any)
	sectionID := sectionOf(doc, section)
	if sectionID == "" {
		sectionID = newID()
		doc["sections"] = append(sections, map[string]any{"id": sectionID, "label": section})
	}
	fields, _ := doc["fields"].([]any)
	doc["fields"] = append(fields, map[string]any{"id": newID(), "type": "CONCEALED", "label": label, "value": value, "section": map[string]any{"id": sectionID}})
	edited, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	if _, err := run.Run(ctx, runner.Command{Name: program, Args: slices.Concat([]string{"item", "edit"}, args), Input: string(edited), Timeout: opTimeout, Secret: true}); err != nil {
		return "", fmt.Errorf("keep it in 1Password: %w", err)
	}
	return short, nil
}

// Free is nil when item keeps nothing at short, a field (section/field) or
// an attachment: else that it's there already.
func (Driver) Free(ctx context.Context, run runner.Runner, item, short string) error {
	_, doc, err := getItem(ctx, run, item)
	if err != nil {
		return err
	}
	if holds(doc, short) {
		return taken(short)
	}
	return nil
}

// taken is why a value can't go at short: one's there already.
func taken(short string) error {
	return fmt.Errorf("%s is in 1Password already: change it there, or give it with --ref", short)
}

// getItem is item's arguments to op, and its JSON, as op item get has it.
func getItem(ctx context.Context, run runner.Runner, item string) ([]string, map[string]any, error) {
	args, err := itemArgs(item)
	if err != nil {
		return nil, nil, err
	}
	res, err := run.Run(ctx, runner.Command{Name: program, Args: slices.Concat([]string{"item", "get"}, args, []string{"--format", "json"}), Timeout: opTimeout, Secret: true})
	if err != nil {
		return nil, nil, fmt.Errorf("read the item from 1Password: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(res.Stdout, &doc); err != nil {
		return nil, nil, fmt.Errorf("read the item from 1Password: %w", err)
	}
	return args, doc, nil
}

// sectionOf is the id of the item's section labelled section, the last
// when several are: "" for none.
func sectionOf(doc map[string]any, section string) string {
	sections, _ := doc["sections"].([]any)
	id := ""
	for _, sc := range sections {
		if m, _ := sc.(map[string]any); m["label"] == section {
			id, _ = m["id"].(string)
		}
	}
	return id
}

// holds reports whether the item keeps something at short: a field of its
// section, as in GitHub/token, or an attachment, by its name.
func holds(doc map[string]any, short string) bool {
	section, label, isField := strings.Cut(short, "/")
	if !isField {
		files, _ := doc["files"].([]any)
		return slices.ContainsFunc(files, func(f any) bool { m, _ := f.(map[string]any); return m["name"] == short })
	}
	id := sectionOf(doc, section)
	fields, _ := doc["fields"].([]any)
	return id != "" && slices.ContainsFunc(fields, func(f any) bool {
		m, _ := f.(map[string]any)
		sec, _ := m["section"].(map[string]any)
		return m["label"] == label && sec["id"] == id
	})
}

// Field is field, a section's name and a field's, as in GitHub/token.
func (Driver) Field(field string) (string, error) {
	section, label, ok := strings.Cut(field, "/")
	if !ok || section == "" || label == "" || strings.Contains(label, "/") {
		return "", fmt.Errorf("%q isn't where in 1Password it goes: %s", field, about.Field)
	}
	return field, nil
}

// Attachment is name, an attachment's, with no /.
func (Driver) Attachment(name string) (string, error) {
	if name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("%q isn't an attachment's name: no /", name)
	}
	return name, nil
}

// assigned is a field's or attachment's name as op's assignments take it:
// each period, equals sign and backslash escaped, as op would read a period
// as a section's end.
func assigned(name string) string {
	return strings.NewReplacer(`\`, `\\`, ".", `\.`, "=", `\=`).Replace(name)
}

// Attach keeps the file at path in item as an attachment named name.
func (d Driver) Attach(ctx context.Context, run runner.Runner, item, name, path string) (string, error) {
	if _, err := d.Attachment(name); err != nil {
		return "", err
	}
	args, err := itemArgs(item)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	if _, err := run.Run(ctx, runner.Command{Name: program, Args: slices.Concat([]string{"item", "edit"}, args, []string{assigned(name) + "[file]=" + path}), Timeout: opTimeout, Secret: true}); err != nil {
		return "", fmt.Errorf("attach it in 1Password: %w", err)
	}
	return name, nil
}

// Delete deletes the field or attachment short names from item.
func (Driver) Delete(ctx context.Context, run runner.Runner, item, short string) error {
	args, err := itemArgs(item)
	if err != nil {
		return err
	}
	// A section's name, then a field's, as section.field; an attachment's
	// alone.
	parts := strings.Split(short, "/")
	for i, part := range parts {
		parts[i] = assigned(part)
	}
	if _, err := run.Run(ctx, runner.Command{Name: program, Args: slices.Concat([]string{"item", "edit"}, args, []string{strings.Join(parts, ".") + "[delete]"}), Timeout: opTimeout, Secret: true}); err != nil {
		return fmt.Errorf("delete it from 1Password: %w", err)
	}
	return nil
}

// newID is an id for a new field or section, as 1Password's are, or a
// template's mark: 26 lower-case letters and digits, at random.
func newID() string {
	b := make([]byte, 13)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var _ store.Driver = Driver{}
