package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/runner"
)

// Model is what this Mac is, as macOS names its model, as in MacBook Pro:
// "Mac" when it can't be told.
func Model(ctx context.Context, run runner.Runner) string {
	res, err := run.Run(ctx, runner.Command{Name: "system_profiler", Args: []string{"SPHardwareDataType", "-json"}, Timeout: 30 * time.Second})
	if err != nil {
		return "Mac"
	}
	var hw struct {
		Hardware []struct {
			Name string `json:"machine_name"`
		} `json:"SPHardwareDataType"`
	}
	if json.Unmarshal(res.Stdout, &hw) != nil || len(hw.Hardware) == 0 || hw.Hardware[0].Name == "" {
		return "Mac"
	}
	return hw.Hardware[0].Name
}

// Suggest is the name suggested for a Mac that's model, as in macbook-pro:
// its model's words, in lower case, joined by dashes, numbered when taken
// says that name is.
func Suggest(model string, taken func(string) bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(model) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	name := strings.TrimSuffix(b.String(), "-")
	if name == "" {
		name = "mac"
	}
	for n, try := 2, name; ; n++ {
		if !taken(try) {
			return try
		}
		try = name + "-" + strconv.Itoa(n)
	}
}

// macStep names this Mac, the name recorded before it's written anywhere:
// in a config starting on this Mac, its one Mac, written in its first
// files; in one joined, one of its Macs, or a new one, added as kit machine
// add adds it. Done when kit has this Mac's name, as setup recorded it, and
// the config knows it. A --mac or --from other than recorded is refused
// (sameMac).
func (s *Setup) macStep() engine.Step {
	return engine.Step{
		Name: MacStep, Title: "This Mac",
		Check: func(context.Context) check.Result {
			rec := s.record()
			if err := s.sameMac(rec); err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			name, err := config.ReadMachine(s.Dirs.State)
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			if rec.Mac == "" || name != rec.Mac {
				return check.Result{State: check.Attention, Summary: "not named yet"}
			}
			cfg, err := s.config()
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			if !cfg.Knows(name) {
				return check.Result{State: check.Attention, Summary: "named " + name + ", which kit-config doesn't know"}
			}
			if rec.NewMac {
				return check.Result{State: check.OK, Summary: says(name, "new")}
			}
			return check.Result{State: check.OK, Summary: name}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if err := s.sameMac(s.record()); err != nil {
				return err
			}
			if s.record().Mac == "" {
				if err := s.name(ctx); err != nil {
					return err
				}
			}
			rec := s.record()
			if err := s.known(rec); err != nil {
				return err
			}
			return config.WriteMachine(s.Dirs.State, rec.Mac)
		},
	}
}

// name names this Mac, recording its name, whether it's new to a config
// joined, and the Mac it starts as a copy of, if any.
func (s *Setup) name(ctx context.Context) error {
	if s.record().Empty {
		name, err := s.newName(ctx, func(string) bool { return false })
		if err != nil {
			return err
		}
		return s.update(func(r *record) { r.Mac = name })
	}
	cfg, err := s.config()
	if err != nil {
		return err
	}
	name, from, err := s.whichMac(ctx, cfg)
	if err != nil {
		return err
	}
	return s.update(func(r *record) { r.Mac, r.NewMac, r.From = name, !cfg.Knows(name), from })
}

// known has kit-config know rec's Mac: written in the first files of a
// config starting on this Mac, unless they're there; added to one joined,
// unless it's there. The terminal is Ghostty when setup runs in it.
func (s *Setup) known(rec record) error {
	if !rec.Empty {
		cfg, err := s.config()
		if err != nil || cfg.Knows(rec.Mac) {
			return err
		}
		return cfg.AddMac(rec.Mac, rec.From, s.Model)
	}
	if _, err := os.Stat(filepath.Join(s.Dirs.Config, config.File)); err == nil {
		return nil
	}
	f := config.First{Mac: rec.Mac, Description: s.Model, Kit: s.Version}
	if t, ok := apps.Running(s.Getenv); ok && t.Name == "ghostty" {
		f.Terminal = t.Name
	}
	return config.Start(s.Dirs.Config, f)
}

// newName is a new Mac's name: --mac's, else asked, suggested from its
// model, none taken already.
func (s *Setup) newName(ctx context.Context, taken func(string) bool) (string, error) {
	suggestion := Suggest(s.Model, taken)
	name := s.Given.Mac
	if name == "" {
		if s.Ask == nil {
			return "", s.unanswered("--mac <name> names this Mac (" + suggestion + ", say)")
		}
		var err error
		if name, err = s.Ask.Text(ctx, MacStep, "what will kit-config call it?", suggestion); err != nil {
			return "", err
		}
	}
	return name, config.NewMacName(name)
}

// whichMac is which of cfg's Macs this is, or a new one's name and the Mac
// it starts as a copy of, if any: as --mac and --from say, else asked.
func (s *Setup) whichMac(ctx context.Context, cfg *config.Config) (name, from string, err error) {
	g := s.Given
	switch {
	case g.Mac != "" && cfg.Knows(g.Mac) && g.From != "":
		return "", "", fmt.Errorf("--from starts a new Mac as a copy of another, and %s is one of kit-config's Macs already", g.Mac)
	case g.Mac != "" && cfg.Knows(g.Mac):
		return g.Mac, "", nil
	case g.Mac == "" && s.Ask == nil:
		return "", "", s.unanswered("--mac <name> names this Mac: one of " + look.InWords(cfg.MacNames()) + ", or a new one's (" + Suggest(s.Model, cfg.Knows) + ", say), with --from <mac> to start it as a copy of one")
	}
	if g.Mac == "" {
		macs := cfg.MacNames()
		answers := make([]boot.Answer, 0, len(macs)+1)
		for _, m := range macs {
			answers = append(answers, boot.Answer{Label: m, Does: cfg.Macs[m].Description})
		}
		answers = append(answers, boot.Answer{Label: "A new one", Does: "a Mac of its own in kit-config"})
		i, err := s.Ask.Choose(ctx, MacStep, "which of your Macs is this?", answers)
		switch {
		case err != nil:
			return "", "", err
		case i < len(macs):
			return macs[i], "", nil
		}
	}
	if name, err = s.newName(ctx, cfg.Knows); err != nil {
		return "", "", err
	}
	if cfg.Knows(name) {
		return "", "", fmt.Errorf("kit-config has a Mac named %s already: choose it, or name this one otherwise", name)
	}
	from, err = s.startsFrom(ctx, cfg)
	return name, from, err
}

// startsFrom is the Mac a new one starts as a copy of: --from's, else
// asked at a terminal; none without one.
func (s *Setup) startsFrom(ctx context.Context, cfg *config.Config) (string, error) {
	macs := cfg.MacNames()
	if s.Given.From != "" || s.Ask == nil || len(macs) == 0 {
		return s.Given.From, nil
	}
	starts := []boot.Answer{{Label: "Fresh", Does: "only what every Mac shares, and what this one has"}}
	for _, m := range macs {
		starts = append(starts, boot.Answer{Label: m, Does: fmt.Sprintf("a copy of %s's setup to start from", m)})
	}
	i, err := s.Ask.Choose(ctx, MacStep, "where does it start from?", starts)
	if err != nil || i == 0 {
		return "", err
	}
	return macs[i-1], nil
}
