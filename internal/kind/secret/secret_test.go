package secret_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kind/secret/onepassword"
	"github.com/leeovery/kit/internal/kind/secret/onepassword/optest"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const item = "op://vault/Item"

// onePassword is what kit knows of 1Password.
var onePassword = onepassword.Driver{}.About()

// declare declares each line's secret: one in full in the plain section,
// quoted or not, the rest in item's.
func declare(t *testing.T, s *secret.Secrets, lines ...[2]string) config.List {
	t.Helper()
	var list config.List
	for i, l := range lines {
		value, note, _ := strings.Cut(l[1], "   # ")
		e := config.Entry{Name: l[0], Value: value, Note: note, Scope: "shared", Line: i + 2, Section: "secrets"}
		if !strings.HasPrefix(strings.TrimPrefix(value, "'"), "op://") {
			e.Item, e.Section = item, "secrets "+item
		}
		list.Entries = append(list.Entries, e)
	}
	if _, err := s.Values(list); err != nil {
		t.Fatal(err)
	}
	return list
}

func TestLinesRead(t *testing.T) {
	s := secret.New(runnertest.New(t), t.TempDir())
	for _, c := range []struct{ item, value, want string }{
		{"", "GitHub/token", `"GitHub/token" isn't a reference kit knows: in a plain [secrets], give where its store keeps it in full (1Password's, as in op://vault/item/field), or put it in its item's section`},
		{item, "op://vault/a/b", "op://vault/a/b is in full: in [secrets " + item + "] a line names a field or attachment of its item"},
		{"", "op://vault/a/b --mode", "--mode needs its value"},
		{item, "a/b --mode 9", "--mode 9 isn't a file's permissions"},
		{"", "op://vault/a/b --github x/y", "--github isn't an option"},
	} {
		e := config.Entry{Name: "~/.npmrc", Value: c.value, Scope: "shared", Line: 2, Item: c.item, Section: strings.TrimSpace("secrets " + c.item)}
		_, err := s.Values(config.List{Entries: []config.Entry{e}})
		if err == nil || !strings.Contains(err.Error(), "shared/declarations:2: ~/.npmrc: "+c.want) {
			t.Errorf("%q in [%s]: error = %v", c.value, e.Section, err)
		}
	}
}

func TestCheckingReadsWhatsInPlace(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home)
	list := declare(t, s,
		[2]string{"GH_TOKEN", "GitHub/token"},
		[2]string{"MISSING", "GitHub/other"},
		[2]string{"~/.npmrc", "npmrc"},
		[2]string{"~/.ssh/key.pub", "op://vault/Key/public --mode 644"},
	)
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export GH_TOKEN='x'\nexport STRAY='y'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".npmrc"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range kind.Compare(t.Context(), s, list).Items {
		got = append(got, it.Name+" "+it.State)
	}
	want := []string{"MISSING missing", "~/.npmrc missing", "~/.ssh/key.pub missing", "STRAY extra"}
	if !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q (the npmrc's mode is wrong)", got, want)
	}
	for _, c := range fake.Calls() {
		if strings.HasPrefix(c, "op ") {
			t.Errorf("checking ran %s: it never asks 1Password", c)
		}
	}
}

