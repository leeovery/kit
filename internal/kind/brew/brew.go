// Package brew is Homebrew's kinds, formulae and casks, and the step that
// checks Homebrew is there.
package brew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/hold"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// StepName is the name of the step that checks Homebrew is there, which
// the formulae and casks need.
const StepName = "homebrew"

// installTimeout is how long an install of n things may take: half an hour,
// and three minutes more for each, as a new Mac installs every cask in one
// go, big downloads and all, where one at a time a big cask, or formulae
// building from source, can take a while.
func installTimeout(n int) time.Duration { return 30*time.Minute + time.Duration(n)*3*time.Minute }

// Homebrew is Homebrew, driven through a runner.
type Homebrew struct {
	run runner.Runner
	// Admin reports whether an administrator's password is at hand, for
	// installing Homebrew and casks that install through a package: nil
	// when kit isn't installing, and asks nothing.
	Admin func(ctx context.Context) bool

	// update is brew update's, once a run: mu is held while it runs, and
	// done once it has.
	update struct {
		mu   sync.Mutex
		done bool
		err  error
	}
	// changing is held while brew installs or uninstalls: formulae's and
	// casks' steps run side by side, and two of Homebrew changing things at
	// once can clash over its locks. turn is the name of the kind that last
	// took it.
	changing sync.Mutex
	turn     atomic.Pointer[string]
	// updating is held whole while brew update runs, and shared by every
	// other brew command kit runs (share): Homebrew updating itself swaps
	// its own code and Ruby, and a command running meanwhile fails ("Another
	// brew vendor-install ruby process is already running": the sixth run).
	updating sync.RWMutex
}

// formulaeName and casksName name Homebrew's kinds.
const (
	formulaeName = "brew"
	casksName    = "cask"
)

// FormulaeDescriptor and CasksDescriptor are Homebrew's formulae's and
// casks' kinds, as kit's shared code knows them: each needs Homebrew, its
// step made with the first in a run, and shares it with the other (Of).
var (
	FormulaeDescriptor = kind.Descriptor{
		Name:    formulaeName,
		Label:   "Homebrew formulae",
		Group:   kind.Packages,
		Section: &config.Section{Kind: formulaeName, Header: "homebrew formulae", Form: config.Names, Sorted: true, Held: true},
		Help: &kind.Help{
			What:   "Homebrew's formulae",
			Add:    "Install formulae, and declare them",
			Remove: "Uninstall formulae, and undeclare them",
		},
		Row:   "Homebrew formulae",
		Tool:  "Homebrew",
		Needs: []string{StepName},
		Setup: setup,
		New:   func(d *kind.Deps) kind.Kind { return Of(d).Formulae() },
	}
	CasksDescriptor = kind.Descriptor{
		Name:    casksName,
		Label:   "Homebrew casks",
		Group:   kind.Packages,
		Section: &config.Section{Kind: casksName, Header: "homebrew casks", Form: config.Names, Sorted: true, Held: true},
		Help: &kind.Help{
			What:   "Homebrew's casks",
			Add:    "Install casks, and declare them",
			Remove: "Uninstall casks, and undeclare them",
		},
		Row:   "Homebrew casks",
		Tool:  "Homebrew",
		Needs: []string{StepName},
		Setup: setup,
		New:   func(d *kind.Deps) kind.Kind { return Of(d).Casks() },
	}
)

// setup is the step Homebrew's kinds need: Homebrew's own.
func setup(d *kind.Deps) []engine.Step { return []engine.Step{Of(d).Step()} }

// shared keeps the Homebrew Homebrew's kinds share.
var shared = kind.NewKey[*Homebrew]("Homebrew")

// Of is the Homebrew the kinds made from d share.
func Of(d *kind.Deps) *Homebrew {
	return kind.Share(d, shared, func() *Homebrew { return New(d.Run) })
}

// New returns Homebrew, driven through run.
func New(run runner.Runner) *Homebrew {
	return &Homebrew{run: run}
}

