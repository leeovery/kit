// Package brew is Homebrew's kinds, formulae and casks, and the step that
// checks Homebrew is there.
package brew

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// StepName is the name of the step that checks Homebrew is there, which
// the formulae and casks need.
const StepName = "homebrew"

// installTimeout is how long an install may take: big casks, and formulae
// built from source, can take a while.
const installTimeout = 30 * time.Minute

// Homebrew is Homebrew, driven through a runner.
type Homebrew struct {
	run runner.Runner
	// Admin reports whether an administrator's password is at hand, for
	// casks that install through a package: nil when kit isn't installing,
	// and asks nothing.
	Admin func(ctx context.Context) bool

	update struct {
		once sync.Once
		err  error
	}
	// changing is held while brew installs or uninstalls: formulae's and
	// casks' steps run side by side, and two of Homebrew changing things at
	// once can clash over its locks.
	changing sync.Mutex
}

// New returns Homebrew, driven through run.
func New(run runner.Runner) *Homebrew {
	return &Homebrew{run: run}
}

// Step checks brew is on kit's PATH, and where Homebrew is.
func (h *Homebrew) Step() engine.Step {
	return engine.Step{
		Name:  StepName,
		Title: "Homebrew",
		Check: func(ctx context.Context) check.Result {
			res, err := h.brew(ctx, "--prefix")
			if errors.Is(err, runner.ErrNotFound) {
				return check.Result{State: check.Attention, Summary: "not installed: brew isn't on kit's PATH"}
			}
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			return check.Result{State: check.OK, Summary: strings.TrimSpace(string(res.Stdout))}
		},
	}
}

// Formulae is the brew kind.
func (h *Homebrew) Formulae() kind.Kind {
	return formulae{h}
}

// Casks is the cask kind.
func (h *Homebrew) Casks() kind.Kind {
	return casks{h}
}

func (h *Homebrew) brew(ctx context.Context, args ...string) (runner.Result, error) {
	return h.run.Run(ctx, runner.Command{Name: "brew", Args: args})
}

// updated brings Homebrew's formulae up to date, once a run, before the
// first install: an install from stale formulae can fail.
func (h *Homebrew) updated(ctx context.Context) error {
	h.update.once.Do(func() { _, h.update.err = h.brew(ctx, "update", "--quiet") })
	return h.update.err
}

// install runs brew install, after brew update, of names, with which says
// what they are: --formula or --cask.
func (h *Homebrew) install(ctx context.Context, which string, names []string) error {
	if err := h.updated(ctx); err != nil {
		return err
	}
	h.changing.Lock()
	defer h.changing.Unlock()
	_, err := h.run.Run(ctx, runner.Command{Name: "brew", Args: slices.Concat([]string{"install", which}, names), Timeout: installTimeout})
	return err
}

// uninstall runs brew uninstall of names, with which says what they are.
func (h *Homebrew) uninstall(ctx context.Context, which string, names []string) error {
	h.changing.Lock()
	defer h.changing.Unlock()
	_, err := h.brew(ctx, slices.Concat([]string{"uninstall", which}, names)...)
	return err
}

// Uses lists the formulae installed that need the formula name.
func (h *Homebrew) Uses(ctx context.Context, name string) ([]string, error) {
	return h.lines(ctx, "uses", "--installed", name)
}

