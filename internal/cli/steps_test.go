package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stepScript writes an executable run script for scope's step called name,
// returning its full path.
func (w *world) stepScript(t *testing.T, scope, name string) string {
	t.Helper()
	path := filepath.Join(".config", "kit", scope, "steps", name, "run")
	w.write(t, path, "#!/bin/bash\n")
	full := filepath.Join(w.home, path)
	if err := os.Chmod(full, 0o755); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestAStepOfYourOwn(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "steps", `tool "Installs the tool" --after brew`+"\n")
	run := w.stepScript(t, "laptop", "tool")
	w.fake.On(run, "check").Exits(1).Prints("not installed\n").
		Then().Exits(1).Prints("not installed\n").
		Then().Exits(1).Prints("not installed\n").
		Then().Prints("installed, 2.1\n")
	out, _, code := w.run(t, "status", "tool")
	if code != 1 || !strings.Contains(out, "tool attention not installed\n") || !strings.Contains(out, "Installs the tool") || !strings.Contains(out, "kit apply tool") {
		t.Errorf("kit status tool printed\n%s exit %d", out, code)
	}
	if out, _, _ := w.run(t, "status"); !strings.Contains(out, "tool attention not installed\n") {
		t.Errorf("kit status printed\n%s", out)
	}
	w.fake.On(run, "apply").Prints("Downloading\nInstalled\n")
	out, _, code = w.run(t, "apply", "tool")
	if code != 0 || !strings.Contains(out, "tool ok installed, 2.1\n") {
		t.Errorf("kit apply tool printed\n%s exit %d", out, code)
	}
	if out, _, _ := w.run(t, "status"); !strings.Contains(out, "tool ok installed, 2.1\n") {
		t.Errorf("after applying, kit status printed\n%s", out)
	}
	if out, _, _ := w.run(t, "apply", "tool"); strings.Count(strings.Join(w.fake.Calls(), "\n"), run+" apply") != 1 {
		t.Errorf("a step that's done was applied again:\n%s", out)
	}
}

func TestAStepThatDoesntApply(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "steps", `tool "Installs the tool"`+"\n")
	run := w.stepScript(t, "laptop", "tool")
	w.fake.On(run, "check").Exits(1).Prints("not installed\n")
	w.fake.On(run, "apply").Exits(1).Prints("Downloading\n").PrintsToStderr("curl: (6) Could not resolve host\n")
	if out, _, code := w.run(t, "apply", "tool"); code != 1 || !strings.Contains(out, "curl: (6) Could not resolve host") {
		t.Errorf("kit apply tool printed\n%s exit %d", out, code)
	}
	w.fake.On(run, "apply")
	if out, _, code := w.run(t, "apply", "tool"); code != 1 || !strings.Contains(out, "applied, but its check still says: not installed") {
		t.Errorf("an apply its check doesn't believe printed\n%s exit %d", out, code)
	}
}

func TestStepsThatDontRead(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "steps", strings.Join([]string{
		`odd "Does something" --sideways`,
		`lost "Comes before what isn't here" --before nothing --after brw`,
		`later "Comes after a kind this Mac doesn't use" --after mas`,
		`gone "Has no script"`,
	}, "\n")+"\n")
	later := w.stepScript(t, "laptop", "later")
	w.fake.On(later, "check")
	out, _, code := w.run(t, "status")
	for _, want := range []string{
		`odd failed laptop/declarations:`,
		`"--sideways" isn't one of a step's options: --after, --before and --sudo`,
		"lost failed laptop/declarations:",
		"it comes after brw, which isn't a step; it comes before nothing, which isn't a step",
		"later ok done",
		"gone failed laptop/steps/gone/run isn't there",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kit status printed\n%s\nexit %d, want %q", out, code, want)
		}
	}
	w.writeSection(t, "laptop", "steps", `brew "Shadows kit's own"`+"\n")
	if _, errOut, code := w.run(t, "status"); code != 2 || !strings.Contains(errOut, "laptop/declarations:") || !strings.Contains(errOut, "a step of your own can't be called brew, as one of kit's is: rename it") {
		t.Errorf("a step named as kit's printed %q, exit %d", errOut, code)
	}
}

func TestAStepWaitsForThePassword(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "steps", `trust "Trusts the authority" --sudo`+"\n")
	run := w.stepScript(t, "laptop", "trust")
	w.fake.On(run, "check").Exits(1).Prints("not trusted\n")
	w.fake.On("sudo", "-n", "true").Exits(1)
	if out, _, code := w.run(t, "apply", "trust"); code != 1 || !strings.Contains(out, "waiting for an administrator's password") {
		t.Errorf("kit apply trust printed\n%s exit %d", out, code)
	}
}

func TestAddAndRemoveAStep(t *testing.T) {
	w := laptopWorld(t)
	w.expectSync([]string{"laptop/declarations", "laptop/steps/tool"}, "kit step add tool (laptop): from its maker")
	out, _, code := w.run(t, "step", "add", "tool", "Installs the tool, from its maker", "--after", "brew", "--sudo", "--note", "from its maker")
	if code != 0 || !strings.Contains(out, "tool ok declared in laptop; its script, laptop/steps/tool/run, is a template: write its check and apply") {
		t.Errorf("kit step add printed\n%s exit %d", out, code)
	}
	if got := w.readSection(t, "laptop", "steps"); got != `tool "Installs the tool, from its maker" --after brew --sudo   # from its maker`+"\n" {
		t.Errorf("[steps] = %q", got)
	}
	script := filepath.Join(w.home, ".config", "kit", "laptop", "steps", "tool", "run")
	info, err := os.Stat(script)
	if err != nil || info.Mode().Perm() != 0o755 || !strings.HasPrefix(w.read(t, filepath.Join(".config", "kit", "laptop", "steps", "tool", "run")), "#!/bin/bash\n# tool: Installs the tool, from its maker\n") {
		t.Errorf("its script: %v, %v", info, err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"tool", "Again"}, "a step called tool is there already"},
		{[]string{"brew", "Shadows kit's own"}, "a step called brew is there already"},
		{[]string{"other", "Comes after what isn't here", "--after", "nothing"}, "it comes after nothing, which isn't a step"},
		{[]string{"other", "Comes before what isn't here", "--before", "nothing"}, "it comes before nothing, which isn't a step"},
	} {
		out, _, code := w.run(t, append([]string{"step", "add"}, tc.args...)...)
		if code != 1 || !strings.Contains(out, tc.want) {
			t.Errorf("kit step add %q printed\n%s exit %d, want %q", tc.args, out, code, tc.want)
		}
	}
	w.expectSync([]string{"laptop/declarations", "laptop/steps/tool"}, "kit step remove tool (laptop)")
	if out, _, code := w.run(t, "step", "remove", "tool"); code != 0 || w.readSection(t, "laptop", "steps") != "" {
		t.Errorf("kit step remove printed\n%s exit %d", out, code)
	}
	if _, err := os.Stat(filepath.Dir(script)); !os.IsNotExist(err) {
		t.Errorf("its folder is still there: %v", err)
	}
}