// Step checks brew is on kit's PATH, and where Homebrew is; applying
// installs Homebrew, which needs an administrator's password.
func (h *Homebrew) Step() engine.Step {
	return engine.Step{
		Name:  StepName,
		Title: "Homebrew",
		Admin: true,
		Check: func(ctx context.Context) check.Result {
			res, err := h.brew(ctx, "--prefix")
			if errors.Is(err, runner.ErrNotFound) {
				return check.Result{State: check.Attention, Summary: "not installed: brew isn't on kit's PATH", Items: []check.Item{{
					ID: StepName + ":" + StepName, Name: "Homebrew", State: "missing", Detail: "brew isn't on kit's PATH", Action: kind.Install,
				}}}
			}
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			return check.Result{State: check.OK, Summary: strings.TrimSpace(string(res.Stdout))}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if h.Admin == nil || !h.Admin(ctx) {
				return errors.New("waiting for an administrator's password: kit apply at a terminal asks for it")
			}
			return Install(ctx, h.run)
		},
	}
}

// installer is Homebrew's own install script.
const installer = "https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh"

// installerTimeout is how long installing Homebrew may take: the Xcode
// Command Line Tools come with it, downloaded and installed by
// softwareupdate (11 minutes in all in a virtual machine, 9 Oct).
const installerTimeout = 45 * time.Minute

// Install installs Homebrew with its own install script, unattended
// (NONINTERACTIVE), the Xcode Command Line Tools with it if they're not
// there: the script asks sudo for an administrator's password, which asks
// kit through SUDO_ASKPASS, so the run must hold one.
func Install(ctx context.Context, run runner.Runner) error {
	script := InstallerFile()
	defer func() { _ = os.Remove(script) }()
	if _, err := run.Run(ctx, runner.Command{Name: "curl", Args: []string{"-fsSL", "--retry", "2", "-o", script, installer}}); err != nil {
		return fmt.Errorf("download Homebrew's installer: %w", err)
	}
	if _, err := run.Run(ctx, runner.Command{Name: "/bin/bash", Args: []string{script}, Env: []string{"NONINTERACTIVE=1"}, Timeout: installerTimeout}); err != nil {
		return fmt.Errorf("install Homebrew: %w", err)
	}
	return nil
}

// InstallerFile is where Install keeps Homebrew's install script while it
// runs it: in the user's own temporary folder.
func InstallerFile() string {
	return filepath.Join(os.TempDir(), "kit-homebrew-install.sh")
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
	defer h.share(ctx)()
	return h.run.Run(ctx, h.batched(runner.Command{Name: "brew", Args: args}))
}

// share waits, while Homebrew updates itself, till it's done, as every
// brew command but the update does, the wait not counted against the time
// limits ctx runs under; it returns what lets an update go.
func (h *Homebrew) share(ctx context.Context) (done func()) {
	if !h.updating.TryRLock() {
		waited, resume := hold.Waiting(ctx), kind.Waiting(ctx, "Homebrew's update")
		h.updating.RLock()
		resume()
		waited()
	}
	return h.updating.RUnlock
}

// batched is cmd, a brew command, with Homebrew's git over SSH never asking
// anything (SSHConfig): brew installing runs in kit's session, with kit's
// terminal, where ssh would ask, unseen behind kit's screen, for good, as it
// did whether to trust GitHub's host key when the user's git settings sent
// a tap's clone over SSH (the fifth run, 16 Oct). Asking, it fails at once,
// saying why.
func (h *Homebrew) batched(cmd runner.Command) runner.Command {
	if path, err := SSHConfig(); err == nil {
		cmd.Env = append(cmd.Env, "HOMEBREW_SSH_CONFIG_PATH="+path)
	}
	return cmd
}

// sshConfig is the ssh config Homebrew's git uses as kit runs brew: batch
// mode, then the user's own config, as ssh takes the first value it reads.
const sshConfig = `# Homebrew's git over SSH as kit runs brew: never asking, as no one would
# see it. Written by kit; then the user's own.
BatchMode yes
Include ~/.ssh/config
`

// SSHConfig writes the ssh config Homebrew's git uses as kit runs brew, in
// the user's own temporary folder, and returns where it is.
func SSHConfig() (string, error) {
	path := filepath.Join(os.TempDir(), "kit-homebrew-ssh-config")
	if had, err := os.ReadFile(path); err == nil && string(had) == sshConfig {
		return path, nil
	}
	return path, os.WriteFile(path, []byte(sshConfig), 0o600)
}

