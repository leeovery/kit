package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var machineFile = filepath.Join(".local", "state", "kit", "machine")

func TestMachineSetsAndShowsTheName(t *testing.T) {
	w := newWorld(t, map[string]string{"kit.toml": twoMacs})

	steps := []struct {
		args       []string
		wantOut    string
		wantStatus int
	}{
		{args: []string{"machine", "laptop"}, wantOut: "this Mac is laptop\n"},
		{args: []string{"machine"}, wantOut: "laptop\n"},
		{args: []string{"machine", "laptop"}, wantOut: "this Mac is laptop already\n"},
		{args: []string{"machine", "studio"}, wantOut: "this Mac was laptop; it's studio now\n"},
		{args: []string{"machine"}, wantOut: "studio\n"},
	}
	for _, s := range steps {
		out, errOut, status := w.run(t, s.args...)
		if out != s.wantOut || errOut != "" || status != s.wantStatus {
			t.Errorf("kit %v printed %q, %q, exit %d; want %q, exit %d", s.args, out, errOut, status, s.wantOut, s.wantStatus)
		}
	}
	if got := w.read(t, machineFile); got != "studio\n" {
		t.Errorf("the machine file holds %q, want studio", got)
	}
}

func TestMachineWithoutAName(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{name: "with a config", files: map[string]string{"kit.toml": twoMacs}, want: "kit: this Mac has no name yet: run kit machine <name>, one of laptop, studio\n"},
		{name: "without one", files: map[string]string{}, want: "kit: this Mac has no name yet: run kit machine <name>\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errOut, status := newWorld(t, tt.files).run(t, "machine")
			if out != "" || errOut != tt.want || status != 1 {
				t.Errorf("kit machine printed %q, %q, exit %d; want %q, exit 1", out, errOut, status, tt.want)
			}
		})
	}
}

func TestMachineRefusesAnUnknownName(t *testing.T) {
	w := newWorld(t, map[string]string{"kit.toml": twoMacs})
	out, errOut, status := w.run(t, "machine", "other")
	if want := "kit: no Mac named other in kit.toml: one of laptop, studio\n"; out != "" || errOut != want || status != 2 {
		t.Errorf("kit machine other printed %q, %q, exit %d; want %q, exit 2", out, errOut, status, want)
	}
	if got := w.read(t, machineFile); got != "" {
		t.Errorf("the machine file holds %q, want none", got)
	}
}

func TestMachineNeedsAConfigToSetTheName(t *testing.T) {
	w := newWorld(t, map[string]string{})
	_, errOut, status := w.run(t, "machine", "laptop")
	want := "kit: no config repository: no kit.toml in " + filepath.Join(w.home, ".config", "kit") +
		": clone your config repository to " + filepath.Join(w.home, ".config", "kit") + ", or point KIT_CONFIG at it\n"
	if errOut != want || status != 2 {
		t.Errorf("kit machine laptop printed %q, exit %d; want %q, exit 2", errOut, status, want)
	}
}

func TestMachineFollowsKITCONFIG(t *testing.T) {
	w := newWorld(t, map[string]string{})
	w.write(t, filepath.Join("kit-config", "kit.toml"), twoMacs)
	w.env["KIT_CONFIG"] = filepath.Join(w.home, "kit-config")

	if out, errOut, status := w.run(t, "machine", "studio"); out != "this Mac is studio\n" || status != 0 {
		t.Errorf("kit machine studio printed %q, %q, exit %d; want it set from KIT_CONFIG's config", out, errOut, status)
	}
}

func TestMachineRefusesAConfigNeedingALaterKit(t *testing.T) {
	w := newWorld(t, map[string]string{"kit.toml": "minimum_kit = \"0.2.0\"\n" + twoMacs})
	_, errOut, status := w.run(t, "machine", "laptop")
	if want := "kit: the config needs kit 0.2.0 or later, and this is 0.1.0: update kit\n"; errOut != want || status != 2 {
		t.Errorf("kit machine laptop printed %q, exit %d; want %q, exit 2", errOut, status, want)
	}
}