// lines runs brew with args, and returns what it printed, a line each.
func (h *Homebrew) lines(ctx context.Context, args ...string) ([]string, error) {
	res, err := h.brew(ctx, args...)
	if err != nil {
		return nil, err
	}
	var lines []string
	for line := range strings.Lines(string(res.Stdout)) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

type formulae struct{ *Homebrew }

func (formulae) Name() string    { return "brew" }
func (formulae) Title() string   { return "Formulae" }
func (formulae) Program() string { return "brew" }

// Installed lists the formulae installed, by full name, from three listings
// run side by side: every formula, the leaves (needed by no other), and the
// leaves installed for themselves.
func (f formulae) Installed(ctx context.Context) ([]kind.Installed, error) {
	listings := [][]string{
		{"list", "--formula", "--full-name", "-1"},
		{"leaves"},
		{"leaves", "--installed-on-request"},
	}
	found := make([][]string, len(listings))
	errs := make([]error, len(listings))
	var wg sync.WaitGroup
	for i, args := range listings {
		wg.Go(func() { found[i], errs[i] = f.lines(ctx, args...) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	leaves, onRequest := set(found[1]), set(found[2])
	installed := make([]kind.Installed, 0, len(found[0]))
	for _, name := range found[0] {
		installed = append(installed, kind.Installed{Name: name, Explicit: onRequest[name], Needed: !leaves[name]})
	}
	return installed, nil
}

// NeededBy lists the formulae installed that need name.
func (f formulae) NeededBy(ctx context.Context, name string) ([]string, error) {
	return f.Uses(ctx, name)
}

func (f formulae) Resolve(ctx context.Context, names []string) (map[string]string, error) {
	return resolve(ctx, f.Homebrew, "--formula", names)
}

// Install installs names, a tap's first: a tap's formula then claims a name
// it shares with one of Homebrew's own before anything pulls that in as a
// dependency (a tap's php, wanted, and Homebrew's, which composer needs a
// php of, share a keg).
func (f formulae) Install(ctx context.Context, names []string) error {
	ordered := slices.Clone(names)
	slices.SortStableFunc(ordered, func(a, b string) int {
		return cmp.Compare(boolInt(!strings.Contains(a, "/")), boolInt(!strings.Contains(b, "/")))
	})
	return f.install(ctx, "--formula", ordered)
}

func (f formulae) Remove(ctx context.Context, names []string) error {
	return f.uninstall(ctx, "--formula", names)
}

// Blocked finds the missing formulae whose names another tap's formula,
// installed, holds: two formulae of one name share a keg, so can't both be
// installed.
func (f formulae) Blocked(_ context.Context, missing map[string]string, installed []kind.Installed) (map[string]string, error) {
	blocked := make(map[string]string)
	for name, full := range missing {
		for _, it := range installed {
			if it.Name != full && lastPart(it.Name) == lastPart(full) {
				blocked[name] = "another tap's " + it.Name + " is installed under that name"
			}
		}
	}
	return blocked, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// lastPart is a name's last part: its name in its tap.
func lastPart(name string) string {
	return name[strings.LastIndex(name, "/")+1:]
}

type casks struct{ *Homebrew }

func (casks) Name() string    { return "cask" }
func (casks) Title() string   { return "Casks" }
func (casks) Program() string { return "brew" }

// NeedsAdmin finds which of names install through a package or an
// installer, which ask for an administrator's password.
func (c casks) NeedsAdmin(ctx context.Context, names []string) ([]string, error) {
	return c.Homebrew.NeedsAdmin(ctx, names)
}

// SetAdmin is how the casks find out whether an administrator's password is
// at hand.
func (c casks) SetAdmin(held func(ctx context.Context) bool) {
	c.Admin = held
}

// Installed lists the casks installed, by full name, each installed for
// itself.
func (c casks) Installed(ctx context.Context) ([]kind.Installed, error) {
	names, err := c.lines(ctx, "list", "--cask", "--full-name", "-1")
	if err != nil {
		return nil, err
	}
	installed := make([]kind.Installed, len(names))
	for i, name := range names {
		installed[i] = kind.Installed{Name: name, Explicit: true}
	}
	return installed, nil
}

func (c casks) Resolve(ctx context.Context, names []string) (map[string]string, error) {
	return resolve(ctx, c.Homebrew, "--cask", names)
}

func (c casks) Install(ctx context.Context, names []string) error {
	return c.install(ctx, "--cask", names)
}

func (c casks) Remove(ctx context.Context, names []string) error {
	return c.uninstall(ctx, "--cask", names)
}

// Blocked finds the missing casks that install through a package, which
// needs an administrator's password, when kit is installing and hasn't got
// one.
func (c casks) Blocked(ctx context.Context, missing map[string]string, _ []kind.Installed) (map[string]string, error) {
	blocked := make(map[string]string)
	if c.Admin == nil {
		return blocked, nil
	}
	var fulls []string
	for _, full := range missing {
		fulls = append(fulls, full)
	}
	slices.Sort(fulls)
	needing, err := c.NeedsAdmin(ctx, fulls)
	if err != nil || len(needing) == 0 || c.Admin(ctx) {
		return blocked, err
	}
	for name, full := range missing {
		if slices.Contains(needing, full) {
			blocked[name] = "needs an administrator's password: run kit apply at a terminal"
		}
	}
	return blocked, nil
}

// NeedsAdmin finds which of casks, by full name, install through a package
// or an installer, which ask for an administrator's password.
func (h *Homebrew) NeedsAdmin(ctx context.Context, casks []string) ([]string, error) {
	if len(casks) == 0 {
		return nil, nil
	}
	res, err := h.brew(ctx, slices.Concat([]string{"info", "--json=v2", "--cask"}, casks)...)
	if err != nil {
		return nil, err
	}
	var in info
	if err := json.Unmarshal(res.Stdout, &in); err != nil {
		return nil, fmt.Errorf("read brew info's answer: %w", err)
	}
	var needing []string
	for _, c := range in.Casks {
		for _, artifact := range c.Artifacts {
			if _, pkg := artifact["pkg"]; pkg {
				needing = append(needing, c.FullToken)
				break
			}
			if _, installer := artifact["installer"]; installer {
				needing = append(needing, c.FullToken)
				break
			}
		}
	}
	return needing, nil
}

// info is what brew info --json=v2 says of formulae and casks, as far as
// the names each goes by.
type info struct {
	Formulae []struct {
		Name     string   `json:"name"`
		FullName string   `json:"full_name"`
		Aliases  []string `json:"aliases"`
		Oldnames []string `json:"oldnames"`
	} `json:"formulae"`
	Casks []struct {
		Token     string                       `json:"token"`
		FullToken string                       `json:"full_token"`
		OldTokens []string                     `json:"old_tokens"`
		Artifacts []map[string]json.RawMessage `json:"artifacts"`
	} `json:"casks"`
}

// resolve asks brew info for names' full names, all at once. brew info
// says nothing at all when any name is unknown, so then it asks of each
// alone, leaving out those it doesn't know.
func resolve(ctx context.Context, h *Homebrew, which string, names []string) (map[string]string, error) {
	resolved, err := lookUp(ctx, h, which, names)
	if err == nil {
		return resolved, nil
	}
	if _, exited := errors.AsType[*runner.ExitError](err); !exited {
		return nil, err
	}
	resolved = make(map[string]string, len(names))
	for _, name := range names {
		one, err := lookUp(ctx, h, which, []string{name})
		if _, exited := errors.AsType[*runner.ExitError](err); exited {
			continue
		}
		if err != nil {
			return nil, err
		}
		maps.Copy(resolved, one)
	}
	return resolved, nil
}

// lookUp asks brew info of names in one go, and maps each to its full name
// by every name brew says it goes by.
func lookUp(ctx context.Context, h *Homebrew, which string, names []string) (map[string]string, error) {
	res, err := h.brew(ctx, slices.Concat([]string{"info", "--json=v2", which}, names)...)
	if err != nil {
		return nil, err
	}
	var in info
	if err := json.Unmarshal(res.Stdout, &in); err != nil {
		return nil, fmt.Errorf("read brew info's answer: %w", err)
	}
	goesBy := make(map[string]string)
	for _, f := range in.Formulae {
		for _, n := range slices.Concat([]string{f.Name, "homebrew/core/" + f.Name}, f.Aliases, f.Oldnames) {
			goesBy[n] = f.FullName
			goesBy[inTap(f.FullName, n)] = f.FullName
		}
	}
	for _, c := range in.Casks {
		for _, n := range slices.Concat([]string{c.Token, "homebrew/cask/" + c.Token}, c.OldTokens) {
			goesBy[n] = c.FullToken
			goesBy[inTap(c.FullToken, n)] = c.FullToken
		}
	}
	resolved := make(map[string]string, len(names))
	for _, name := range names {
		if full, ok := goesBy[name]; ok {
			resolved[name] = full
		}
	}
	return resolved, nil
}

// inTap is name in the tap fullName is from, as in owner/tap/tool@2 for the
// alias tool@2 of owner/tap/tool: a tap's other names are named with it. For
// Homebrew's own formulae and casks, whose full names have no tap, it's name.
func inTap(fullName, name string) string {
	if i := strings.LastIndex(fullName, "/"); i >= 0 {
		return fullName[:i+1] + name
	}
	return name
}

func set(names []string) map[string]bool {
	s := make(map[string]bool, len(names))
	for _, n := range names {
		s[n] = true
	}
	return s
}
