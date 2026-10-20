package secret_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kind/secret/onepassword"
	"github.com/leeovery/kit/internal/kind/secret/secrettest"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// fakeStore is a secrets store of a kind other than 1Password: a value's
// reference is vault:<item>/<field>, an item's vault:<item>. It holds
// values, stands as session says, and notes the references each read asks
// for.
type fakeStore struct {
	session store.Session
	values  map[string]string
	reads   [][]string
}

func (f *fakeStore) About() store.About {
	return store.About{
		ID: "fake", Name: "Fake Vault", Program: "fakevault", Env: []string{"FAKE_ACCOUNT", "FAKE_SESSION_*"},
		Ref: "vault:item/field", Item: "vault:item", Field: "<field>",
		NoAnswer: "Fake Vault doesn't answer", SignedOut: "Fake Vault is signed out",
		SignIn: store.SignIn{Title: "Sign in to Fake Vault", Steps: []string{"Open Fake Vault", "Sign in"}, App: "com.example.fakevault", AppName: "Fake Vault"},
	}
}

func (f *fakeStore) IsRef(ref string) bool {
	rest, ok := strings.CutPrefix(ref, "vault:")
	return ok && strings.Count(rest, "/") == 1
}

func (f *fakeStore) IsItem(ref string) bool {
	rest, ok := strings.CutPrefix(ref, "vault:")
	return ok && rest != "" && !strings.Contains(rest, "/")
}

func (f *fakeStore) Join(item, short string) string { return item + "/" + short }

func (f *fakeStore) Short(item, ref string) (string, bool) { return strings.CutPrefix(ref, item+"/") }

func (f *fakeStore) Session(context.Context, runner.Runner) store.Session { return f.session }

func (f *fakeStore) Unlock(context.Context, runner.Runner) error { return nil }

func (f *fakeStore) Read(ctx context.Context, run runner.Runner, ref string) (string, error) {
	got, err := f.ReadMany(ctx, run, []string{ref})
	return got[ref], err
}

func (f *fakeStore) ReadMany(_ context.Context, _ runner.Runner, refs []string) (map[string]string, error) {
	f.reads = append(f.reads, refs)
	values := map[string]string{}
	for _, ref := range refs {
		v, ok := f.values[ref]
		if !ok {
			return nil, errors.New("no such value")
		}
		values[ref] = v
	}
	return values, nil
}

func (f *fakeStore) Store(context.Context, runner.Runner, string, string, string) (string, error) {
	return "", errors.New("not kept here")
}

func (f *fakeStore) Attach(context.Context, runner.Runner, string, string, string) (string, error) {
	return "", errors.New("not kept here")
}

func (f *fakeStore) Delete(context.Context, runner.Runner, string, string) error {
	return errors.New("not kept here")
}

// twoStores are the secrets of a home in 1Password and fake, each driven
// as its reference says, and the fake runner standing in for 1Password,
// signed in, holding A: A in 1Password's item; B in full in the fake's,
// and C in the fake's item.
func twoStores(t *testing.T, fake *fakeStore) (*secret.Secrets, *runnertest.Fake, string) {
	t.Helper()
	home := t.TempDir()
	run := runnertest.New(t)
	s := secret.NewWith(run, home, []store.Driver{onepassword.Driver{}, fake})
	list := config.List{Entries: []config.Entry{
		{Name: "A", Value: "Section/a", Item: item, Section: "secrets " + item, Scope: "shared", Line: 2},
		{Name: "B", Value: "vault:keys/b", Section: "secrets", Scope: "shared", Line: 5},
		{Name: "C", Value: "c", Item: "vault:keys", Section: "secrets vault:keys", Scope: "shared", Line: 8},
	}}
	if _, err := s.Values(list); err != nil {
		t.Fatal(err)
	}
	run.On("op", "whoami")
	secrettest.Holds(run, map[string]string{item + "/Section/a": "a"})
	return s, run, home
}

// Each secret's reference picks its store: a config can use two, and each
// reads its own, once each.
func TestEachStoreReadsItsOwn(t *testing.T) {
	fake := &fakeStore{session: store.Open, values: map[string]string{"vault:keys/b": "b", "vault:keys/c": "c"}}
	s, run, home := twoStores(t, fake)
	report, err := s.Sync(t.Context())
	if err != nil || report.Synced != 3 || len(report.Failed) != 0 {
		t.Fatalf("Sync() = %+v, %v", report, err)
	}
	if want := [][]string{{"vault:keys/b", "vault:keys/c"}}; !slices.EqualFunc(fake.reads, want, slices.Equal) {
		t.Errorf("the fake store read %q, want its own, at once", fake.reads)
	}
	for _, c := range run.Commands() {
		if c.String() == "op inject" && strings.Contains(c.Input, "vault:") {
			t.Errorf("op inject read %q: want 1Password's alone", c.Input)
		}
	}
	env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh"))
	for _, want := range []string{"# Written by kit from 1Password and Fake Vault:", "export A='a'\n", "export B='b'\n", "export C='c'\n"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("~/.secrets.zsh =\n%s\nwant %q", env, want)
		}
	}
	if got := s.Commands(); !slices.Equal(got, []string{"op", "fakevault"}) {
		t.Errorf("Commands() = %q, want each store's program", got)
	}
}

