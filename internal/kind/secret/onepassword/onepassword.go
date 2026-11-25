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

// op runs op as c says (command).
func (Driver) op(ctx context.Context, run runner.Runner, c runner.Command) (runner.Result, error) {
	return run.Run(ctx, command(c))
}

// command is op run as c says, in kit's session (runner.Command's
// SharesSession): 1Password's app asks once a terminal, its approval
// lasting ten minutes, and a run in a session of its own, without the
// terminal, is asked again each time, six times for one kit secret add (5
// Nov 2026). Signed in with a password, there's no approval to share, and
// op, its session over, might ask for the password at the terminal: it runs
// without it. op alone gets kit's own variables of op's, a password's
// session or a service account's token among them: kit runs every other
// program without them.
func command(c runner.Command) runner.Command {
	c.Name = program
	c.SharesSession = !passworded()
	c.Env = slices.Concat(env(), c.Env)
	return c
}

// biometric turns 1Password's app approval on or off for op.
const biometric = "OP_BIOMETRIC_UNLOCK_ENABLED"

// opVars are kit's own variables of op's, each as name=value.
func opVars() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool { return !strings.HasPrefix(kv, "OP_") })
}

// env is op's environment: kit's own variables of op's, the app's approval
// off with a session's variable, unless kit's own say otherwise. A
// session's variable alone doesn't turn it off: op asks the app, and
// ignores the session.
func env() []string {
	env := opVars()
	if _, set := os.LookupEnv(biometric); !set && inSession() {
		env = append(env, biometric+"=false")
	}
	return env
}

// passworded is whether op is signed in with a password, as over a remote
// connection: the app's approval turned off, or a session's variable set.
func passworded() bool {
	return os.Getenv(biometric) == "false" || inSession()
}

// inSession is whether a password's session variable is set.
func inSession() bool {
	return slices.ContainsFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "OP_SESSION_") })
}

// about is what kit knows of 1Password.
var about = store.About{
	ID: "1password", Name: "1Password", Program: program,
	Ref:       "op://vault/item/field",
	Item:      "op://vault/item",
	Field:     "<section>/<field>, as in GitHub/token",
	NoAnswer:  "unlock its app, with Settings › Developer › Integrate with 1Password CLI on; away from the Mac, as over a remote connection, " + withPassword,
	SignedOut: "1Password isn't signed in, or its CLI isn't turned on: sign in to its app, then turn on Settings › Developer › Integrate with 1Password CLI; if kit still waits with both done, quit 1Password and open it again",
	// The app's prompt takes a fingerprint, on the Mac alone.
	AtScreen:   true,
	WhenLocked: withPassword,
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
		Packages: []string{"cask:1password", "cask:1password-cli"},
	},
}

// withPassword is how to have 1Password answer without its app's approval.
const withPassword = "sign in with your 1Password password in a terminal of your own, and leave it open: kit secret signin"

func (Driver) About() store.About { return about }

// SignIn signs in with the person's password, op asking for it at kit's
// terminal, the app's approval off: the session's variable op prints for a
// shell to set, which names the account it's for, as Apply sets it. op
// prints it in the shell's own words, as fish's set -x, so it's told the
// shell is sh; and it prints none while a session it's given is open, so
// it's given none.
func (Driver) SignIn(ctx context.Context, run runner.Runner) (string, error) {
	env := slices.DeleteFunc(opVars(), func(kv string) bool { return strings.HasPrefix(kv, "OP_SESSION_") })
	env = append(env, biometric+"=false", "SHELL=/bin/sh")
	res, err := run.Run(ctx, runner.Command{Name: program, Args: []string{"signin"}, Env: env, Terminal: true, Secret: true, Timeout: approvalTimeout})
	if err != nil {
		return "", fmt.Errorf("1Password didn't sign you in: %w", err)
	}
	session, ok := sessionOf(res.Stdout)
	if !ok {
		return "", errors.New("1Password signed you in, but op gave no session")
	}
	return session, nil
}