func TestSyncPutsEachInPlace(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home)
	declare(t, s,
		[2]string{"PLAIN", "Section/plain   # a note"},
		[2]string{"AWKWARD", "Section/awkward"},
		[2]string{"~/.config/tool/token", "Section/plain"},
		[2]string{"~/.config/tool/key", "key-file --mode 640"},
		[2]string{"ELSEWHERE", "op://vault/Other/field"},
	)
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export STRAY='kept'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	awkward := `it's $HOME, a ` + "`date`" + `, a \ and "quotes"`
	fake.On("op", "whoami")
	optest.Holds(fake, map[string]string{
		item + "/Section/plain":   "plain value",
		item + "/Section/awkward": awkward,
		item + "/key-file":        "line one\nline two\n",
		"op://vault/Other/field":  "elsewhere",
	})
	report, err := s.Sync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Synced != 5 || report.Changed != 5 || len(report.Failed) != 0 {
		t.Errorf("report = %+v", report)
	}
	token := filepath.Join(home, ".config/tool/token")
	if data, _ := os.ReadFile(token); string(data) != "plain value\n" {
		t.Errorf("the file holds %q", data)
	}
	if info, _ := os.Stat(token); info.Mode().Perm() != 0o600 {
		t.Errorf("the file is %v", info.Mode().Perm())
	}
	key := filepath.Join(home, ".config/tool/key")
	if data, _ := os.ReadFile(key); string(data) != "line one\nline two\n" {
		t.Errorf("the file of many lines holds %q", data)
	}
	if info, _ := os.Stat(key); info.Mode().Perm() != 0o640 {
		t.Errorf("the file of many lines is %v", info.Mode().Perm())
	}
	env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh"))
	for _, want := range []string{"export PLAIN='plain value'   # a note\n", "export ELSEWHERE='elsewhere'\n", "export STRAY='kept'\n"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("~/.secrets.zsh =\n%s\nwant a line %q", env, want)
		}
	}
	out, err := exec.Command("/bin/zsh", "-f", "-c", `source "$1"; printf %s "$AWKWARD"`, "zsh", filepath.Join(home, ".secrets.zsh")).Output()
	if err != nil || string(out) != awkward {
		t.Errorf("zsh read back %q, %v; want %q", out, err, awkward)
	}
	if report, err := s.Sync(t.Context()); err != nil || report.Changed != 0 {
		t.Errorf("a second sync = %+v, %v; want nothing changed", report, err)
	}
}

// Each run of op that reads a value has the person approve it, as each is
// in a session of its own: a sync reads every value in one, op inject, its
// references on its standard input and its output kept from the log, after
// asking, of no one, whether 1Password is signed in.
func TestSyncReadsEveryValueInOneRunOfOp(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	declare(t, s,
		[2]string{"ONE", "Section/one"},
		[2]string{"TWO", "Section/two"},
		[2]string{"~/.ssh/key", "'op://vault/Key Item/private key'"},
		[2]string{"ALSO_ONE", "Section/one"},
	)
	fake.On("op", "whoami").Exits(1).PrintsToStderr("account is not signed in")
	fake.On("op", "account", "list", "--format", "json").Prints(`[{"url": "my.1password.com"}]`)
	optest.Holds(fake, map[string]string{item + "/Section/one": "1", item + "/Section/two": "2", "op://vault/Key Item/private key": "-----BEGIN KEY-----\nabc\n-----END KEY-----"})
	if report, err := s.Sync(t.Context()); err != nil || report.Synced != 4 {
		t.Fatalf("Sync() = %+v, %v", report, err)
	}
	want := []string{"op whoami", "op account list --format json", "op inject"}
	if got := fake.Calls(); !slices.Equal(got, want) {
		t.Errorf("ran %q, want %q: one run of op reading the values", got, want)
	}
	inject := fake.Commands()[2]
	if !inject.Secret || strings.Count(inject.Input, "{{") != 3 || !strings.Contains(inject.Input, "{{ op://vault/Key Item/private key }}") {
		t.Errorf("op inject read %q (secret: %v): want each reference once, its output kept from the log", inject.Input, inject.Secret)
	}
}

// signedIn has fake's 1Password signed in, with no session open, so that
// each run of op that reads a value asks the person to approve it.
func signedIn(fake *runnertest.Fake) {
	fake.On("op", "whoami").Exits(1)
	fake.On("op", "account", "list", "--format", "json").Prints(`[{"url": "my.1password.com"}]`)
}

// reads are the runs of op that read values, as fake has them.
func reads(fake *runnertest.Fake) []runner.Command {
	return slices.DeleteFunc(fake.Commands(), func(c runner.Command) bool {
		return len(c.Args) == 0 || c.Args[0] != "inject" && c.Args[0] != "read" && c.Args[0] != "vault"
	})
}

