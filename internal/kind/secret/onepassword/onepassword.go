// Package onepassword is 1Password as a secrets store, driven through its
// CLI, op: a value's reference is op://vault/item/field, an item's
// op://vault/item, and a field kit stores is a section and a field, as in
// GitHub/token. Values pass through op's standard input and output, never
// a command line, and op's output is kept from every report.
package onepassword

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"os"
	"path/filepath"
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

// opTimeout is how long 1Password may take to answer what needs no
// approval (who's signed in, the accounts): it has hung after a restart and
// over remote connections.
const opTimeout = 15 * time.Second

// approvalTimeout ends a run of op that touches an item, reading, getting or
// editing it, only once it's hung: 1Password's app may have the person
// approve it, a fingerprint at the prompt it shows, once the last approval
// has lapsed, and the prompt can come late on a busy Mac. Ended sooner, the
// run is lost to a person still answering (9 Nov: a delete gave up at 15s).
const approvalTimeout = 10 * time.Minute

// opRun runs op as c says, in kit's session (runner.Command.SharesSession):
// 1Password's app asks once a terminal, its approval lasting ten minutes,
// and a run in a session of its own, without the terminal, is asked again
// each time, six times for one kit secret add (5 Nov 2026). Signed in with
// a password, there's no approval to share, and op, its session over, might
// ask for the password at the terminal: it runs without it. op alone gets
// kit's own variables of op's, a password's session or a service account's
// token among them: kit runs every other program without them.
func opRun(ctx context.Context, run runner.Runner, c runner.Command) (runner.Result, error) {
	c.Name = program
	c.SharesSession = !passworded()
	c.Env = slices.Concat(opEnv(), c.Env)
	return run.Run(ctx, c)
}

// opEnv are kit's own variables of op's, each as name=value.
func opEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool { return !strings.HasPrefix(kv, "OP_") })
}

// passworded is whether op is signed in with a password, as over a remote
// connection: the app's approval turned off, or a session's variable set.
func passworded() bool {
	return os.Getenv("OP_BIOMETRIC_UNLOCK_ENABLED") == "false" ||
		slices.ContainsFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "OP_SESSION_") })
}

