package secret_test

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/secret"
	"github.com/leeovery/kit/internal/kind/secret/onepassword"
	"github.com/leeovery/kit/internal/kind/secret/onepassword/optest"
	"github.com/leeovery/kit/internal/kind/secret/store"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// fakeStore is a secrets store of a kind other than 1Password: a value's
// reference is vault:<item>/<field>, an item's vault:<item>, or, named, as
// other, other:<item>/<field> and other:<item>. It holds values, stands as
// session says, and notes the references each read asks for.
type fakeStore struct {
	name    string
	mu      sync.Mutex
	session store.Session
	values  map[string]string
	reads   [][]string
	// locked has every read at once fail till Unlock; unlock is what
	// Unlock gives back.
	locked bool
	unlock error
}

func (f *fakeStore) About() store.About {
	if f.name != "" {
		title := "The " + f.name + " store"
		return store.About{ID: f.name, Name: title, Program: f.name, Ref: f.name + ":item/field", Item: f.name + ":item", Field: "<field>",
			NoAnswer: title + " doesn't answer", SignedOut: title + " is signed out", SignIn: store.SignIn{Title: "Sign in to " + title}}
	}
	return store.About{
		ID: "fake", Name: "Fake Vault", Program: "fakevault", Env: []string{"FAKE_ACCOUNT", "FAKE_SESSION_*"},
		Ref: "vault:item/field", Item: "vault:item", Field: "<field>",
		NoAnswer: "Fake Vault doesn't answer", SignedOut: "Fake Vault is signed out",
		SignIn: store.SignIn{Title: "Sign in to Fake Vault", Steps: []string{"Open Fake Vault", "Sign in"}, App: "com.example.fakevault", AppName: "Fake Vault"},
	}
}

// scheme is what its references start with.
func (f *fakeStore) scheme() string { return cmp.Or(f.name, "vault") + ":" }

func (f *fakeStore) IsRef(ref string) bool {
	rest, ok := strings.CutPrefix(ref, f.scheme())
	return ok && strings.Count(rest, "/") == 1
}

func (f *fakeStore) IsItem(ref string) bool {
	rest, ok := strings.CutPrefix(ref, f.scheme())
	return ok && rest != "" && !strings.Contains(rest, "/")
}

func (f *fakeStore) Join(item, short string) string { return item + "/" + short }

func (f *fakeStore) Short(item, ref string) (string, bool) { return strings.CutPrefix(ref, item+"/") }

func (f *fakeStore) Session(context.Context, runner.Runner) store.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.session
}

// signIn has the store signed in, as a person signing in does.
func (f *fakeStore) signIn() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.session = store.SignedIn
}

func (f *fakeStore) Unlock(context.Context, runner.Runner) error {
	if f.unlock == nil {
		f.locked = false
	}
	return f.unlock
}

func (f *fakeStore) Read(ctx context.Context, run runner.Runner, ref string) (string, error) {
	got, err := f.ReadMany(ctx, run, []string{ref})
	return got[ref], err
}