// While op waits for the person to approve the read, the step says so, and
// what they're to do, as macOS asking is said, then what it's doing again,
// nothing left to do; with a session open, nothing's asked, and nothing
// said.
func TestSyncSaysItsWaitingForApproval(t *testing.T) {
	for _, open := range []bool{false, true} {
		fake := runnertest.New(t)
		s := secret.New(fake, t.TempDir())
		declare(t, s, [2]string{"ONE", "Section/one"})
		if open {
			fake.On("op", "whoami")
		} else {
			signedIn(fake)
		}
		optest.Holds(fake, map[string]string{item + "/Section/one": "1"})
		var said []string
		ctx := event.WithSaying(t.Context(), func(d event.Doing) { said = append(said, d.Says+" | "+d.Todo) })
		if _, err := s.Sync(event.WithDoing(ctx, "syncing")); err != nil {
			t.Fatal(err)
		}
		want := []string{"waiting for your approval in 1Password | 1Password asks whether kit may read your secrets: approve it", "syncing | "}
		if open {
			want = nil
		}
		if !slices.Equal(said, want) {
			t.Errorf("a session open %v: the step said %q, want %q", open, said, want)
		}
	}
}

// While op waits for the person's approval, the step gives up its job, so
// the run starts others meanwhile.
func TestSyncGivesUpItsJobWhileItWaitsForApproval(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	list := declare(t, s, [2]string{"ONE", "Section/one"})
	signedIn(fake)
	asking, ran := make(chan struct{}), make(chan struct{})
	gaveUp := false
	fake.On("op", "inject").Does(func() {
		close(asking)
		select {
		case <-ran:
			gaveUp = true
		case <-time.After(5 * time.Second):
		}
	}).Filters(func(string) (string, error) { return "", errors.New("authorization prompt dismissed") })
	other := engine.Step{Name: "other", Title: "Other", Check: func(context.Context) check.Result {
		select {
		case <-asking:
			close(ran)
		case <-time.After(5 * time.Second):
		}
		return check.Result{State: check.OK}
	}}
	p, err := engine.New(kind.Placed(s, list, func(context.Context) kind.Placing { return kind.Placing{} }), other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(t.Context(), event.NewFanout(), engine.Options{Jobs: 1}); err != nil {
		t.Fatal(err)
	}
	if !gaveUp {
		t.Error("the other step didn't run while op waited for the person: want the secrets' step to give up its job")
	}
}

// A reference op names in saying why op inject failed is left out, failing
// in op's words and keeping its previous value, and the rest read in one
// more run: the person approves two reads, never more.
func TestSyncLeavesOutAReferenceOpNames(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home)
	declare(t, s,
		[2]string{"A", "Section/a"},
		[2]string{"BROKEN", "Section/broken"},
		[2]string{"D", "Section/d"},
	)
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export BROKEN='old value'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	signedIn(fake)
	optest.Holds(fake, map[string]string{item + "/Section/a": "a", item + "/Section/d": "d"})
	report, err := s.Sync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := "[ERROR] could not read secret " + item + "/Section/broken"; report.Synced != 2 || len(report.Failed) != 1 || report.Failed["BROKEN"] != want || len(report.Held) != 0 {
		t.Errorf("report = %+v, want BROKEN failed in op's words: %q", report, want)
	}
	got := reads(fake)
	if len(got) != 2 || got[0].String() != "op inject" || got[1].String() != "op inject" || strings.Contains(got[1].Input, "broken") || strings.Count(got[1].Input, "{{") != 2 {
		t.Errorf("read with %q: want every value in one op inject, then the rest but BROKEN in one more", fake.Calls())
	}
	env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh"))
	for _, line := range []string{"export A='a'\n", "export D='d'\n", "export BROKEN='old value'\n"} {
		if !strings.Contains(string(env), line) {
			t.Errorf("~/.secrets.zsh =\n%s\nwant a line %q", env, line)
		}
	}
}