// A store that's signed out fails its own secrets alone, in its words, each
// keeping its previous value; the rest sync. With every store signed out,
// the sync can't go on.
func TestAStoreSignedOutFailsItsOwnAlone(t *testing.T) {
	fake := &fakeStore{session: store.SignedOut}
	s, _, home := twoStores(t, fake)
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export B='old b'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := s.Sync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Synced != 1 || report.Failed["B"] != "Fake Vault is signed out" || report.Failed["C"] != "Fake Vault is signed out" || len(fake.reads) > 0 {
		t.Errorf("Sync() = %+v, read %q: want B and C failed in the fake's words, nothing asked of it", report, fake.reads)
	}
	env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh"))
	if !strings.Contains(string(env), "export A='a'\n") || !strings.Contains(string(env), "export B='old b'\n") {
		t.Errorf("~/.secrets.zsh =\n%s\nwant A synced and B as it was", env)
	}

	s = secret.NewWith(runnertest.New(t), t.TempDir(), []store.Driver{fake})
	if _, err := s.Values(config.List{Entries: []config.Entry{{Name: "B", Value: "vault:keys/b", Section: "secrets"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(t.Context()); err == nil || err.Error() != "Fake Vault is signed out" {
		t.Errorf("Sync() of the signed-out store's alone = %v", err)
	}
}

// A reference, or an item, no store knows is refused, naming how each
// store's read.
func TestAReferenceNoStoreKnows(t *testing.T) {
	s := secret.NewWith(runnertest.New(t), t.TempDir(), []store.Driver{onepassword.Driver{}, &fakeStore{}})
	for _, c := range []struct {
		e    config.Entry
		want string
	}{
		{config.Entry{Name: "X", Value: "elsewhere:a/b", Section: "secrets"},
			`"elsewhere:a/b" isn't a reference kit knows: in a plain [secrets], give where its store keeps it in full (1Password's, as in op://vault/item/field; Fake Vault's, as in vault:item/field)`},
		{config.Entry{Name: "X", Value: "a", Item: "elsewhere:a", Section: "secrets elsewhere:a"},
			`"elsewhere:a" isn't an item's reference kit knows: 1Password's, as in op://vault/item; Fake Vault's, as in vault:item`},
		{config.Entry{Name: "X", Value: "op://vault/a/b", Item: "vault:keys", Section: "secrets vault:keys"},
			"op://vault/a/b is in full: in [secrets vault:keys] a line names a field or attachment of its item, as in <field>"},
	} {
		c.e.Scope, c.e.Line = "shared", 2
		if _, err := s.Values(config.List{Entries: []config.Entry{c.e}}); err == nil || !strings.HasPrefix(err.Error(), "shared/declarations:2: X: "+c.want) {
			t.Errorf("%s in [%s]: error = %v", c.e.Value, c.e.Section, err)
		}
	}
}

// Each store the secrets are in brings its sign-in, a card from the store's
// own words: watched, done once the store's signed in; waiting for its
// program and its app. A store kept nothing in brings none.
func TestEachStoreUsedBringsItsSignIn(t *testing.T) {
	fake := &fakeStore{session: store.SignedOut}
	s := secret.NewWith(runnertest.New(t), t.TempDir(), []store.Driver{onepassword.Driver{}, fake})
	if _, err := s.Values(config.List{Entries: []config.Entry{{Name: "B", Value: "vault:keys/b", Section: "secrets"}}}); err != nil {
		t.Fatal(err)
	}
	cards := s.Cards()
	if len(cards) != 1 {
		t.Fatalf("Cards() = %+v, want the fake store's alone", cards)
	}
	c := cards[0]
	if c.Name != "fake-sign-in" || c.Title != "Sign in to Fake Vault" || !slices.Equal(c.Steps, []string{"Open Fake Vault", "Sign in"}) || c.Why != "Your secrets come from Fake Vault." ||
		c.Program != "fakevault" || c.App != "com.example.fakevault" || c.AppName != "Fake Vault" {
		t.Errorf("Cards()[0] = %+v", c)
	}
	if c.Done(t.Context()) {
		t.Error("Done() with the store signed out = true")
	}
	fake.session = store.SignedIn
	if !c.Done(t.Context()) {
		t.Error("Done() with the store signed in = false")
	}
	var _ kind.Carded = s
}

// The variables each store names are passed on to what kit runs, a name
// ending * standing for every variable starting so; the rest aren't.
func TestEnvPassesOnWhatTheStoresName(t *testing.T) {
	getenv := func(name string) string {
		return map[string]string{"FAKE_ACCOUNT": "someone", "OP_ACCOUNT": "my", "HOME": "/Users/someone"}[name]
	}
	environ := []string{"FAKE_SESSION_x=token", "OP_SESSION_y=other", "UNRELATED=z"}
	got := secret.EnvOf([]store.Driver{&fakeStore{}}, getenv, environ)
	if want := []string{"FAKE_ACCOUNT=someone", "FAKE_SESSION_x=token"}; !slices.Equal(got, want) {
		t.Errorf("EnvOf() = %q, want %q", got, want)
	}
	if got := secret.Env(getenv, environ); !slices.Equal(got, []string{"OP_SESSION_y=other", "OP_ACCOUNT=my"}) {
		t.Errorf("Env() = %q, want 1Password's", got)
	}
}
