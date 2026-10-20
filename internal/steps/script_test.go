package steps_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

func TestParseScript(t *testing.T) {
	for _, tc := range []struct {
		value, wrong string
		want         steps.Script
	}{
		{value: `"Installs the tool"`, want: steps.Script{Does: "Installs the tool"}},
		{value: `"Trusts the authority" --after brew --after cask:1password --before fonts --sudo`, want: steps.Script{Does: "Trusts the authority", After: []string{"brew", "cask:1password"}, Before: []string{"fonts"}, Admin: true}},
		{value: `"Fetches releases" --secret TOKEN --secret ~/.config/tool/key`, want: steps.Script{Does: "Fetches releases", Secrets: []string{"TOKEN", "~/.config/tool/key"}}},
		{value: "--sudo", wrong: "what the step does comes after its name, in quotes"},
		{value: `"Does" --sideways`, wrong: `"--sideways" isn't one of a step's options: --after, --before, --secret and --sudo`},
		{value: `"Does" --secret --sudo`, wrong: "--secret needs a secret's name after it"},
		{value: `"Does" --after`, wrong: "--after needs a step's name after it"},
		{value: `"Does" --before --sudo`, wrong: "--before needs a step's name after it"},
		{value: `"Does" --before cask:1password`, wrong: "--before a step"},
		{value: `"Does" --after cask:`, wrong: "one of its things as in cask:firefox"},
		{value: `"Does" --needs secret`, wrong: "--needs is --after now"},
		{value: `"Does" --admin`, wrong: "--admin is --sudo now"},
		{value: `"Does`, wrong: "a quote isn't closed"},
	} {
		got, err := steps.ParseScript("/config", config.Entry{Name: "tool", Value: tc.value, Scope: "laptop"})
		switch {
		case tc.wrong != "":
			if err == nil || !strings.Contains(err.Error(), tc.wrong) {
				t.Errorf("%s: err = %v, want %q", tc.value, err, tc.wrong)
			}
			continue
		case err != nil:
			t.Errorf("%s: %v", tc.value, err)
			continue
		}
		if got.Name != "tool" || got.Folder != "laptop/steps/tool" || got.Dir != filepath.Join("/config", "laptop", "steps", "tool") {
			t.Errorf("%s: placed %+v", tc.value, got)
		}
		if got.Does != tc.want.Does || !slices.Equal(got.After, tc.want.After) || !slices.Equal(got.Before, tc.want.Before) || !slices.Equal(got.Secrets, tc.want.Secrets) || got.Admin != tc.want.Admin {
			t.Errorf("%s: = %+v, want %+v", tc.value, got, tc.want)
		}
	}
}

func TestTemplate(t *testing.T) {
	got := steps.Template("tool", "Installs the tool")
	if !strings.HasPrefix(got, "#!/bin/bash\n# tool: Installs the tool\n") || strings.Contains(got, "STEP:") {
		t.Errorf("Template() starts\n%s", got[:min(len(got), 120)])
	}
	for _, want := range []string{"check() {", "apply() {", `"$1"`} {
		if !strings.Contains(got, want) {
			t.Errorf("Template() has no %q", want)
		}
	}
}

