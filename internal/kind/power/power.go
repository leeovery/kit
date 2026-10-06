// Package power is the Mac's power settings as a kind: each declared in a
// [power settings] section in pmset's form (-c sleep 0), compared with what
// pmset -g custom says, and applied through sudo. As with macOS settings, a
// setting kit has seen as declared and that differs now was changed on the
// Mac, so applying leaves it for kit reconcile.
package power

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/state"
)

// recordName names kit's record of the power settings it has seen as
// declared.
const recordName = "power.json"

type record struct {
	Seen map[string]bool `json:"seen,omitempty"`
}

// sections are pmset -g custom's headings, by the source they're for.
var sections = map[string]string{"AC Power:": "charger", "Battery Power:": "battery", "UPS Power:": "ups"}

// Power is the Mac's power settings, driven through pmset.
type Power struct {
	run      runner.Runner
	stateDir string
	declared map[string]string
	// seen are the declared settings kit has seen as declared.
	seen  map[string]bool
	admin func(ctx context.Context) bool
}

// New returns the power settings, run through run, with kit's records in
// stateDir.
func New(run runner.Runner, stateDir string) *Power {
	return &Power{run: run, stateDir: stateDir, declared: map[string]string{}}
}

func (p *Power) Name() string    { return "power" }
func (p *Power) Title() string   { return "Power settings" }
func (p *Power) Program() string { return "pmset" }

// Verb says a setting as declared is set.
func (p *Power) Verb() string { return "set" }

// Diverges reports whether a setting set otherwise was changed on purpose:
// whether kit has seen it as declared before.
func (p *Power) Diverges(name string) bool { return p.seen[name] }

// Values reads each setting's declared value.
func (p *Power) Values(list config.List) (config.List, error) {
	p.declared = make(map[string]string, len(list.Entries))
	for _, e := range list.Entries {
		p.declared[e.Name] = e.Value
	}
	return list, nil
}

// settings are the power settings as they are, by source and setting, as
// in charger:sleep. A Mac with one source, which pmset shows without a
// heading, has the charger's.
func (p *Power) settings(ctx context.Context) (map[string]string, error) {
	res, err := p.run.Run(ctx, runner.Command{Name: "pmset", Args: []string{"-g", "custom"}})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	source := "charger"
	for line := range strings.Lines(string(res.Stdout)) {
		line = strings.TrimSpace(line)
		if s, ok := sections[line]; ok {
			source = s
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		setting := strings.Join(fields[:len(fields)-1], " ")
		out[source+":"+setting] = fields[len(fields)-1]
	}
	return out, nil
}

// actual is a declared setting's value: for every source, its value when
// every source pmset shows has the same, else "".
func actual(settings map[string]string, name string) (string, bool) {
	source, setting, _ := strings.Cut(name, ":")
	if source != "all" {
		v, ok := settings[name]
		return v, ok
	}
	value, found := "", false
	for _, s := range slices.Sorted(maps.Keys(settings)) {
		if _, rest, _ := strings.Cut(s, ":"); rest == setting {
			if found && settings[s] != value {
				return "", true
			}
			value, found = settings[s], true
		}
	}
	return value, found
}

// Installed lists the declared settings pmset shows, and those kit has seen
// as declared before.
func (p *Power) Installed(ctx context.Context) ([]kind.Installed, error) {
	settings, err := p.settings(ctx)
	if err != nil {
		return nil, err
	}
	var out []kind.Installed
	err = state.Update(p.stateDir, recordName, func(rec *record) {
		if rec.Seen == nil {
			rec.Seen = map[string]bool{}
		}
		for _, name := range slices.Sorted(maps.Keys(p.declared)) {
			v, ok := actual(settings, name)
			if ok && v == p.declared[name] {
				rec.Seen[name] = true
			}
			if ok || rec.Seen[name] {
				out = append(out, kind.Installed{Name: name, Explicit: true})
			}
		}
		p.seen = maps.Clone(rec.Seen)
	})
	return out, err
}

// Resolve knows every setting.
func (p *Power) Resolve(_ context.Context, names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = name
	}
	return out, nil
}

// Differs says how each declared setting changed since kit saw it as
// declared is set now.
func (p *Power) Differs(ctx context.Context, names []string) (map[string]string, error) {
	settings, err := p.settings(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, name := range names {
		switch v, ok := actual(settings, name); {
		case !ok:
			out[name] = "not shown by pmset"
		case v == "":
			out[name] = "set differently on each power source"
		case v != p.declared[name]:
			out[name] = "set to " + v
		}
	}
	return out, nil
}

// Value is a setting's value as it is.
func (p *Power) Value(ctx context.Context, name string) (string, error) {
	settings, err := p.settings(ctx)
	if err != nil {
		return "", err
	}
	v, ok := actual(settings, name)
	if !ok || v == "" {
		return "", fmt.Errorf("%s has no one value to declare", name)
	}
	return v, nil
}

// flag is pmset's flag for a setting's source.
func flag(name string) string {
	source, _, _ := strings.Cut(name, ":")
	for f, s := range config.PowerSources {
		if s == source {
			return f
		}
	}
	return "-a"
}

// Install sets each declared setting, through sudo.
func (p *Power) Install(ctx context.Context, names []string) error {
	var errs []error
	for _, name := range names {
		value, ok := p.declared[name]
		if !ok {
			errs = append(errs, fmt.Errorf("%s isn't declared", name))
			continue
		}
		_, setting, _ := strings.Cut(name, ":")
		if _, err := p.run.Run(ctx, runner.Command{Name: "sudo", Args: []string{"-n", "pmset", flag(name), setting, value}}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// Remove leaves each setting as it is: a power setting has no unset state,
// so undeclaring one leaves the Mac's value.
func (p *Power) Remove(context.Context, []string) error { return nil }

// NeedsAdmin is names: pmset needs root.
func (p *Power) NeedsAdmin(_ context.Context, names []string) ([]string, error) {
	return slices.Clone(names), nil
}

// SetAdmin is how the kind finds out whether an administrator's password is
// at hand.
func (p *Power) SetAdmin(held func(ctx context.Context) bool) { p.admin = held }

// Blocked holds up every setting missing while kit has no administrator's
// password.
func (p *Power) Blocked(ctx context.Context, missing map[string]string, _ []kind.Installed) (map[string]string, error) {
	blocked := map[string]string{}
	if p.admin == nil || p.admin(ctx) {
		return blocked, nil
	}
	for name := range missing {
		blocked[name] = "waiting for an administrator's password: kit apply at a terminal asks for it"
	}
	return blocked, nil
}

var _ interface {
	kind.Kind
	kind.Valued
	kind.Diverger
	kind.Admin
	kind.Blocker
} = (*Power)(nil)