// sessionOf is the session's variable op signin printed, as a shell would
// set it, as NAME=token: export OP_SESSION_<account>="<token>", and lines
// of advice after it, which a shell takes as comments.
func sessionOf(out []byte) (string, bool) {
	for line := range strings.Lines(string(out)) {
		assignment, ok := strings.CutPrefix(strings.TrimSpace(line), "export ")
		name, token, _ := strings.Cut(assignment, "=")
		if token = strings.Trim(token, `"'`); ok && strings.HasPrefix(name, "OP_SESSION_") && token != "" {
			return name + "=" + token, true
		}
	}
	return "", false
}

// Apply is c, a run of op, in session, as SignIn gave it: the session's
// variable, over any of kit's own, and the app's approval off, as op asks
// the app and ignores the session otherwise; in a session of its own, as
// there's no approval to share.
func (Driver) Apply(session string, c runner.Command) runner.Command {
	c.Env = slices.Concat(c.Env, []string{session, biometric + "=false"})
	c.SharesSession = false
	return c
}

// inSession runs op as c says, in session.
func (d Driver) inSession(ctx context.Context, run runner.Runner, session string, c runner.Command) (runner.Result, error) {
	return run.Run(ctx, d.Apply(session, command(c)))
}

// Lasts reports whether session is open, as op whoami, run in it, says:
// op exiting otherwise than 0 says it's over, as with the app's approval
// off it asks nothing else; op not finishing says nothing.
func (d Driver) Lasts(ctx context.Context, run runner.Runner, session string) (bool, error) {
	_, err := d.inSession(ctx, run, session, runner.Command{Args: []string{"whoami"}, Timeout: opTimeout})
	if _, over := errors.AsType[*runner.ExitError](err); err != nil && !over {
		return false, fmt.Errorf("op didn't answer: %w", err)
	}
	return err == nil, nil
}

// SignOut ends session with op signout, run in it.
func (d Driver) SignOut(ctx context.Context, run runner.Runner, session string) error {
	if _, err := d.inSession(ctx, run, session, runner.Command{Args: []string{"signout"}, Timeout: opTimeout}); err != nil {
		return fmt.Errorf("1Password didn't sign you out: %w", err)
	}
	return nil
}

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
func (d Driver) Session(ctx context.Context, run runner.Runner) store.Session {
	if _, err := d.op(ctx, run, runner.Command{Args: []string{"whoami"}, Timeout: opTimeout}); err == nil {
		return store.Open
	}
	res, err := d.op(ctx, run, runner.Command{Args: []string{"account", "list", "--format", "json"}, Timeout: opTimeout})
	var accounts []json.RawMessage
	if err != nil || json.Unmarshal(res.Stdout, &accounts) != nil || len(accounts) == 0 {
		return store.SignedOut
	}
	return store.SignedIn
}

// Unlock has the app open a session, asking for a fingerprint: listing the
// vaults needs one.
func (d Driver) Unlock(ctx context.Context, run runner.Runner) error {
	_, err := d.op(ctx, run, runner.Command{Args: []string{"vault", "list", "--format", "json"}, Timeout: approvalTimeout})
	return err
}

// Read reads a value with op read, through a pipe, without the newline op
// ends it with: store.Unread when op names ref in saying why it couldn't.
// op adds that newline only to a value that doesn't end with one already
// (tried, 9 Nov 2026), so a value that does reads without its own: through
// the pipe, "a" and "a\n" are one. A file's bytes read whole only with
// --out-file (ReadFile).
func (d Driver) Read(ctx context.Context, run runner.Runner, ref string) (string, error) {
	res, err := d.op(ctx, run, runner.Command{Args: []string{"read", ref}, Timeout: approvalTimeout, Secret: true})
	if err != nil {
		return "", unread(err, ref)
	}
	return strings.TrimSuffix(string(res.Stdout), "\n"), nil
}

// ReadFile reads a value as a file holds it: an attachment's bytes
// exactly, with op read --out-file; a field's text, with Read, ended by one
// newline in place of any it ends with. Read first, so op names ref should
// it not read; then the item says whether it's an attachment.
func (d Driver) ReadFile(ctx context.Context, run runner.Runner, ref string) (string, error) {
	value, err := d.Read(ctx, run, ref)
	if err != nil {
		return "", err
	}
	attached, err := d.isAttachment(ctx, run, ref)
	switch {
	case err != nil:
		return "", err
	case attached:
		return d.readOut(ctx, run, ref)
	}
	return strings.TrimRight(value, "\n") + "\n", nil
}