// script is a step of the user's own, its folder in a directory of its
// own holding an executable run script.
func script(t *testing.T, s steps.Script) steps.Script {
	t.Helper()
	s.Name, s.Folder, s.Dir = "tool", "laptop/steps/tool", filepath.Join(t.TempDir(), "laptop", "steps", "tool")
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "run"), []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestScriptStepChecks(t *testing.T) {
	s := script(t, steps.Script{Does: "Installs the tool", After: []string{"brew"}, Before: []string{"fonts"}, Admin: true})
	run := filepath.Join(s.Dir, "run")
	for _, tc := range []struct {
		name   string
		answer func(*runnertest.Script)
		want   check.Result
	}{
		{"done", func(sc *runnertest.Script) { sc.Prints("\ninstalled, 2.1\nmore\n") }, check.Result{State: check.OK, Summary: "installed, 2.1"}},
		{"done quietly", func(*runnertest.Script) {}, check.Result{State: check.OK, Summary: "done"}},
		{"not done", func(sc *runnertest.Script) { sc.Exits(1).Prints("not installed\n") }, check.Result{State: check.Attention, Summary: "not installed"}},
		{"not done quietly", func(sc *runnertest.Script) { sc.Exits(1) }, check.Result{State: check.Attention, Summary: "not done"}},
		{"can't tell", func(sc *runnertest.Script) { sc.Exits(2).Prints("can't read the keychain\n") }, check.Result{State: check.Failed, Reason: "can't read the keychain"}},
		{"not yet", func(sc *runnertest.Script) { sc.Exits(3).Prints("no key file: kit secret sync writes it\n") }, check.Result{State: check.Deferred, Reason: "no key file: kit secret sync writes it"}},
		{"crashed", func(sc *runnertest.Script) {
			sc.Exits(127).Prints("checking\n").PrintsToStderr(run + ": line 4: tool: command not found\n")
		}, check.Result{State: check.Failed, Reason: "tool: command not found"}},
		{"silent", func(sc *runnertest.Script) { sc.Exits(5) }, check.Result{State: check.Failed, Reason: "it exited 5, saying nothing"}},
		{"slow", func(sc *runnertest.Script) { sc.Fails(context.DeadlineExceeded) }, check.Result{State: check.Failed, Reason: "it didn't finish: " + context.DeadlineExceeded.Error()}},
	} {
		fake := runnertest.New(t)
		tc.answer(fake.On(run, "check"))
		step := steps.ScriptStep(fake, &steps.Admin{}, s)
		got := step.Check(t.Context())
		if got.State != tc.want.State || got.Summary != tc.want.Summary || got.Reason != tc.want.Reason {
			t.Errorf("%s: Check() = %+v, want %+v", tc.name, got, tc.want)
		}
		if tc.want.State == check.Attention {
			want := check.Item{ID: "tool:tool", Name: "Installs the tool", State: steps.Problem, Detail: "kit apply tool", Action: steps.ActionRun}
			if len(got.Items) != 1 || got.Items[0] != want {
				t.Errorf("%s: items %+v, want %+v", tc.name, got.Items, want)
			}
		}
		cmds := fake.Commands()
		if len(cmds) != 1 || cmds[0].Dir != s.Dir || cmds[0].Timeout != time.Minute {
			t.Errorf("%s: ran %+v, want the script in its folder, given a minute", tc.name, cmds)
		}
	}
	step := steps.ScriptStep(runnertest.New(t), &steps.Admin{}, s)
	if step.Name != "tool" || step.Title != "tool" || step.Area != steps.AreaSteps || !step.Admin || !slices.Equal(step.After, []string{"brew"}) || !slices.Equal(step.Before, []string{"fonts"}) {
		t.Errorf("step = %+v", step)
	}
}

func TestScriptStepWithoutItsScript(t *testing.T) {
	s := script(t, steps.Script{Does: "Installs the tool"})
	run := filepath.Join(s.Dir, "run")
	if err := os.Chmod(run, 0o644); err != nil {
		t.Fatal(err)
	}
	fake := runnertest.New(t)
	if got := steps.ScriptStep(fake, nil, s).Check(t.Context()); got.State != check.Failed || got.Reason != "laptop/steps/tool/run can't be run: chmod +x it" {
		t.Errorf("not executable: Check() = %+v", got)
	}
	if err := os.Remove(run); err != nil {
		t.Fatal(err)
	}
	if got := steps.ScriptStep(fake, nil, s).Check(t.Context()); got.State != check.Failed || got.Reason != "laptop/steps/tool/run isn't there" {
		t.Errorf("missing: Check() = %+v", got)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("ran %q", calls)
	}
}

func TestScriptStepApplies(t *testing.T) {
	s := script(t, steps.Script{Does: "Installs the tool"})
	run := filepath.Join(s.Dir, "run")
	fake := runnertest.New(t)
	fake.On(run, "apply").Prints("Downloading\nInstalled\n")
	fake.On(run, "check").Prints("installed, 2.1\n")
	if err := steps.ScriptStep(fake, nil, s).Apply(t.Context(), check.Result{}); err != nil {
		t.Errorf("Apply() = %v", err)
	}
	cmds := fake.Commands()
	if len(cmds) != 2 || cmds[0].String() != run+" apply" || cmds[0].Dir != s.Dir || cmds[0].Timeout != 15*time.Minute || cmds[1].String() != run+" check" {
		t.Errorf("ran %+v, want apply in its folder, given 15 minutes, then the check", cmds)
	}
}

// A script that fails to apply says it couldn't, then why, in the words
// it last said, never its path: a shell's own error without the lead
// naming the script and its line.
func TestScriptStepApplyFails(t *testing.T) {
	s := script(t, steps.Script{Does: "Installs the tool"})
	run := filepath.Join(s.Dir, "run")
	for _, tc := range []struct {
		name   string
		answer func(apply, checking *runnertest.Script)
		want   string
	}{
		{"says why", func(apply, _ *runnertest.Script) {
			apply.Exits(1).Prints("Downloading\n").PrintsToStderr("curl: (6) Could not resolve host\n\n")
		}, "couldn't apply: curl: (6) Could not resolve host"},
		{"the shell says why", func(apply, _ *runnertest.Script) {
			apply.Exits(127).PrintsToStderr(run + ": line 53: gpg: command not found\n")
		}, "couldn't apply: gpg: command not found"},
		{"the shell says why, without a line", func(apply, _ *runnertest.Script) {
			apply.Exits(1).PrintsToStderr(run + ": TOKEN: unbound variable\n")
		}, "couldn't apply: TOKEN: unbound variable"},
		{"says it last", func(apply, _ *runnertest.Script) { apply.Exits(1).Prints("Downloading\nno space left\n") }, "couldn't apply: no space left"},
		{"says nothing", func(apply, _ *runnertest.Script) { apply.Exits(3) }, "couldn't apply: it exited 3, saying nothing"},
		{"isn't believed", func(_, checking *runnertest.Script) { checking.Exits(1).Prints("not installed\n") }, "couldn't apply: its check still says not installed"},
		{"breaks its check", func(_, checking *runnertest.Script) { checking.Exits(4).Prints("can't tell\n") }, "couldn't apply: its check still says can't tell"},
	} {
		fake := runnertest.New(t)
		checking := fake.On(run, "check")
		tc.answer(fake.On(run, "apply"), checking)
		err := steps.ScriptStep(fake, nil, s).Apply(t.Context(), notDone())
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: Apply() = %v, want %q", tc.name, err, tc.want)
		}
	}
}

