package power_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/power"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

const laptop = `Battery Power:
 Sleep On Power Button 1
 sleep                1
 standby              1
AC Power:
 Sleep On Power Button 1
 sleep                %s
 standby              0
`

func items(t *testing.T, p *power.Power, list config.List) []string {
	t.Helper()
	var out []string
	for _, it := range kind.Compare(t.Context(), p, list).Items {
		out = append(out, strings.Join(slices.DeleteFunc([]string{it.Name, it.State, it.Detail, it.Action}, func(s string) bool { return s == "" }), " "))
	}
	return out
}

func TestPowerSettings(t *testing.T) {
	fake := runnertest.New(t)
	p := power.New(fake, t.TempDir())
	list, err := p.Values(config.List{Entries: []config.Entry{
		{Name: "charger:sleep", Value: "0"},
		{Name: "all:Sleep On Power Button", Value: "1"},
		{Name: "all:standby", Value: "0"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	fake.On("pmset", "-g", "custom").Prints(strings.Replace(laptop, "%s", "0", 1))
	want := []string{"all:standby changed set differently on each power source install"}
	if got := items(t, p, list); !slices.Equal(got, want) {
		t.Errorf("items = %q, want %q (standby differs between the sources)", got, want)
	}
	fake.On("pmset", "-g", "custom").Prints(strings.Replace(laptop, "%s", "10", 1))
	want = []string{"all:standby changed set differently on each power source install", "charger:sleep diverged set to 10"}
	if got := items(t, p, list); !slices.Equal(got, want) {
		t.Errorf("after the charger's sleep changed, items = %q, want %q", got, want)
	}
	p.SetAdmin(func(context.Context) bool { return true })
	fake.On("sudo", "-n", "pmset", "-a", "standby", "0")
	if err := p.Install(t.Context(), []string{"all:standby"}); err != nil {
		t.Errorf("Install() = %v", err)
	}
	if v, err := p.Value(t.Context(), "charger:sleep"); err != nil || v != "10" {
		t.Errorf("Value() = %q, %v", v, err)
	}
}

func TestADesktopShowsOneSource(t *testing.T) {
	fake := runnertest.New(t)
	p := power.New(fake, t.TempDir())
	list, _ := p.Values(config.List{Entries: []config.Entry{{Name: "all:sleep", Value: "0"}}})
	fake.On("pmset", "-g", "custom").Prints(" sleep                0\n autorestart          1\n")
	if got := items(t, p, list); len(got) != 0 {
		t.Errorf("items = %q, want none", got)
	}
}

// regiven is a kind.Password held, sudo having forgotten it, given again
// once kit finds out.
type regiven struct{ forgot int }

func (*regiven) Held() bool                { return true }
func (*regiven) Wait(context.Context) bool { return true }
func (r *regiven) Forgotten(context.Context) bool {
	r.forgot++
	return true
}

// A setting whose sudo finds the password forgotten, as once Composer's
// cleared it mid-run, is set once kit's given it again.
func TestASettingIsSetOnceThePasswordsGivenAgain(t *testing.T) {
	fake := runnertest.New(t)
	p := power.New(fake, t.TempDir())
	if _, err := p.Values(config.List{Entries: []config.Entry{{Name: "all:standby", Value: "0"}}}); err != nil {
		t.Fatal(err)
	}
	p.SetAdmin(func(context.Context) bool { return true })
	fake.On("sudo", "-n", "pmset", "-a", "standby", "0").Exits(1).PrintsToStderr("sudo: a password is required").Then()
	pw := &regiven{}
	err := p.Install(kind.WithPassword(t.Context(), pw), []string{"all:standby"})
	if want := []string{"sudo -n pmset -a standby 0", "sudo -n pmset -a standby 0"}; err != nil || !slices.Equal(fake.Calls(), want) || pw.forgot != 1 {
		t.Errorf("Install() = %v, ran %q, kit found out %d times; want it set, %q, once", err, fake.Calls(), pw.forgot, want)
	}
}

// custom is what pmset -g custom prints on a Mac mini: disablesleep isn't
// in it.
const custom = `AC Power:
 Sleep On Power Button 1
 autorestartatconnect 0
 lowpowermode         0
 standby              0
 ttyskeepawake        1
 powernap             0
 displaysleep         0
 womp                 1
 networkoversleep     0
 sleep                0
 tcpkeepalive         1
 autorestart          1
 disksleep            10
 SleepServices        0
`

// inUse is the part of pmset -g after its system-wide settings.
const inUse = `Currently in use:
 standby              0
 sleep                0 (sleep prevented by powerd, screensharingd)
`

func TestDisableSleepIsReadFromTheSystemWideSettings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		declared []config.Entry
		g        string
		want     []string
	}{
		{"set, declared for every source", []config.Entry{{Name: "all:disablesleep", Value: "1"}}, "System-wide power settings:\n SleepDisabled\t\t1\n" + inUse, nil},
		{"set, declared for the charger", []config.Entry{{Name: "charger:disablesleep", Value: "1"}}, "System-wide power settings:\n SleepDisabled\t\t1\n" + inUse, nil},
		{"set, declared for the battery", []config.Entry{{Name: "battery:disablesleep", Value: "1"}}, "System-wide power settings:\n SleepDisabled\t\t1\n" + inUse, nil},
		{"not set", []config.Entry{{Name: "all:disablesleep", Value: "1"}}, "System-wide power settings:\n SleepDisabled\t\t0\n" + inUse, []string{"all:disablesleep changed set to 0 install"}},
		{"not shown", []config.Entry{{Name: "all:disablesleep", Value: "1"}}, inUse, []string{"all:disablesleep missing install"}},
		{"not shown, declared off", []config.Entry{{Name: "all:disablesleep", Value: "0"}}, inUse, []string{"all:disablesleep missing install"}},
		{"shown only as in use", []config.Entry{{Name: "all:disablesleep", Value: "1"}}, "Currently in use:\n SleepDisabled\t\t1\n sleep                0 (sleep prevented by powerd)\n", []string{"all:disablesleep missing install"}},
		{"beside a setting pmset -g custom shows", []config.Entry{{Name: "all:disablesleep", Value: "1"}, {Name: "all:sleep", Value: "0"}}, "System-wide power settings:\n SleepDisabled\t\t1\n" + inUse, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := runnertest.New(t)
			p := power.New(fake, t.TempDir())
			list, err := p.Values(config.List{Entries: tc.declared})
			if err != nil {
				t.Fatal(err)
			}
			fake.On("pmset", "-g", "custom").Prints(custom)
			fake.On("pmset", "-g").Prints(tc.g)
			if got := items(t, p, list); !slices.Equal(got, tc.want) {
				t.Errorf("items = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPmsetGIsReadOnlyForDisableSleep(t *testing.T) {
	fake := runnertest.New(t)
	p := power.New(fake, t.TempDir())
	list, err := p.Values(config.List{Entries: []config.Entry{{Name: "all:sleep", Value: "0"}, {Name: "all:disksleep", Value: "10"}}})
	if err != nil {
		t.Fatal(err)
	}
	fake.On("pmset", "-g", "custom").Prints(custom)
	if got := items(t, p, list); len(got) != 0 {
		t.Errorf("items = %q, want none", got)
	}
	for _, c := range fake.Calls() {
		if c != "pmset -g custom" {
			t.Errorf("ran %q, want only pmset -g custom", c)
		}
	}
}

func TestDisableSleepsValueIsTheSystemWideOne(t *testing.T) {
	fake := runnertest.New(t)
	p := power.New(fake, t.TempDir())
	fake.On("pmset", "-g", "custom").Prints(custom)
	fake.On("pmset", "-g").Prints("System-wide power settings:\n SleepDisabled\t\t1\n" + inUse)
	for _, name := range []string{"all:disablesleep", "charger:disablesleep", "battery:disablesleep"} {
		if v, err := p.Value(t.Context(), name); err != nil || v != "1" {
			t.Errorf("Value(%q) = %q, %v; want 1", name, v, err)
		}
	}
}

// disablesleep set to 1 by kit and turned off since, or no longer shown, was
// changed on the Mac.
func TestDisableSleepChangedSinceKitSawItSetDiverged(t *testing.T) {
	for _, tc := range []struct{ name, g, want string }{
		{"set to 0", "System-wide power settings:\n SleepDisabled\t\t0\n" + inUse, "all:disablesleep diverged set to 0"},
		{"not shown", inUse, "all:disablesleep diverged not shown by pmset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := runnertest.New(t)
			p := power.New(fake, t.TempDir())
			list, err := p.Values(config.List{Entries: []config.Entry{{Name: "all:disablesleep", Value: "1"}}})
			if err != nil {
				t.Fatal(err)
			}
			fake.On("pmset", "-g", "custom").Prints(custom)
			fake.On("pmset", "-g").Prints("System-wide power settings:\n SleepDisabled\t\t1\n" + inUse)
			if got := items(t, p, list); len(got) != 0 {
				t.Fatalf("items = %q, want none", got)
			}
			fake.On("pmset", "-g").Prints(tc.g)
			if got, want := items(t, p, list), []string{tc.want}; !slices.Equal(got, want) {
				t.Errorf("items = %q, want %q", got, want)
			}
		})
	}
}

func TestPmsetGFailingFailsTheCheck(t *testing.T) {
	fake := runnertest.New(t)
	p := power.New(fake, t.TempDir())
	list, err := p.Values(config.List{Entries: []config.Entry{{Name: "all:disablesleep", Value: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	fake.On("pmset", "-g", "custom").Prints(custom)
	fake.On("pmset", "-g").Exits(1)
	if got := kind.Compare(t.Context(), p, list); got.State != check.Failed || got.Reason != "pmset exited 1" {
		t.Errorf("Compare() = %v, %q; want it failed, pmset -g's error its reason", got.State, got.Reason)
	}
}

// disablesleep is one value for every source, so kit sets it with -a,
// whichever source it's declared for.
func TestDisableSleepIsSetForEverySource(t *testing.T) {
	fake := runnertest.New(t)
	p := power.New(fake, t.TempDir())
	if _, err := p.Values(config.List{Entries: []config.Entry{{Name: "charger:disablesleep", Value: "1"}}}); err != nil {
		t.Fatal(err)
	}
	fake.On("sudo", "-n", "pmset", "-a", "disablesleep", "1")
	if err := p.Install(t.Context(), []string{"charger:disablesleep"}); err != nil {
		t.Errorf("Install() = %v, ran %q; want sudo -n pmset -a disablesleep 1", err, fake.Calls())
	}
}