// A reference that doesn't read, as kit secret add --ref checks it, says so
// in op's words, the reference once before them.
func TestReadsSaysWhyInOpsWords(t *testing.T) {
	ref := item + "/Section/missing"
	said := "[ERROR] could not read secret '" + ref + "': no such field"
	fake := runnertest.New(t)
	fake.On("op", "read", ref).Exits(1).PrintsToStderr(said)
	err := secret.New(fake, t.TempDir()).Reads(t.Context(), onepassword.Driver{}, ref)
	if want := ref + " doesn't read from 1Password: " + said; err == nil || err.Error() != want {
		t.Errorf("Reads() = %v, want %q", err, want)
	}
}

// held is why 1Password's secrets are held back, op having said said.
func held(said string) string {
	return onePassword.Name + " wasn't approved, or couldn't read them: " + said + "; " + onePassword.NoAnswer
}

// Should the second read fail naming no reference, the rest are held back,
// in op's words and how to have it answer, with no third: the reference op
// named first still fails in its words.
func TestSyncReadsTheRestOnceMore(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	declare(t, s, [2]string{"A", "Section/a"}, [2]string{"BROKEN", "Section/broken"})
	signedIn(fake)
	fake.On("op", "inject").Exits(1).PrintsToStderr("[ERROR] could not read secret " + item + "/Section/broken").
		Then().Exits(1).PrintsToStderr("[ERROR] authorization prompt dismissed, please try again")
	report, err := s.Sync(t.Context())
	if err != nil || report.Failed["BROKEN"] == "" || report.Held["A"] != held("[ERROR] authorization prompt dismissed, please try again") {
		t.Errorf("Sync() = %+v, %v; want BROKEN failed, A held back", report, err)
	}
	if n := len(reads(fake)); n != 2 {
		t.Errorf("read with %q: want two runs of op inject, no more", fake.Calls())
	}
}

// Should the second read name references too, they fail in op's words as
// the first's did, and only what neither read nor named is held back, with
// no third.
func TestSyncFailsWhatEachReadNames(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	declare(t, s, [2]string{"A", "Section/a"}, [2]string{"B", "Section/b"}, [2]string{"C", "Section/c"})
	signedIn(fake)
	sayA, sayB := "[ERROR] could not read secret "+item+"/Section/a", "[ERROR] could not read secret "+item+"/Section/b"
	fake.On("op", "inject").Exits(1).PrintsToStderr(sayA).Then().Exits(1).PrintsToStderr(sayB)
	report, err := s.Sync(t.Context())
	if err != nil || report.Failed["A"] != sayA || report.Failed["B"] != sayB || report.Held["C"] == "" || report.Synced != 0 {
		t.Errorf("Sync() = %+v, %v; want A and B failed in op's words, C held back", report, err)
	}
	if n := len(reads(fake)); n != 2 {
		t.Errorf("read with %q: want two runs of op inject, no more", fake.Calls())
	}
}

// op quotes the reference it was reading in saying the person didn't
// approve it, or didn't in time: that names no reference, and holds every
// secret back, in op's words, with no second read.
func TestAnApprovalOpQuotesAReferenceInNamesNone(t *testing.T) {
	for _, said := range []string{
		"[ERROR] 2026/11/05 05:52:13 could not read secret '" + item + "/Section/a': error initializing client: authorization prompt dismissed, please try again",
		"[ERROR] 2026/11/05 05:52:13 could not read secret '" + item + "/Section/b': error initializing client: authorization timeout",
	} {
		fake := runnertest.New(t)
		s := secret.New(fake, t.TempDir())
		declare(t, s, [2]string{"A", "Section/a"}, [2]string{"B", "Section/b"})
		signedIn(fake)
		fake.On("op", "inject").Exits(1).PrintsToStderr(said)
		if _, err := s.Sync(t.Context()); !engine.IsNotYet(err) || err.Error() != held(said) {
			t.Errorf("op saying %q: Sync() = %v, want both held back in op's words", said, err)
		}
		if n := len(reads(fake)); n != 1 {
			t.Errorf("op saying %q: read with %q: want one run of op inject", said, fake.Calls())
		}
	}
}