// Added from another Mac, a Mac's folder is a copy of that one's, every file
// as it is, and kit.toml gains its table after the last Mac's, comments and
// all left as they were; this Mac's name stays.
func TestMachineAddCopiesAMac(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, filepath.Join(".config", "kit", "kit.toml"), twoMacs+"\n# Kept.\n")
	run := w.stepScript(t, "studio", "tool")
	w.expectCommit(t, commit{
		message: "kit machine add mini (laptop)", paths: []string{"kit.toml", "mini"},
		status: " M kit.toml\x00?? mini/declarations\x00?? mini/steps/tool/run\x00",
		update: []string{"kit.toml", "mini/declarations", "mini/steps/tool/run"},
		staged: "kit.toml\x00mini/declarations\x00mini/steps/tool/run\x00",
	})
	out, errOut, code := w.run(t, "machine", "add", "mini", "--from", "studio", "--description", `Mac "mini"`)
	if code != 0 || !strings.Contains(out, "mini ok added to kit-config, a copy of studio") {
		t.Fatalf("kit machine add printed\n%s%s exit %d", out, errOut, code)
	}
	want := strings.Replace(twoMacs+"\n# Kept.\n", "\n# Kept.", "\n[macs.mini]\ndescription = \"Mac \\\"mini\\\"\"\n\n# Kept.", 1)
	if got := w.read(t, filepath.Join(".config", "kit", "kit.toml")); got != want {
		t.Errorf("kit.toml =\n%s\nwant\n%s", got, want)
	}
	if got, want := w.read(t, filepath.Join(".config", "kit", "mini", "declarations")), "[homebrew formulae]\nffmpeg\n"; got != want {
		t.Errorf("mini/declarations = %q, want studio's, %q", got, want)
	}
	copied := filepath.Join(w.home, ".config", "kit", "mini", "steps", "tool", "run")
	if info, err := os.Stat(copied); err != nil || info.Mode().Perm() != 0o755 || w.read(t, strings.TrimPrefix(copied, w.home)) != w.read(t, strings.TrimPrefix(run, w.home)) {
		t.Errorf("mini's script: %v, %v; want studio's, executable", info, err)
	}
	if got := w.read(t, machineFile); got != "laptop\n" {
		t.Errorf("this Mac's name is %q, want laptop still", got)
	}
}

func TestMachineAddMakesAnEmptyMac(t *testing.T) {
	w := laptopWorld(t)
	w.expectCommit(t, commit{
		message: "kit machine add mini (laptop)", paths: []string{"kit.toml", "mini"},
		status: " M kit.toml\x00?? mini/declarations\x00", update: []string{"kit.toml", "mini/declarations"},
		staged: "kit.toml\x00mini/declarations\x00",
	})
	out, errOut, code := w.run(t, "machine", "add", "mini")
	if code != 0 || !strings.Contains(out, "mini ok added to kit-config\n") {
		t.Fatalf("kit machine add printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, filepath.Join(".config", "kit", "kit.toml")); got != twoMacs+"\n[macs.mini]\n" {
		t.Errorf("kit.toml =\n%s", got)
	}
	if info, err := os.Stat(filepath.Join(w.home, ".config", "kit", "mini", "declarations")); err != nil || info.Size() != 0 {
		t.Errorf("mini/declarations: %v, %v; want it empty", info, err)
	}
	if _, errOut, code := w.run(t, "machine", "mini"); errOut != "" || code != 0 {
		t.Errorf("kit machine mini printed %q, exit %d; want the new Mac known", errOut, code)
	}
}

func TestMachineAddRefuses(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"studio"}, "kit: kit.toml knows a Mac named studio already\n"},
		{[]string{"mini", "--from", "other"}, "kit: no Mac named other in kit.toml: one of laptop, studio\n"},
		{[]string{"Mini"}, "kit: \"Mini\" can't be a Mac's name: use lower-case letters, digits and hyphens\n"},
		{[]string{"shared"}, "kit: a Mac can't be called shared: that's the file every Mac reads\n"},
		{[]string{"add"}, "kit: a Mac can't be called add: that's kit machine add\n"},
	} {
		w := laptopWorld(t)
		out, errOut, code := w.run(t, append([]string{"machine", "add"}, tc.args...)...)
		if out != "" || errOut != tc.want || code != 2 {
			t.Errorf("kit machine add %v printed %q, %q, exit %d; want %q, exit 2", tc.args, out, errOut, code, tc.want)
		}
		if got := w.read(t, filepath.Join(".config", "kit", "kit.toml")); got != twoMacs {
			t.Errorf("kit machine add %v left kit.toml\n%s", tc.args, got)
		}
	}
}
