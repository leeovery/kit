package power_test

import (
	"context"
	"slices"
	"strings"
	"testing"

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