// notDone is what a check found before applying.
func notDone() check.Result {
	return check.Result{State: check.Attention}
}

func TestScriptStepWaitsForThePassword(t *testing.T) {
	s := script(t, steps.Script{Does: "Trusts the authority", Admin: true})
	run := filepath.Join(s.Dir, "run")
	fake := runnertest.New(t)
	admin := &steps.Admin{Held: func(context.Context) bool { return false }}
	if err := steps.ScriptStep(fake, admin, s).Apply(t.Context(), notDone()); err == nil || err.Error() != steps.AdminWait {
		t.Errorf("without the password, Apply() = %v", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("without the password, ran %q", calls)
	}
	fake.On(run, "apply")
	fake.On(run, "check")
	admin.Held = func(context.Context) bool { return true }
	if err := steps.ScriptStep(fake, admin, s).Apply(t.Context(), notDone()); err != nil {
		t.Errorf("with the password, Apply() = %v", err)
	}
}

// needing is s needing TOKEN, which ready says is in place, its value as
// kit's secrets hold it.
func needing(s steps.Script, ready bool) steps.Script {
	s.Secrets = []string{"TOKEN"}
	s.Needed = []engine.Wait{{Step: "secret", Thing: "TOKEN", Ready: func(context.Context) bool { return ready }}}
	s.Env = func() ([]string, error) { return []string{"TOKEN=plain-words-value"}, nil }
	return s
}

// A step needing a secret runs its script with the secret set, check and
// apply alike, its value hidden in what the script says; it's applied once
// the secret's in place, and not before, saying what isn't there.
func TestScriptStepNeedingASecret(t *testing.T) {
	s := needing(script(t, steps.Script{Does: "Fetches releases"}), true)
	run := filepath.Join(s.Dir, "run")
	fake := runnertest.New(t)
	fake.On(run, "check").Exits(1).Prints("not fetched with plain-words-value\n").Then().Prints("fetched\n")
	fake.On(run, "apply").Exits(1).PrintsToStderr("denied: plain-words-value\n").Then()
	step := steps.ScriptStep(fake, nil, s)
	if step.Later == nil || step.Later[0].Thing != "TOKEN" {
		t.Errorf("Later = %+v, want the wait for TOKEN", step.Later)
	}
	found := step.Check(t.Context())
	if found.Summary != "not fetched with [redacted]" {
		t.Errorf("Check() = %+v, want what it said, its secret hidden", found)
	}
	if err := step.Apply(t.Context(), found); err == nil || err.Error() != "couldn't apply: denied: [redacted]" {
		t.Errorf("Apply() = %v, want its failure, its secret hidden", err)
	}
	if err := step.Apply(t.Context(), found); err != nil {
		t.Errorf("Apply() = %v", err)
	}
	for _, c := range fake.Commands() {
		if !slices.Equal(c.Env, []string{"TOKEN=plain-words-value"}) {
			t.Errorf("%s ran without TOKEN set, alone", c)
		}
	}

	s = needing(s, false)
	fake = runnertest.New(t)
	err := steps.ScriptStep(fake, nil, s).Apply(t.Context(), notDone())
	if err == nil || err.Error() != "needs TOKEN, which isn't there yet" || len(fake.Calls()) != 0 {
		t.Errorf("without TOKEN, Apply() = %v, running %q; want it not applied, saying why", err, fake.Calls())
	}
}

func TestBrokenScript(t *testing.T) {
	e := config.Entry{Name: "tool", Scope: "laptop", Line: 12}
	step := steps.BrokenScript(e, os.ErrInvalid)
	got := step.Check(t.Context())
	if step.Name != "tool" || step.Area != steps.AreaSteps || step.Apply != nil || got.State != check.Failed || got.Reason != "laptop/declarations:12: "+os.ErrInvalid.Error() {
		t.Errorf("BrokenScript = %+v, Check() = %+v", step, got)
	}
}