// A reference read alone, as a template can't take it, that op names as
// one it couldn't read fails alone: what op inject read is kept, never
// read again.
func TestSyncKeepsWhatOneReadReadBesideAnAwkwardReference(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	awkward := "op://vault/Item $HOME/field}}"
	declare(t, s, [2]string{"PLAIN", "Section/plain"}, [2]string{"AWKWARD", "'" + awkward + "'"})
	signedIn(fake)
	optest.Holds(fake, map[string]string{item + "/Section/plain": "plain"})
	fake.On("op", "read", awkward).Exits(1).PrintsToStderr("[ERROR] could not read secret '" + awkward + "': no such field")
	report, err := s.Sync(t.Context())
	if err != nil || report.Synced != 1 || report.Failed["AWKWARD"] == "" || len(report.Held) != 0 {
		t.Errorf("Sync() = %+v, %v; want PLAIN synced, AWKWARD failed", report, err)
	}
	if want := []string{"op whoami", "op account list --format json", "op inject", "op read '" + awkward + "'"}; !slices.Equal(fake.Calls(), want) {
		t.Errorf("ran %q, want %q", fake.Calls(), want)
	}
}

// A template would read a $ in a reference as a variable, and }} as its
// end: such a reference is read alone, never through op inject.
func TestSyncReadsAnAwkwardReferenceAlone(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	awkward := "op://vault/Item $HOME/field}}"
	declare(t, s, [2]string{"PLAIN", "Section/plain"}, [2]string{"AWKWARD", "'" + awkward + "'"})
	fake.On("op", "whoami")
	optest.Holds(fake, map[string]string{item + "/Section/plain": "plain", awkward: "awkward"})
	if report, err := s.Sync(t.Context()); err != nil || report.Synced != 2 {
		t.Fatalf("Sync() = %+v, %v", report, err)
	}
	for _, c := range fake.Commands() {
		if c.String() == "op inject" && strings.Contains(c.Input, "$HOME") {
			t.Errorf("op inject read %q", c.Input)
		}
	}
	if !slices.Contains(fake.Calls(), "op read '"+awkward+"'") {
		t.Errorf("ran %q: want the awkward reference read alone", fake.Calls())
	}
}

// A variable's value is read back from ~/.secrets.zsh as a sync wrote it,
// whatever's in it, and without asking 1Password; one written otherwise
// isn't guessed at.
func TestVariableReadsWhatASyncWrote(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home)
	declare(t, s,
		[2]string{"PLAIN", "Section/plain   # a note"},
		[2]string{"AWKWARD", "Section/awkward"},
		[2]string{"~/.config/tool/token", "Section/plain"},
	)
	awkward := `it's $HOME, ''a ` + "`date`" + `, a \ and "quotes" # not a note'`
	fake.On("op", "whoami")
	optest.Holds(fake, map[string]string{item + "/Section/plain": "plain value", item + "/Section/awkward": awkward})
	if _, err := s.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	calls := len(fake.Calls())
	for name, want := range map[string]string{"PLAIN": "plain value", "AWKWARD": awkward} {
		if got, ok, err := s.Variable(name); err != nil || !ok || got != want {
			t.Errorf("Variable(%s) = %v, %v: want what the sync wrote", name, ok, err)
		}
	}
	if _, ok, err := s.Variable("MISSING"); ok || err != nil {
		t.Errorf("Variable(MISSING) = %v, %v; want none", ok, err)
	}
	if !s.Has(t.Context(), "AWKWARD") || !s.Has(t.Context(), "~/.config/tool/token") || s.Has(t.Context(), "MISSING") {
		t.Error("Has() didn't say what's in place")
	}
	if len(fake.Calls()) != calls {
		t.Errorf("reading back ran %q: it never asks 1Password", fake.Calls()[calls:])
	}
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export PLAIN=unquoted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Variable("PLAIN"); err == nil || err.Error() != "~/.secrets.zsh holds PLAIN otherwise than kit writes it: run kit secret sync" {
		t.Errorf("Variable() of a line kit didn't write = %v", err)
	}
}