// ready taps the taps names, the kind named kindName's, are from that
// aren't tapped yet, each as one command, saying so: in Homebrew's turn to
// change things.
func (h *Homebrew) ready(ctx context.Context, kindName string, names []string) error {
	var taps []string
	for _, name := range names {
		if parts := strings.Split(name, "/"); len(parts) == 3 && !slices.Contains(taps, parts[0]+"/"+parts[1]) {
			taps = append(taps, parts[0]+"/"+parts[1])
		}
	}
	if len(taps) == 0 {
		return nil
	}
	tapped, err := h.lines(ctx, "tap")
	if err != nil {
		return err
	}
	taps = slices.DeleteFunc(taps, func(tap string) bool {
		return slices.ContainsFunc(tapped, func(had string) bool { return strings.EqualFold(had, tap) })
	})
	if len(taps) == 0 {
		return nil
	}
	defer h.take(ctx, kindName)()
	var errs []error
	for _, tap := range taps {
		if err := h.watched(ctx, "tapping "+tap, h.batched(runner.Command{Name: "brew", Args: []string{"tap", tap}})); err != nil {
			errs = append(errs, fmt.Errorf("couldn't tap %s: %w", tap, err))
		}
	}
	return errors.Join(errs...)
}

// watched runs cmd, a brew command that counts nothing, as a tap or an
// uninstall, once Homebrew isn't updating itself (share), then watched for
// going quiet, its step saying says (kind.Watch): the wait for the update
// is no silence of the command's.
func (h *Homebrew) watched(ctx context.Context, says string, cmd runner.Command) error {
	defer h.share(ctx)()
	cmd, unwatch := kind.Watch(ctx, says, cmd)
	defer unwatch()
	_, err := h.run.Run(ctx, cmd)
	return err
}

// updated brings Homebrew's formulae up to date, once a run, before the
// first install: an install from stale formulae can fail. The step
// installing says it's updating Homebrew, or, while another kind's install
// updates it, that it waits for that (kind.Waiting).
func (h *Homebrew) updated(ctx context.Context) error {
	if !h.update.mu.TryLock() {
		resume := kind.Waiting(ctx, "Homebrew's update")
		h.update.mu.Lock()
		resume()
	}
	defer h.update.mu.Unlock()
	if !h.update.done {
		resume := kind.Saying(ctx, "updating Homebrew")
		h.updating.Lock()
		_, h.update.err = h.run.Run(ctx, h.batched(runner.Command{Name: "brew", Args: []string{"update", "--quiet"}}))
		h.updating.Unlock()
		h.update.done = true
		resume()
	}
	return h.update.err
}

