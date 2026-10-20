package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/nightly"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

// Every step kit makes has a row of its own on applying's screen, never
// the one its area falls back on: each kind, each of kit's own steps, the
// boot's, and the user's own, made as kit makes them.
func TestEveryStepHasARow(t *testing.T) {
	fake := runnertest.New(t)
	home, state, configDir := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, config.File), []byte("format = 1\nprimary = \"laptop\"\nterminal = \"ghostty\"\n[macs.laptop]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	never := func() time.Time { return time.Time{} }
	admin := &steps.Admin{}

	made := []engine.Step{{Name: linked.StepName, Area: steps.AreaDrift}}
	deps := &kind.Deps{Run: fake, Home: home, StateDir: state}
	for _, d := range kinds.All {
		made = append(made, engine.Step{Name: d.New(deps).Name(), Area: steps.AreaDrift})
		if d.Setup != nil {
			made = append(made, d.Setup(deps)...)
		}
	}
	made = slices.Concat(made, []engine.Step{
		steps.PathFile(nil, ""), steps.OhMyZsh(fake, home),
		steps.TouchIDSudo(fake, admin, ""), steps.RemoteLogin(fake, admin), steps.FileSharing(fake, admin),
		steps.TimeMachine(fake, never), steps.Arq(fake, never), steps.Prefs(nil, never), steps.FullDiskAccess(home),
		steps.Scratch(fake, admin, "", home, 501, nil),
		steps.ConfigEdits(gitrepo.Repo{}), steps.ConfigSync(fake, "", never), steps.ConfigPrivate(fake, ""),
		steps.Disk(fake), steps.Memory(fake), steps.FileEvents(fake), steps.Load(fake, never),
		steps.Own(fake, home, config.List{}),
		steps.Manual(fake, home, state, config.List{}, nil), steps.OwnManual(fake, state, steps.ManualStep{Name: "sign-in"}, nil),
		steps.ScriptStep(fake, admin, steps.Script{Name: "fonts"}), steps.BrokenScript(config.Entry{Name: "gpg-key"}, errors.New("unread")),
		nightly.Check(nil, true, true, state, never),
	})
	// kit.toml naming a terminal, the boot makes every step it has.
	booted := (&boot.Boot{Config: configDir}).Booted()
	if !slices.ContainsFunc(booted, func(s engine.Step) bool { return s.Name == "boot-"+boot.TerminalStep }) {
		t.Fatalf("the boot made %d steps, not the hand-off to the terminal: want every one", len(booted))
	}
	made = append(made, booted...)

	for _, s := range made {
		if row, known := render.RowOf(event.Step{Name: s.Name, Area: s.Area}); !known {
			t.Errorf("%s (area %q) falls back on %s: give it a row", s.Name, s.Area, row)
		}
	}
}
