package steps_test

import (
	"context"
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

func own(lines ...[2]string) config.List {
	l := config.List{Kind: "checks"}
	for i, line := range lines {
		l.Entries = append(l.Entries, config.Entry{Name: line[0], Value: line[1], File: "laptop", Line: i + 2})
	}
	return l
}

func TestOwnChecks(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("dig", "+short", "@127.0.0.1", "example.com").Prints("93.184.215.14\n")
	fake.On("/home/someone/bin/check-containers").Exits(1).Prints("plex isn't running\nsonarr isn't running\n")
	fake.On("quiet").Exits(3)
	fake.On("gone").Fails(runner.ErrNotFound)
	fake.On("slow").Fails(context.DeadlineExceeded)

	step := steps.Own(fake, "/home/someone", own(
		[2]string{"dns", "-- dig +short @127.0.0.1 example.com"},
		[2]string{"containers", "-- ~/bin/check-containers"},
		[2]string{"quiet", "-- quiet"},
		[2]string{"gone", "-- gone"},
		[2]string{"slow", "-- slow"},
		[2]string{"odd", "dig example.com"},
	))
	if step.Name != "checks" || step.Area != steps.AreaChecks {
		t.Errorf("step = %s in %s", step.Name, step.Area)
	}
	res := step.Check(t.Context())
	got := map[string]string{}
	for _, it := range res.Items {
		got[it.ID] = it.Name
		if it.Detail == "" {
			t.Errorf("%s: no detail saying whose check it is", it.ID)
		}
	}
	want := map[string]string{
		"checks:containers": "containers: plex isn't running",
		"checks:quiet":      "quiet: it exited 3, saying nothing",
		"checks:gone":       "gone: gone isn't there",
		"checks:slow":       "slow: it didn't finish: " + context.DeadlineExceeded.Error(),
		"checks:odd":        "odd: its line isn't a name, then -- and a command",
	}
	if res.State != check.Attention || res.Summary != "5 of 6 failing" || len(got) != len(want) {
		t.Errorf("Check() = %s %q, items %q", res.State, res.Summary, got)
	}
	for id, name := range want {
		if got[id] != name {
			t.Errorf("%s = %q, want %q", id, got[id], name)
		}
	}
	if cmds := fake.Commands(); !slices.ContainsFunc(cmds, func(c runner.Command) bool { return c.Name == "dig" && c.Timeout > 0 }) {
		t.Errorf("ran %+v, want each check given a timeout", cmds)
	}
}

func TestOwnChecksPassing(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("true")
	res := steps.Own(fake, "/home/someone", own([2]string{"fine", "-- true"})).Check(t.Context())
	if res.State != check.OK || res.Summary != "1 check, passing" {
		t.Errorf("Check() = %+v", res)
	}
}
