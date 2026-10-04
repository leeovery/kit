package cli_test

import (
	"path/filepath"
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
