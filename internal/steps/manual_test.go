package steps_test

import (
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

func TestManual(t *testing.T) {
	state := t.TempDir()
	fake := runnertest.New(t)
	list := config.List{Entries: []config.Entry{
		{Name: "tool", Value: `"Install the tool from its site" -- test -d /Applications/Tool.app`, Scope: "laptop", Line: 2},
		{Name: "app", Value: `"Install the app" -- test -d /Applications/App.app`, Scope: "laptop", Line: 3},
		{Name: "sign-in", Value: `"Sign in to the service"`, Scope: "laptop", Line: 4},
	}}
	fake.On("test", "-d", "/Applications/Tool.app")
	fake.On("test", "-d", "/Applications/App.app").Exits(1)
	step := steps.Manual(fake, "/home/someone", state, list)
	res := step.Check(t.Context())
	var got []string
	for _, it := range res.Items {
		got = append(got, it.ID+" | "+it.Name+" | "+it.Detail)
	}
	want := "manual:app | Install the app | \nmanual:sign-in | Sign in to the service | then: kit done sign-in"
	if res.State != check.Attention || res.Summary != "1 of 3 done" || strings.Join(got, "\n") != want || step.Area != steps.AreaManual {
		t.Errorf("result = %s %q\n%s", res.State, res.Summary, strings.Join(got, "\n"))
	}
	if err := steps.MarkDone(state, []string{"sign-in"}, time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if res := step.Check(t.Context()); len(res.Items) != 1 || res.Items[0].ID != "manual:app" {
		t.Errorf("after kit done, items = %+v", res.Items)
	}
}

func TestManualLinesRead(t *testing.T) {
	for value, want := range map[string]string{
		`-- test -d x`:      "what to do comes after the name",
		`"Do it" test -d x`: "after what to do comes --",
		`"Do it" --`:        "after what to do comes --",
	} {
		if _, err := steps.ParseManual("/home", config.Entry{Name: "x", Value: value}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error = %v", value, err)
		}
	}
}