func (f *fakeStore) ReadMany(_ context.Context, _ runner.Runner, refs []string) (map[string]string, error) {
	f.reads = append(f.reads, refs)
	if f.locked {
		return nil, errors.New("not approved")
	}
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

func (f *fakeStore) Field(field string) (string, error) { return field, nil }

func (f *fakeStore) Attachment(name string) (string, error) { return name, nil }

func (f *fakeStore) Free(_ context.Context, _ runner.Runner, item, short string) error {
	if _, ok := f.values[f.Join(item, short)]; ok {
		return errors.New(short + " is kept already")
	}
	return nil
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

// twoStores are the secrets of a home in 1Password and fake, the stores
// kit knows, each driven as its reference says, and the fake runner
// standing in for 1Password, signed in, holding A, and with the fake's
// program: A in 1Password's item; B in full in the fake's, and C in the
// fake's item.
func twoStores(t *testing.T, fake *fakeStore) (*secret.Secrets, config.List, *runnertest.Fake, string) {
	t.Helper()
	secret.UseStores(t, onepassword.Driver{}, fake)
	home := t.TempDir()
	run := runnertest.New(t)
	s := secret.New(run, home)
	list := config.List{Kind: "secret", Declared: true, Entries: []config.Entry{
		{Name: "A", Value: "Section/a", Item: item, Section: "secrets " + item, Scope: "shared", Line: 2},
		{Name: "B", Value: "vault:keys/b", Section: "secrets", Scope: "shared", Line: 5},
		{Name: "C", Value: "c", Item: "vault:keys", Section: "secrets vault:keys", Scope: "shared", Line: 8},
	}}
	if _, err := s.Values(list); err != nil {
		t.Fatal(err)
	}
	run.On("op", "whoami")
	optest.Holds(run, map[string]string{item + "/Section/a": "a"})
	run.On("fakevault")
	return s, list, run, home
}

// applying applies the secrets declared by list, as a run does, and
// returns how the step finished.
func applying(t *testing.T, s *secret.Secrets, list config.List) check.Result {
	t.Helper()
	step := kind.Placed(s, list, func(context.Context) kind.Placing { return kind.Placing{} })
	p, err := engine.New(step)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Apply(t.Context(), event.NewFanout(), engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return report.Results["secret"]
}

// Each secret's reference picks its store: a config can use two, and each
// reads its own, once each.
func TestEachStoreReadsItsOwn(t *testing.T) {
	fake := &fakeStore{session: store.Open, values: map[string]string{"vault:keys/b": "b", "vault:keys/c": "c"}}
	s, _, run, home := twoStores(t, fake)
	report, err := s.Sync(t.Context())
	if err != nil || report.Synced != 3 || len(report.Failed)+len(report.Held) != 0 {
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
	if got := s.Program(); got != "" {
		t.Errorf("Program() = %q, want none: the secrets are in two stores", got)
	}
}

// A store that's signed out holds back its own secrets alone, in its
// words, each keeping its previous value; the rest sync, and the step
// stands not done yet, as with one store, rather than failed.
func TestAStoreSignedOutHoldsBackItsOwnAlone(t *testing.T) {
	fake := &fakeStore{session: store.SignedOut}
	s, list, _, home := twoStores(t, fake)
	if err := os.WriteFile(filepath.Join(home, ".secrets.zsh"), []byte("export B='old b'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := applying(t, s, list)
	if res.State != check.Deferred || res.Reason != "Fake Vault is signed out" || len(fake.reads) > 0 {
		t.Errorf("the step finished %+v, the fake read %q: want it not done yet, in the fake's words, nothing asked of it", res, fake.reads)
	}
	env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh"))
	if !strings.Contains(string(env), "export A='a'\n") || !strings.Contains(string(env), "export B='old b'\n") {
		t.Errorf("~/.secrets.zsh =\n%s\nwant A synced and B as it was", env)
	}
	report, err := s.Sync(t.Context())
	if err != nil || report.Synced != 1 || report.Held["B"] != "Fake Vault is signed out" || report.Held["C"] != "Fake Vault is signed out" || len(report.Failed) != 0 {
		t.Errorf("Sync() = %+v, %v; want B and C held back", report, err)
	}
}

// A store signed in that doesn't answer fails its own secrets, in its
// words, beside one that syncs; with no store left that answers, the sync
// goes no further.
func TestAStoreThatDoesntAnswerFailsItsOwnAlone(t *testing.T) {
	fake := &fakeStore{session: store.SignedIn, locked: true, unlock: errors.New("dismissed")}
	s, list, _, home := twoStores(t, fake)
	res := applying(t, s, list)
	if res.State != check.Failed || !strings.Contains(res.Reason, "B: Fake Vault doesn't answer") || !strings.Contains(res.Reason, "C: Fake Vault doesn't answer") {
		t.Errorf("the step finished %+v, want B and C failed in the fake's words", res)
	}
	if env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh")); !strings.Contains(string(env), "export A='a'\n") {
		t.Errorf("~/.secrets.zsh =\n%s\nwant A synced", env)
	}

	fake.locked, fake.unlock, fake.values = true, nil, map[string]string{"vault:keys/b": "b", "vault:keys/c": "c"}
	if report, err := s.Sync(t.Context()); err != nil || report.Synced != 3 {
		t.Errorf("Sync() once it answers = %+v, %v", report, err)
	}
}

// With every store down alike, nothing read, the sync goes no further,
// saying why in each one's words; with one held back and one failing, each
// store's secrets are reported as they stand.
func TestEveryStoreDown(t *testing.T) {
	fake := &fakeStore{session: store.SignedOut}
	secret.UseStores(t, onepassword.Driver{}, fake)
	run := runnertest.New(t)
	run.On("op", "whoami").Exits(1)
	run.On("op", "account", "list", "--format", "json").Prints("[]")
	run.On("fakevault")
	s := secret.New(run, t.TempDir())
	list := config.List{Kind: "secret", Declared: true, Entries: []config.Entry{
		{Name: "A", Value: item + "/a", Section: "secrets"},
		{Name: "B", Value: "vault:keys/b", Section: "secrets"},
	}}
	if _, err := s.Values(list); err != nil {
		t.Fatal(err)
	}
	if res := applying(t, s, list); res.State != check.Deferred || res.Reason != onePassword.SignedOut+"; Fake Vault is signed out" {
		t.Errorf("both signed out: the step finished %+v", res)
	}
	fake.session, fake.locked, fake.unlock = store.SignedIn, true, errors.New("dismissed")
	report, err := s.Sync(t.Context())
	if err != nil || report.Held["A"] != onePassword.SignedOut || report.Failed["B"] != "Fake Vault doesn't answer" {
		t.Errorf("one held back, one failing: Sync() = %+v, %v", report, err)
	}
}

// A store whose program isn't on kit's PATH holds back its own secrets
// alone: the rest sync.
func TestAStoresProgramMissingHoldsBackItsOwnAlone(t *testing.T) {
	fake := &fakeStore{session: store.Open, values: map[string]string{"vault:keys/b": "b", "vault:keys/c": "c"}}
	s, _, run, _ := twoStores(t, fake)
	run.On("fakevault").Fails(runner.ErrNotFound)
	report, err := s.Sync(t.Context())
	if err != nil || report.Synced != 1 || report.Held["B"] != "needs fakevault, which isn't there yet" || len(fake.reads) > 0 {
		t.Errorf("Sync() = %+v, %v, the fake read %q", report, err, fake.reads)
	}
}

// A reference, or an item, no store knows is refused, naming how each
// store's read; so is, in an item's section, any reference of a store's
// own, a value's or an item's.
func TestAReferenceNoStoreKnows(t *testing.T) {
	secret.UseStores(t, onepassword.Driver{}, &fakeStore{})
	s := secret.New(runnertest.New(t), t.TempDir())
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
		{config.Entry{Name: "X", Value: "op://vault/other", Item: item, Section: "secrets " + item},
			"op://vault/other is in full: in [secrets " + item + "] a line names a field or attachment of its item"},
		{config.Entry{Name: "X", Value: "vault:other", Item: "vault:keys", Section: "secrets vault:keys"},
			"vault:other is in full"},
	} {
		c.e.Scope, c.e.Line = "shared", 2
		if _, err := s.Values(config.List{Entries: []config.Entry{c.e}}); err == nil || !strings.HasPrefix(err.Error(), "shared/declarations:2: X: "+c.want) {
			t.Errorf("%s in [%s]: error = %v", c.e.Value, c.e.Section, err)
		}
	}
}

// The config's secrets sections know the items of the stores kit knows,
// the same list the kind reads: a store's item reads as a section's.
func TestSectionsKnowTheStoresItems(t *testing.T) {
	secret.UseStores(t, &fakeStore{})
	if err := secret.Descriptor.Section.Items("vault:keys"); err != nil {
		t.Errorf("Items(vault:keys) = %v, want the fake store's item", err)
	}
	if err := secret.Descriptor.Section.Items("op://vault/item"); err == nil {
		t.Error("Items(op://vault/item) = nil, with 1Password not among the stores")
	}
}

// Each store the secrets are in brings its sign-in, a card from the store's
// own words: watched, done once the store's signed in; waiting for its
// program and its app. A store kept nothing in brings none.
func TestEachStoreUsedBringsItsSignIn(t *testing.T) {
	fake := &fakeStore{session: store.SignedOut}
	secret.UseStores(t, onepassword.Driver{}, fake)
	s := secret.New(runnertest.New(t), t.TempDir())
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
	if got := s.Program(); got != "fakevault" {
		t.Errorf("Program() = %q, want the one store's", got)
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
	if got := secret.Env(getenv, environ); !slices.Equal(got, []string{"OP_SESSION_y=other", "OP_ACCOUNT=my"}) {
		t.Errorf("Env() = %q, want 1Password's", got)
	}
	secret.UseStores(t, &fakeStore{})
	if got, want := secret.Env(getenv, environ), []string{"FAKE_ACCOUNT=someone", "FAKE_SESSION_x=token"}; !slices.Equal(got, want) {
		t.Errorf("Env() = %q, want %q", got, want)
	}
}

// Stores held at first are waited for together: one whose sign-in is done
// syncs while another still waits for what its sign-in needs, never held
// back by it. Here B's sign-in waits, as for an app that never comes, until
// C's secret is in place, which it can be only if C's sync doesn't wait for
// B.
func TestStoresHeldAreAwaitedTogether(t *testing.T) {
	b, c := &fakeStore{name: "bee", session: store.SignedOut}, &fakeStore{name: "sea", session: store.SignedOut, values: map[string]string{"sea:keys/c": "c"}}
	secret.UseStores(t, b, c)
	home := t.TempDir()
	run := runnertest.New(t)
	run.On("bee")
	run.On("sea")
	s := secret.New(run, home)
	list := config.List{Kind: "secret", Declared: true, Entries: []config.Entry{
		{Name: "B", Value: "bee:keys/b", Section: "secrets"},
		{Name: "C", Value: "sea:keys/c", Section: "secrets"},
	}}
	if _, err := s.Values(list); err != nil {
		t.Fatal(err)
	}
	cPlaced := func(context.Context) bool {
		env, _ := os.ReadFile(filepath.Join(home, ".secrets.zsh"))
		return strings.Contains(string(env), "export C='c'")
	}
	sawC := false
	signIns := []engine.Step{
		{Name: "bee-sign-in", Check: func(context.Context) check.Result { return check.Result{State: check.Attention} },
			Apply: func(ctx context.Context, _ check.Result) error {
				sawC = engine.Await(ctx, engine.Wait{Thing: "the bee store's app", Ready: cPlaced}, "waiting for the bee store's app")
				return engine.NotYet("needs the bee store's app")
			}},
		{Name: "sea-sign-in", Check: func(context.Context) check.Result { return check.Result{State: check.Attention} },
			Apply: func(context.Context, check.Result) error {
				c.signIn()
				return nil
			}},
	}
	step := kind.Placed(s, list, func(context.Context) kind.Placing { return kind.Placing{} })
	step.Later = []engine.Wait{{Step: "bee-sign-in"}, {Step: "sea-sign-in"}}
	p, err := engine.New(append(signIns, step)...)
	if err != nil {
		t.Fatal(err)
	}
	report, err := p.Apply(t.Context(), event.NewFanout(), engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !sawC {
		t.Error("C wasn't synced while the bee store's sign-in waited: C's sync waited for B's")
	}
	if res := report.Results["secret"]; res.State != check.Deferred || res.Reason != "The bee store is signed out" {
		t.Errorf("the secrets finished %+v, want B not yet, in its store's words", res)
	}
}
