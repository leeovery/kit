package appstore_test

import (
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/appstore"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// A line's apps are their ids, its flags left out; what isn't an id, or a
// flag that takes every app or bundle ids, is refused, as kit-config
// declares an app by its id.
func TestLineNames(t *testing.T) {
	names := appstore.Descriptor.Help.Line.Names
	for _, tt := range []struct {
		args []string
		want []string
	}{
		{[]string{"497799835"}, []string{"497799835"}},
		{[]string{"--force", "497799835", "1091189122"}, []string{"497799835", "1091189122"}},
		{[]string{"--dry-run", "497799835"}, []string{"497799835"}},
		{[]string{"xcode"}, nil},
		{[]string{"xcode@497799835"}, nil},
		{[]string{"+497799835"}, nil},
		{[]string{"0"}, nil},
		{[]string{"--bundle", "com.apple.dt.Xcode"}, nil},
		{[]string{"--all"}, nil},
		{[]string{"--force"}, nil},
	} {
		got, err := names(tt.args)
		if !slices.Equal(got, tt.want) || (err == nil) != (tt.want != nil) {
			t.Errorf("Names(%q) = %q, %v; want %q", tt.args, got, err, tt.want)
		}
	}
}

// A line runs as given through sudo, as mas needs root: an install as kit's
// own do, given their time for each app; but a dry run, which mas runs
// without it.
func TestALineRunsThroughSudo(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("sudo", "-n", "mas", "get", "--force", "497799835", "1091189122")
	fake.On("sudo", "-n", "mas", "uninstall", "497799835")
	fake.On("mas", "uninstall", "--dry-run", "497799835")
	s := appstore.New(fake)
	for _, l := range []kind.CopiedLine{
		{Verb: []string{"get"}, Args: []string{"--force", "497799835", "1091189122"}, Installs: true},
		{Verb: []string{"uninstall"}, Args: []string{"497799835"}},
		{Verb: []string{"uninstall"}, Args: []string{"--dry-run", "497799835"}, DryRun: true},
	} {
		if err := s.Line(t.Context(), l); err != nil {
			t.Errorf("Line(%q) = %v", l.Verb, err)
		}
	}
	calls := []string{"sudo -n mas get --force 497799835 1091189122", "sudo -n mas uninstall 497799835", "mas uninstall --dry-run 497799835"}
	if !slices.Equal(fake.Calls(), calls) {
		t.Errorf("ran %q, want %q", fake.Calls(), calls)
	}
	if got := fake.Commands()[0].Timeout; got != 180*time.Minute {
		t.Errorf("the install's time = %v, want 90 minutes an app", got)
	}
}

// A line's install saying nothing at all for its first minutes is taken as
// no one signed in, as kit's own are, what's left of it stopped; one mas
// says no one's signed in for says so too, naming the fix.
func TestALineWithNoOneSignedInNamesTheFix(t *testing.T) {
	defer appstore.SetFirstWord(50 * time.Millisecond)()
	fake := runnertest.New(t)
	fake.On("sudo", "-n", "mas", "install", "1091189122").Hangs().InGroup(4242)
	fake.On(stray("1091189122")[0], stray("1091189122")[1:]...).Prints("5151\n")
	fake.On("sudo", "-n", "kill", "-KILL", "5151")
	err := appstore.New(fake).Line(t.Context(), kind.CopiedLine{Verb: []string{"install"}, Args: []string{"1091189122"}, Installs: true})
	if want := "the App Store didn't answer: sign in to the App Store, then try again"; err == nil || err.Error() != want {
		t.Errorf("Line = %v, want %q", err, want)
	}
	if !slices.Contains(fake.Calls(), "sudo -n kill -KILL 5151") {
		t.Errorf("ran %q, want what's left of the install stopped", fake.Calls())
	}

	fake = runnertest.New(t)
	fake.On("sudo", "-n", "mas", "install", "1091189122").Exits(1).PrintsToStderr(signedOut)
	err = appstore.New(fake).Line(t.Context(), kind.CopiedLine{Verb: []string{"install"}, Args: []string{"1091189122"}, Installs: true})
	if want := "no one's signed in: sign in to the App Store, then try again"; err == nil || err.Error() != want {
		t.Errorf("Line = %v, want %q", err, want)
	}
}