// install runs brew install, after brew update, of names, the kind named
// kindName's, with which says what they are: --formula or --cask. Each
// tap's go in apart, so a tap that fails, as one Homebrew refuses (the
// sixth run), fails its own alone; and first, so a tap's formula claims a
// name it shares with one of Homebrew's own before anything pulls that in
// as a dependency (a tap's php, wanted, and Homebrew's, which composer
// needs a php of, share a keg). Then Homebrew's own, in one go.
func (h *Homebrew) install(ctx context.Context, kindName, which string, names []string) error {
	if err := h.updated(ctx); err != nil {
		return err
	}
	defer h.take(ctx, kindName)()
	var errs []error
	for _, batch := range byTap(names) {
		if ctx.Err() != nil {
			break
		}
		kind.Starting(ctx)
		if err := h.changes(ctx, runner.Command{
			Name: "brew", Args: slices.Concat([]string{"install", which}, batch),
			Timeout: installTimeout(len(batch)), Lines: kind.Lines(ctx),
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return kind.Joined(errs)
}

// changes runs cmd, brew installing, as Homebrew's turn to change things
// has it (take), once Homebrew isn't updating itself (share).
func (h *Homebrew) changes(ctx context.Context, cmd runner.Command) error {
	defer h.share(ctx)()
	_, err := h.run.Run(ctx, h.sudoed(ctx, h.batched(cmd)))
	return err
}

// byTap are names as they install, in the order they come: each tap's
// together, then Homebrew's own.
func byTap(names []string) [][]string {
	var taps []string
	byTap := map[string][]string{}
	var own []string
	for _, name := range names {
		tap, ok := tapOf(name)
		if !ok {
			own = append(own, name)
			continue
		}
		if _, seen := byTap[tap]; !seen {
			taps = append(taps, tap)
		}
		byTap[tap] = append(byTap[tap], name)
	}
	batches := make([][]string, 0, len(taps)+1)
	for _, tap := range taps {
		batches = append(batches, byTap[tap])
	}
	if len(own) > 0 {
		batches = append(batches, own)
	}
	return batches
}

// take takes Homebrew's turn to change things, for the kind named kindName:
// while another's install or uninstall has it, the step installing says it
// waits for that kind's (kind.Waiting). It returns what gives the turn back.
func (h *Homebrew) take(ctx context.Context, kindName string) (giveBack func()) {
	if !h.changing.TryLock() {
		holder := "Homebrew"
		if name := h.turn.Load(); name != nil {
			holder = engine.Title(ctx, *name)
		}
		resume := kind.Waiting(ctx, holder)
		h.changing.Lock()
		resume()
	}
	h.turn.Store(&kindName)
	return h.changing.Unlock
}

// The lines brew install prints as each formula, and each cask, is
// installed, a dependency's as well as those asked for, after the badge
// (🍺, unless HOMEBREW_NO_EMOJI drops it, or HOMEBREW_INSTALL_BADGE
// changes it): a formula's keg, by its name and version, and its size; a
// cask by its name.
var (
	formulaInstalled = regexp.MustCompile(`(?:^|\s)/\S*/([^/\s]+)/[^/\s]+: (?:[\d,]+ files, )?[\d.]+[KMGT]?B\b`)
	caskInstalled    = regexp.MustCompile(`(?:^|\s)(\S+) was successfully installed!$`)
)

// The lines brew install prints as it downloads, before it installs
// anything: one as each download's done, a dependency's as well as those
// asked for, by its name and version, a formula's bottle, a formula without
// one, or a cask (a bottle's manifest, fetched first, isn't one); then the
// line each install starts with, a formula's, a cask's or a dependency's.
var (
	downloaded = regexp.MustCompile(`^✔\x{FE0E}?\s+(?:Bottle|Formula|Cask)\s+(\S+)\s+\(`)
	begun      = regexp.MustCompile(`^==> (?:Installing|Pouring)\b`)
)

// The lines brew install prints as it begins installing one of the things
// asked for: a formula, a cask, or a formula's dependencies, by its name in
// full, or by its name in its tap, from that tap; and a formula's bottle as
// it's poured, by its name in its tap. A dependency's install is named for
// what needs it (==> Installing ffmpeg dependency: aom).
var (
	installingLine = regexp.MustCompile(`^==> Installing (?:Cask |dependencies for )?([^\s:]+)(?: from (\S+))?`)
	pouringLine    = regexp.MustCompile(`^==> Pouring (\S+?)--`)
)

// fetched is which of names line, from brew install, says is downloaded.
func fetched(line string, names []string) (string, bool) {
	return installed(downloaded, line, names)
}

// installing is which of names line, from brew install, says it begins
// installing.
func installing(line string, names []string) (string, bool) {
	line = strings.TrimSpace(line)
	if m := installingLine.FindStringSubmatch(line); m != nil {
		if m[2] != "" {
			return named(m[2]+"/"+m[1], names)
		}
		return named(m[1], names)
	}
	if m := pouringLine.FindStringSubmatch(line); m != nil {
		return named(m[1], names)
	}
	return "", false
}

// hasBegun reports whether line, from brew install, says it's begun
// installing what it downloaded.
func hasBegun(line string) bool {
	return begun.MatchString(strings.TrimSpace(line))
}

// installed is which of names line says is installed, as installedLine
// finds the name it says.
func installed(installedLine *regexp.Regexp, line string, names []string) (string, bool) {
	m := installedLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	return named(m[1], names)
}

// named is which of names said is, as Homebrew says a name: in full, as in
// owner/tap/tool, or by its name in its tap. The one of that full name, else
// the first of that name in its tap.
func named(said string, names []string) (string, bool) {
	i := slices.IndexFunc(names, func(name string) bool { return strings.EqualFold(name, said) })
	if i < 0 {
		i = slices.IndexFunc(names, func(name string) bool { return strings.EqualFold(lastPart(name), lastPart(said)) })
	}
	if i < 0 {
		return "", false
	}
	return names[i], true
}

// uninstall runs brew uninstall of names, the kind named kindName's, with
// which says what they are, watched for going quiet (kind.Watch), as it
// holds Homebrew's turn: a kind waiting for it is never quiet itself.
func (h *Homebrew) uninstall(ctx context.Context, kindName, which string, names []string) error {
	defer h.take(ctx, kindName)()
	return h.watched(ctx, "uninstalling", h.sudoed(ctx, h.batched(runner.Command{Name: "brew", Args: slices.Concat([]string{"uninstall", which}, names)})))
}

// sudoed is cmd, brew installing or uninstalling, when an administrator's
// password is at hand for the run: told its check of sudo is done, as that
// check makes sudo forget the password it was given, so sudo would ask for
// it again, mid-run, of brew and of everything after it (formulae make the
// check too: the third run, 14 Oct); and run in kit's own session, whose
// sudo has the password, so a Mac with Touch ID for sudo shows no sheet
// for it (SharesSudo: Homebrew's sudo asks kit, never the terminal).
func (h *Homebrew) sudoed(ctx context.Context, cmd runner.Command) runner.Command {
	if h.Admin == nil || !h.Admin(ctx) {
		return cmd
	}
	cmd.Env = append(cmd.Env, "HOMEBREW_SUDO_CHECKED=1")
	cmd.SharesSudo = true
	return cmd
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

func (formulae) Name() string    { return formulaeName }
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

// Install installs names, each tap's apart and first (install).
func (f formulae) Install(ctx context.Context, names []string) error {
	return f.install(ctx, f.Name(), "--formula", names)
}

// Done is which of names line, from brew install, says is installed.
func (formulae) Done(line string, names []string) (string, bool) {
	return installed(formulaInstalled, line, names)
}

// Fetched is which of names line, from brew install, says is downloaded.
func (formulae) Fetched(line string, names []string) (string, bool) { return fetched(line, names) }

// Begun reports whether line, from brew install, says it's begun
// installing what it downloaded.
func (formulae) Begun(line string) bool { return hasBegun(line) }

// Installing is which of names line, from brew install, says it begins
// installing.
func (formulae) Installing(line string, names []string) (string, bool) {
	return installing(line, names)
}

// Short is the part of name a formula's shown by: its name in its tap.
func (formulae) Short(name string) string { return lastPart(name) }

// Ready taps the taps the formulae names are from, before any's installed.
func (f formulae) Ready(ctx context.Context, names []string) error {
	return f.ready(ctx, f.Name(), names)
}

// Shares names the kind formulae take turns with: casks, which Homebrew
// installs too.
func (formulae) Shares() []string { return []string{"cask"} }

func (f formulae) Remove(ctx context.Context, names []string) error {
	return f.uninstall(ctx, f.Name(), "--formula", names)
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

// lastPart is a name's last part: its name in its tap.
func lastPart(name string) string {
	return name[strings.LastIndex(name, "/")+1:]
}

type casks struct{ *Homebrew }

func (casks) Name() string    { return casksName }
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

// Install installs names, each tap's apart and first (install).
func (c casks) Install(ctx context.Context, names []string) error {
	return c.install(ctx, c.Name(), "--cask", names)
}

// Done is which of names line, from brew install, says is installed.
func (casks) Done(line string, names []string) (string, bool) {
	return installed(caskInstalled, line, names)
}

// Fetched is which of names line, from brew install, says is downloaded.
func (casks) Fetched(line string, names []string) (string, bool) { return fetched(line, names) }

// Begun reports whether line, from brew install, says it's begun
// installing what it downloaded.
func (casks) Begun(line string) bool { return hasBegun(line) }

// Installing is which of names line, from brew install, says it begins
// installing.
func (casks) Installing(line string, names []string) (string, bool) {
	return installing(line, names)
}

// Short is the part of name a cask's shown by: its name in its tap.
func (casks) Short(name string) string { return lastPart(name) }

// Ready taps the taps the casks names are from, before any's installed.
func (c casks) Ready(ctx context.Context, names []string) error {
	return c.ready(ctx, c.Name(), names)
}

// Shares names the kind casks take turns with: formulae, which Homebrew
// installs too.
func (casks) Shares() []string { return []string{"brew"} }

func (c casks) Remove(ctx context.Context, names []string) error {
	return c.uninstall(ctx, c.Name(), "--cask", names)
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
// or an installer, which ask for an administrator's password. A cask in a
// tap not added yet isn't one: brew info won't describe it till brew install
// adds its tap, so there's no knowing before.
func (h *Homebrew) NeedsAdmin(ctx context.Context, casks []string) ([]string, error) {
	if len(casks) == 0 {
		return nil, nil
	}
	casks, _, err := askable(ctx, h, casks)
	if err != nil || len(casks) == 0 {
		return nil, err
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

// resolve asks brew info for names' full names, all at once. A name in a
// tap not added yet, which brew info can't look up, is its own full name:
// installing it adds its tap, as on a new Mac. brew info says nothing at all
// when any name is unknown, so then it asks again without the names it says
// it doesn't know; failing that, of each alone, leaving out those it
// doesn't know.
func resolve(ctx context.Context, h *Homebrew, which string, names []string) (map[string]string, error) {
	ask, untapped, err := askable(ctx, h, names)
	if err != nil {
		return nil, err
	}
	resolved := make(map[string]string, len(names))
	for _, name := range untapped {
		resolved[name] = name
	}
	if len(ask) == 0 {
		return resolved, nil
	}
	found, err := lookUp(ctx, h, which, ask)
	exit, exited := errors.AsType[*runner.ExitError](err)
	if exited {
		if known := withoutUnknown(ask, strings.Join(exit.Lines("Error:"), "\n")); len(known) < len(ask) && len(known) > 0 {
			found, err = lookUp(ctx, h, which, known)
			_, exited = errors.AsType[*runner.ExitError](err)
		}
	}
	switch {
	case err == nil:
		maps.Copy(resolved, found)
		return resolved, nil
	case !exited:
		return nil, err
	}
	for _, name := range ask {
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

// askable splits names into those brew info can be asked of and those in a
// tap not added yet, which it refuses to describe; it asks brew which taps
// are added only when a name is in one.
func askable(ctx context.Context, h *Homebrew, names []string) (ask, untapped []string, err error) {
	if !slices.ContainsFunc(names, func(name string) bool { _, ok := tapOf(name); return ok }) {
		return names, nil, nil
	}
	taps, err := h.lines(ctx, "tap")
	if err != nil {
		return nil, nil, err
	}
	tapped := set(taps)
	for _, name := range names {
		if tap, ok := tapOf(name); ok && !tapped[tap] {
			untapped = append(untapped, name)
			continue
		}
		ask = append(ask, name)
	}
	return ask, untapped, nil
}

// tapOf is the tap a name is in, as in owner/tap for owner/tap/tool, by
// Homebrew's lower-case name for it; none for Homebrew's own, which brew
// info looks up whether they're added or not.
func tapOf(name string) (string, bool) {
	parts := strings.Split(strings.ToLower(name), "/")
	if len(parts) != 3 || parts[0] == "homebrew" && (parts[1] == "core" || parts[1] == "cask") {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

// unknownName finds the names brew info says it has no formula or cask of.
var unknownName = regexp.MustCompile(`No available (?:formula|cask) with the name "([^"]+)"`)

// withoutUnknown is names without those brew info's errors say it doesn't
// know.
func withoutUnknown(names []string, stderr string) []string {
	unknown := map[string]bool{}
	for _, m := range unknownName.FindAllStringSubmatch(stderr, -1) {
		unknown[m[1]] = true
	}
	return slices.DeleteFunc(slices.Clone(names), func(name string) bool { return unknown[name] })
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