// When the one run of op that reads every value fails naming no reference,
// as when the person dismisses 1Password's prompt, or it's ended, hung, or
// the run's stopped, every secret's held back, not yet, nothing read again,
// alone or otherwise, and no session opened by asking the person again:
// the next sync tries one read again.
func TestSyncHoldsBackWhatOneReadDoesnt(t *testing.T) {
	for _, c := range []struct {
		name, said string
		script     func(s *runnertest.Script, stop func())
	}{
		{"dismissed", "authorization prompt dismissed", func(s *runnertest.Script, _ func()) { s.Exits(1).PrintsToStderr("authorization prompt dismissed") }},
		{"hung", "op didn't finish", func(s *runnertest.Script, _ func()) { s.Fails(fmt.Errorf("op inject: %w", context.DeadlineExceeded)) }},
		{"stopped", "op didn't finish", func(s *runnertest.Script, stop func()) { s.Does(stop).Hangs() }},
	} {
		fake := runnertest.New(t)
		s := secret.New(fake, t.TempDir())
		declare(t, s, [2]string{"PLAIN", "Section/plain"}, [2]string{"OTHER", "Section/other"})
		signedIn(fake)
		ctx, stop := context.WithCancel(t.Context())
		c.script(fake.On("op", "inject"), stop)
		_, err := s.Sync(ctx)
		stop()
		if err == nil || !engine.IsNotYet(err) || err.Error() != held(c.said) {
			t.Errorf("%s: Sync() = %v, want both held back", c.name, err)
		}
		if want := []string{"op whoami", "op account list --format json", "op inject"}; !slices.Equal(fake.Calls(), want) {
			t.Errorf("%s: ran %q, want %q: no read but the one", c.name, fake.Calls(), want)
		}
	}
}

// Listing no account, 1Password isn't signed in on this Mac, or its CLI
// isn't on: the sync says so at once, not yet rather than failed, and
// asks the app for nothing.
func TestSyncWhenOnePasswordIsntSignedIn(t *testing.T) {
	fake := runnertest.New(t)
	s := secret.New(fake, t.TempDir())
	declare(t, s, [2]string{"PLAIN", "Section/plain"})
	fake.On("op", "whoami").Exits(1)
	fake.On("op", "account", "list", "--format", "json").Prints("[]")
	_, err := s.Sync(t.Context())
	if err == nil || err.Error() != onePassword.SignedOut {
		t.Errorf("Sync() = %v", err)
	}
	if want := []string{"op whoami", "op account list --format json"}; !slices.Equal(fake.Calls(), want) {
		t.Errorf("ran %q, want %q: nothing asked of anyone", fake.Calls(), want)
	}
}

func TestRemove(t *testing.T) {
	home := t.TempDir()
	fake := runnertest.New(t)
	s := secret.New(fake, home)
	declare(t, s, [2]string{"KEEP", "a/b"}, [2]string{"GONE", "a/c"}, [2]string{"~/.token", "a/d"})
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("# header\nexport KEEP='1'\nexport GONE='2'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".token"), []byte("t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(t.Context(), []string{"GONE", "~/.token"}); err != nil {
		t.Fatal(err)
	}
	if env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh")); string(env) != "# header\nexport KEEP='1'\n" {
		t.Errorf("~/.secrets.zsh = %q", env)
	}
	if _, err := os.Stat(filepath.Join(home, ".token")); !os.IsNotExist(err) {
		t.Errorf("the file: %v, want it gone", err)
	}
}
