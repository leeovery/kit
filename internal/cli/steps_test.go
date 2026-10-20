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
	if out, _, code := w.run(t, "apply", "tool"); code != 1 || !strings.Contains(out, "couldn't apply: its check still says not installed") {
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
		`"--sideways" isn't one of a step's options: --after, --before, --secret and --sudo`,
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

// Added for every Mac, a step the Macs declare moves with its folder, in
// one edit and commit: theirs, the same, goes to the shared one, and none is
// left behind. Their folders differing, or a shared one there otherwise,
// it's refused, nothing changed.
func TestAddAStepSharedMovesItsFolder(t *testing.T) {
	line := `tool "Installs the tool" --after brew` + "\n"
	setUp := func(t *testing.T, studio, shared string) *world {
		w := laptopWorld(t)
		w.writeSection(t, "laptop", "steps", line)
		w.writeSection(t, "studio", "steps", line)
		w.stepScript(t, "laptop", "tool")
		w.write(t, filepath.Join(".config", "kit", "studio", "steps", "tool", "run"), studio)
		if err := os.Chmod(filepath.Join(w.home, ".config", "kit", "studio", "steps", "tool", "run"), 0o755); err != nil {
			t.Fatal(err)
		}
		if shared != "" {
			w.write(t, filepath.Join(".config", "kit", "shared", "steps", "tool", "run"), shared)
		}
		return w
	}
	w := setUp(t, "#!/bin/bash\n", "")
	w.expectSync([]string{"laptop/declarations", "laptop/steps/tool", "shared/declarations", "shared/steps/tool", "studio/declarations", "studio/steps/tool"}, "kit step add tool (laptop)")
	out, errOut, code := w.run(t, "step", "add", "tool", "Installs the tool", "--after", "brew", "--shared")
	if code != 0 || !strings.Contains(out, "tool ok declared in shared, out of laptop and studio; its folder moved from laptop/steps/tool and studio/steps/tool") {
		t.Fatalf("kit step add --shared printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "shared", "steps"); got != line {
		t.Errorf("shared [steps] = %q", got)
	}
	if got := w.read(t, filepath.Join(".config", "kit", "shared", "steps", "tool", "run")); got != "#!/bin/bash\n" {
		t.Errorf("shared/steps/tool/run = %q, want the Macs' script", got)
	}
	for _, mac := range []string{"laptop", "studio"} {
		if _, err := os.Stat(filepath.Join(w.home, ".config", "kit", mac, "steps", "tool")); !os.IsNotExist(err) {
			t.Errorf("%s/steps/tool is still there: %v", mac, err)
		}
		if got := w.readSection(t, mac, "steps"); got != "" {
			t.Errorf("%s's [steps] = %q, want tool out of it", mac, got)
		}
	}

	// A Mac's folder git never kept is moved, and left out of the commit,
	// git having nothing of it to take out.
	w = setUp(t, "#!/bin/bash\n", "")
	dir := filepath.Join(w.home, ".config", "kit")
	git := func(args ...string) []string { return append([]string{"-C", dir}, args...) }
	all := []string{"laptop/declarations", "laptop/steps/tool", "shared/declarations", "shared/steps/tool", "studio/declarations", "studio/steps/tool"}
	kept := []string{"laptop/declarations", "shared/declarations", "shared/steps/tool", "studio/declarations", "studio/steps/tool"}
	w.fake.On("git", git(append([]string{"status", "--porcelain", "--"}, all...)...)...).Prints(" M laptop/declarations\n M shared/declarations\n?? shared/steps/tool/run\n M studio/declarations\n D studio/steps/tool/run\n")
	w.fake.On("git", git(append([]string{"add", "--"}, kept...)...)...)
	w.fake.On("git", git(append([]string{"commit", "--quiet", "-m", "kit step add tool (laptop)", "--"}, kept...)...)...)
	w.fake.On("git", git("remote")...).Prints("origin\n")
	w.fake.On("git", git("pull", "--rebase", "--autostash", "--quiet")...)
	w.fake.On("git", git("push", "--quiet")...)
	if out, errOut, code := w.run(t, "step", "add", "tool", "Installs the tool", "--after", "brew", "--shared"); code != 0 || !strings.Contains(out, "kit-config ok committed and pushed") {
		t.Errorf("kit step add --shared, laptop's folder never committed, printed\n%s%s exit %d", out, errOut, code)
	}

	for _, c := range []struct {
		name, studio, shared, want string
	}{
		{"the Macs' folders differ", "#!/bin/bash\necho studio\n", "", "tool failed laptop/steps/tool and studio/steps/tool differ: make them the same, or take one out first"},
		{"a shared folder otherwise", "#!/bin/bash\n", "#!/bin/bash\necho shared\n", "tool failed laptop/steps/tool and shared/steps/tool differ: make them the same, or take one out first"},
	} {
		w := setUp(t, c.studio, c.shared)
		before := map[string]string{}
		for _, path := range []string{"shared/declarations", "laptop/declarations", "studio/declarations", "laptop/steps/tool/run", "studio/steps/tool/run"} {
			before[path] = w.read(t, filepath.Join(".config", "kit", path))
		}
		out, _, code := w.run(t, "step", "add", "tool", "Installs the tool", "--after", "brew", "--shared")
		if code != 1 || !strings.Contains(out, c.want) {
			t.Errorf("%s: kit step add --shared printed\n%s exit %d; want %q", c.name, out, code, c.want)
		}
		for path, was := range before {
			if got := w.read(t, filepath.Join(".config", "kit", path)); got != was {
				t.Errorf("%s: %s = %q, want it untouched", c.name, path, got)
			}
		}
	}
}