// about is what kit knows of 1Password.
var about = store.About{
	ID: "1password", Name: "1Password", Program: program,
	Ref:       "op://vault/item/field",
	Item:      "op://vault/item",
	Field:     "<section>/<field>, as in GitHub/token",
	NoAnswer:  "unlock its app, with Settings › Developer › Integrate with 1Password CLI on; over a remote connection, sign in with a password in the shell kit runs from: eval $(OP_BIOMETRIC_UNLOCK_ENABLED=false op signin)",
	SignedOut: "1Password isn't signed in, or its CLI isn't turned on: sign in to its app, then turn on Settings › Developer › Integrate with 1Password CLI; if kit still waits with both done, quit 1Password and open it again",
	SignIn: store.SignIn{
		Title: "Sign in to 1Password",
		Steps: []string{
			"Scan the QR code 1Password shows with 1Password on your phone",
			"Settings › Developer › turn on Integrate with 1Password CLI",
			// The app can stop answering its CLI, as under heavy load, till
			// it's opened again; op says the same as with the CLI off, so
			// it's a step for everyone.
			"If kit still waits with both done:",
			"Quit 1Password and open it again",
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
	if _, err := opRun(ctx, run, runner.Command{Args: []string{"whoami"}, Timeout: opTimeout}); err == nil {
		return store.Open
	}
	res, err := opRun(ctx, run, runner.Command{Args: []string{"account", "list", "--format", "json"}, Timeout: opTimeout})
	var accounts []json.RawMessage
	if err != nil || json.Unmarshal(res.Stdout, &accounts) != nil || len(accounts) == 0 {
		return store.SignedOut
	}
	return store.SignedIn
}

// Unlock has the app open a session, asking for a fingerprint: listing the
// vaults needs one.
func (Driver) Unlock(ctx context.Context, run runner.Runner) error {
	_, err := opRun(ctx, run, runner.Command{Args: []string{"vault", "list", "--format", "json"}, Timeout: approvalTimeout})
	return err
}

// Read reads a value with op read, through a pipe, without the newline op
// ends it with: store.Unread when op names ref in saying why it couldn't.
// op adds that newline only to a value that doesn't end with one already
// (tried, 9 Nov 2026), so a value that does reads without its own: through
// the pipe, "a" and "a\n" are one. A file's bytes read whole only with
// --out-file (ReadFile).
func (Driver) Read(ctx context.Context, run runner.Runner, ref string) (string, error) {
	res, err := opRun(ctx, run, runner.Command{Args: []string{"read", ref}, Timeout: approvalTimeout, Secret: true})
	if err != nil {
		return "", unread(err, []string{ref})
	}
	return strings.TrimSuffix(string(res.Stdout), "\n"), nil
}

// ReadFile reads a value as a file holds it, never with op inject, which
// turns each byte that isn't UTF-8 into U+FFFD: an attachment's bytes
// exactly, with op read --out-file; a field's text, with Read, ended by one
// newline in place of any it ends with. Read first, so op names ref should
// it not read; then the item says whether it's an attachment.
func (d Driver) ReadFile(ctx context.Context, run runner.Runner, ref string) (string, error) {
	value, err := d.Read(ctx, run, ref)
	if err != nil {
		return "", err
	}
	attached, err := isAttachment(ctx, run, ref)
	switch {
	case err != nil:
		return "", err
	case attached:
		return readOut(ctx, run, ref)
	}
	return strings.TrimRight(value, "\n") + "\n", nil
}

// readOut reads ref's bytes exactly with op read --out-file, into a folder
// only the person can read, made for it and removed after.
func readOut(ctx context.Context, run runner.Runner, ref string) (string, error) {
	dir, err := os.MkdirTemp("", "kit-secret-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	out := filepath.Join(dir, "value")
	args := []string{"read", "--force", "--file-mode", "0600", "--out-file", out, ref}
	if _, err := opRun(ctx, run, runner.Command{Args: args, Timeout: approvalTimeout, Secret: true}); err != nil {
		return "", unread(err, []string{ref})
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return "", fmt.Errorf("op read wrote no file: %w", err)
	}
	return string(data), nil
}

// isAttachment reports whether ref, a value's, is one of its item's
// attachments, as op item get lists them and op read finds them (has):
// store.Unread when it finds a field there too, as which op reads is
// unknown.
func isAttachment(ctx context.Context, run runner.Runner, ref string) (bool, error) {
	parts, _ := path(ref)
	if len(parts) != 3 && len(parts) != 4 {
		return false, nil
	}
	item := scheme + parts[0] + "/" + parts[1]
	_, doc, err := getItem(ctx, run, item)
	if err != nil {
		return false, err
	}
	short := strings.Join(parts[2:], "/")
	file := hasFile(doc, short)
	if file && hasField(doc, short) {
		return false, store.Unread{ref: fmt.Sprintf("%s names both a field and a file in %s: give its section, or rename one", ref, item)}
	}
	return file, nil
}

// ReadMany reads refs in one run of op inject, as 1Password may have the
// person approve each run: a template on its standard input, each ref between
// lines of a mark no value holds, so that a value of many lines reads back
// whole, each exactly as op inject gives it. op inject is a text
// template, so a value that isn't UTF-8, as a file may be, doesn't read
// back whole: ReadFile reads those. A ref with a $ or }} in it is read
// alone, with Read, as Read gives it, its final newline lost: a template
// would read it as a variable, or as the reference's end. It fails with store.Unread when op names refs in saying
// why it couldn't, the values read beside them given back.
func (d Driver) ReadMany(ctx context.Context, run runner.Runner, refs []string) (map[string]string, error) {
	plain, awkward := split(refs)
	values, err := inject(ctx, run, plain)
	if err != nil {
		return nil, err
	}
	named := store.Unread{}
	for _, ref := range awkward {
		v, err := d.Read(ctx, run, ref)
		bad, isUnread := errors.AsType[store.Unread](err)
		switch {
		case err == nil:
			values[ref] = v
		case !isUnread:
			return nil, err
		default:
			maps.Copy(named, bad)
		}
	}
	if len(named) > 0 {
		return values, named
	}
	return values, nil
}

// split is refs a template can take, and those it can't, with a $ or }}.
func split(refs []string) (plain, awkward []string) {
	for _, ref := range refs {
		if strings.Contains(ref, "$") || strings.Contains(ref, "}}") {
			awkward = append(awkward, ref)
		} else {
			plain = append(plain, ref)
		}
	}
	return plain, awkward
}

// inject reads refs in one run of op inject, as ReadMany says: none, given
// none.
func inject(ctx context.Context, run runner.Runner, refs []string) (map[string]string, error) {
	values := make(map[string]string, len(refs))
	if len(refs) == 0 {
		return values, nil
	}
	mark := "kit-" + newID()
	var template strings.Builder
	for _, ref := range refs {
		template.WriteString(mark + "\n{{ " + ref + " }}\n")
	}
	template.WriteString(mark + "\n")
	res, err := opRun(ctx, run, runner.Command{Args: []string{"inject"}, Input: template.String(), Timeout: approvalTimeout, Secret: true})
	if err != nil {
		return nil, unread(err, refs)
	}
	parts := strings.Split(string(res.Stdout), mark)
	if len(parts) != len(refs)+2 {
		return nil, errors.New("op inject gave back other than its template")
	}
	for i, ref := range refs {
		values[ref] = strings.TrimSuffix(strings.TrimPrefix(parts[i+1], "\n"), "\n")
	}
	return values, nil
}

// unread is why a read of refs failed with err: store.Unread when op, having
// run, quoted some of them in its errors, each with the line it's quoted
// in; else what op said last, or how it ended. A ref is quoted
// where it's the longest of refs at that place, so that op quoting
// op://vault/item/token2 doesn't name op://vault/item/token; a line about
// the person's approval, or the session, names none, as op quotes the
// reference it was reading in saying it wasn't approved.
func unread(err error, refs []string) error {
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok {
		return errors.New("op didn't finish")
	}
	longest := slices.SortedFunc(slices.Values(refs), func(a, b string) int { return len(b) - len(a) })
	named := store.Unread{}
	for line := range strings.Lines(exit.FullStderr) {
		line = strings.TrimSpace(line)
		if aboutApproval(line) {
			continue
		}
		quoted := map[int]bool{}
		for _, ref := range longest {
			for at := range quotes(line, ref) {
				if !quoted[at] {
					quoted[at] = true
					named[ref] = line
				}
			}
		}
	}
	if len(named) == 0 {
		return errors.New(cmp.Or(exit.Stderr, fmt.Sprintf("op exited %d", exit.Code)))
	}
	return named
}

// approval are words of op's errors about the person's approval, or the
// session, rather than a reference: matched in any case.
var approval = []string{"initializing client", "authoriz", "dismissed", "sign in", "signed in", "session", "timeout", "timed out"}

// aboutApproval reports whether line, one of op's errors, is about the
// person's approval, or the session.
func aboutApproval(line string) bool {
	line = strings.ToLower(line)
	return slices.ContainsFunc(approval, func(w string) bool { return strings.Contains(line, w) })
}

// quotes are where ref is in line, each place it starts.
func quotes(line, ref string) iter.Seq[int] {
	return func(yield func(int) bool) {
		for from := 0; ; {
			i := strings.Index(line[from:], ref)
			if i < 0 || !yield(from+i) {
				return
			}
			from += i + 1
		}
	}
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
	if _, err := opRun(ctx, run, runner.Command{Args: slices.Concat([]string{"item", "edit"}, args), Input: string(edited), Timeout: approvalTimeout, Secret: true}); err != nil {
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
	res, err := opRun(ctx, run, runner.Command{Args: slices.Concat([]string{"item", "get"}, args, []string{"--format", "json"}), Timeout: approvalTimeout, Secret: true})
	if err != nil {
		return nil, nil, fmt.Errorf("read the item from 1Password: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(res.Stdout, &doc); err != nil {
		return nil, nil, fmt.Errorf("read the item from 1Password: %w", err)
	}
	return args, doc, nil
}

// sectionOf is the id of the item's section called section, by its label
// or id, in any case, the last when several are: "" for none.
func sectionOf(doc map[string]any, section string) string {
	sections, _ := doc["sections"].([]any)
	id := ""
	for _, sc := range sections {
		if m, _ := sc.(map[string]any); names(m, "label", section) {
			id, _ = m["id"].(string)
		}
	}
	return id
}

// holds reports whether op read would find something of the item at
// short: a field or an attachment (has).
func holds(doc map[string]any, short string) bool {
	return hasFile(doc, short) || hasField(doc, short)
}

// hasFile reports whether op read would find an attachment of the item at
// short (has).
func hasFile(doc map[string]any, short string) bool { return has(doc, "files", "name", short) }

// hasField reports whether op read would find a field of the item at short
// (has).
func hasField(doc map[string]any, short string) bool { return has(doc, "fields", "label", short) }

// has reports whether the item's list (its files, or its fields), each
// called by key, has one at short as op read finds them (tried, 9 Nov
// 2026): a name alone finds one in any section, or none; a section's name
// and its, as in GitHub/key.pem, one in that section alone; each part a
// name or an id, in any case.
func has(doc map[string]any, list, key, short string) bool {
	section, name, inSection := strings.Cut(short, "/")
	if !inSection {
		name = short
	}
	items, _ := doc[list].([]any)
	return slices.ContainsFunc(items, func(it any) bool {
		m, _ := it.(map[string]any)
		return names(m, key, name) && (!inSection || inSectionCalled(doc, m, section))
	})
}

// inSectionCalled reports whether m, a field or file of the item, is in its
// section called section, by its label or id, in any case: as m names the
// section, or the item's sections do.
func inSectionCalled(doc, m map[string]any, section string) bool {
	sec, _ := m["section"].(map[string]any)
	if names(sec, "label", section) {
		return true
	}
	id, _ := sec["id"].(string)
	return id != "" && sectionOf(doc, section) == id
}

// names reports whether m, a field, file or section of an item, is called
// name: by its key (a label, a name) or its id, in any case.
func names(m map[string]any, key, name string) bool {
	called, _ := m[key].(string)
	id, _ := m["id"].(string)
	return strings.EqualFold(called, name) || id != "" && strings.EqualFold(id, name)
}

// Field is field, a section's name and a field's, as in GitHub/token.
func (Driver) Field(field string) (string, error) {
	section, label, ok := strings.Cut(field, "/")
	if !ok || section == "" || label == "" || strings.Contains(label, "/") {
		return "", fmt.Errorf("%q isn't where in 1Password it goes: %s", field, about.Field)
	}
	return field, nil
}

// Attachment is name, an attachment's: its name alone, with no /, or a
// section's name and its, as a field's are.
func (d Driver) Attachment(name string) (string, error) {
	if _, err := d.Field(name); err == nil || name != "" && !strings.Contains(name, "/") {
		return name, nil
	}
	return "", fmt.Errorf("%q isn't an attachment's name: a name with no /, or a section's and a name, as in GitHub/key.pem", name)
}

// assigned is a field's or attachment's name as op's assignments take it:
// each period, equals sign and backslash escaped, as op would read a period
// as a section's end.
func assigned(name string) string {
	return strings.NewReplacer(`\`, `\\`, ".", `\.`, "=", `\=`).Replace(name)
}

// assignment is short, a field or attachment of an item, as op's
// assignments name it: a section's name, then its own, as section.name; an
// item's own alone.
func assignment(short string) string {
	parts := strings.Split(short, "/")
	for i, part := range parts {
		parts[i] = assigned(part)
	}
	return strings.Join(parts, ".")
}

// Attach keeps the file at path in item as an attachment named name, in a
// section when name gives one (Attachment).
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
	if _, err := opRun(ctx, run, runner.Command{Args: slices.Concat([]string{"item", "edit"}, args, []string{assignment(name) + "[file]=" + path}), Timeout: approvalTimeout, Secret: true}); err != nil {
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
	if _, err := opRun(ctx, run, runner.Command{Args: slices.Concat([]string{"item", "edit"}, args, []string{assignment(short) + "[delete]"}), Timeout: approvalTimeout, Secret: true}); err != nil {
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