// readOut reads ref's bytes exactly with op read --out-file, into a folder
// only the person can read, made for it and removed after.
func (d Driver) readOut(ctx context.Context, run runner.Runner, ref string) (string, error) {
	dir, err := os.MkdirTemp("", "kit-secret-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	out := filepath.Join(dir, "value")
	args := []string{"read", "--force", "--file-mode", "0600", "--out-file", out, ref}
	if _, err := d.op(ctx, run, runner.Command{Args: args, Timeout: approvalTimeout, Secret: true}); err != nil {
		return "", unread(err, ref)
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
func (d Driver) isAttachment(ctx context.Context, run runner.Runner, ref string) (bool, error) {
	parts, _ := path(ref)
	if len(parts) != 3 && len(parts) != 4 {
		return false, nil
	}
	item := scheme + parts[0] + "/" + parts[1]
	_, doc, err := d.getItem(ctx, run, item)
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

// unread is why a read of ref failed with err: store.Unread when op, having
// run, quoted it in its errors, with the line it's quoted in; else what op
// said last, or how it ended. A line about the person's approval, or the
// session, names none, as op quotes the reference it was reading in saying
// it wasn't approved.
func unread(err error, ref string) error {
	exit, ok := errors.AsType[*runner.ExitError](err)
	if !ok {
		return errors.New("op didn't finish")
	}
	for line := range strings.Lines(exit.FullStderr) {
		if line = strings.TrimSpace(line); strings.Contains(line, ref) && !aboutApproval(line) {
			return store.Unread{ref: line}
		}
	}
	return errors.New(cmp.Or(exit.Stderr, fmt.Sprintf("op exited %d", exit.Code)))
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
	args, doc, err := d.getItem(ctx, run, item)
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
	if _, err := d.op(ctx, run, runner.Command{Args: slices.Concat([]string{"item", "edit"}, args), Input: string(edited), Timeout: approvalTimeout, Secret: true}); err != nil {
		return "", fmt.Errorf("keep it in 1Password: %w", err)
	}
	return short, nil
}

// Free is nil when item keeps nothing at short, a field (section/field) or
// an attachment: else that it's there already.
func (d Driver) Free(ctx context.Context, run runner.Runner, item, short string) error {
	_, doc, err := d.getItem(ctx, run, item)
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
func (d Driver) getItem(ctx context.Context, run runner.Runner, item string) ([]string, map[string]any, error) {
	args, err := itemArgs(item)
	if err != nil {
		return nil, nil, err
	}
	res, err := d.op(ctx, run, runner.Command{Args: slices.Concat([]string{"item", "get"}, args, []string{"--format", "json"}), Timeout: approvalTimeout, Secret: true})
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

// Section is the section short is in, as in GitHub for GitHub/token: none
// for a name alone, found in any section.
func (Driver) Section(short string) (string, bool) {
	section, _, ok := strings.Cut(short, "/")
	if !ok || section == "" {
		return "", false
	}
	return section, true
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
	if _, err := d.op(ctx, run, runner.Command{Args: slices.Concat([]string{"item", "edit"}, args, []string{assignment(name) + "[file]=" + path}), Timeout: approvalTimeout, Secret: true}); err != nil {
		return "", fmt.Errorf("attach it in 1Password: %w", err)
	}
	return name, nil
}

// Delete deletes the field or attachment short names from item.
func (d Driver) Delete(ctx context.Context, run runner.Runner, item, short string) error {
	args, err := itemArgs(item)
	if err != nil {
		return err
	}
	if _, err := d.op(ctx, run, runner.Command{Args: slices.Concat([]string{"item", "edit"}, args, []string{assignment(short) + "[delete]"}), Timeout: approvalTimeout, Secret: true}); err != nil {
		return fmt.Errorf("delete it from 1Password: %w", err)
	}
	return nil
}

// newID is an id for a new field or section, as 1Password's are: 26
// lower-case letters and digits, at random.
func newID() string {
	b := make([]byte, 13)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var _ store.Passworded = Driver{}
