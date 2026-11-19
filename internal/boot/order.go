package boot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/linked"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
	kitstate "github.com/leeovery/kit/internal/state"
	"github.com/leeovery/kit/internal/steps"
)

// The boot order's steps, after This Mac, by name.
const (
	PasswordStep = "password"
	HomebrewStep = "homebrew"
	KitStep      = "kit"
	AppsStep     = "apps"
	ConfigStep   = "kit-config"
	AccessStep   = "full-disk-access"
	TerminalStep = "terminal"
)

// Order is the boot order, planned from the config repository signing in
// read: this Mac; the password, once; Homebrew; the apps kit.toml names;
// kit-config, cloned, its files linked; kit itself, through Homebrew,
// unless it runs a build of its own (OwnBuild); then, with a terminal
// named, Full Disk Access for it, and the hand-off to it. Each needs the
// one before. kit comes after kit-config's files are linked, as Homebrew,
// installing it by its tap's name, writes that it trusts it into
// ~/.homebrew/trust.json, which kit-config may link: written first, the
// file would stand where the link belongs.
func (b *Boot) Order() []engine.Step {
	cfg := b.config()
	order := []engine.Step{b.macStep(), b.passwordStep(), b.homebrewStep()}
	if wanted := b.apps(cfg); len(wanted) > 0 {
		order = append(order, b.appsStep(wanted))
	}
	order = append(order, b.configStep())
	if !b.OwnBuild() {
		order = append(order, b.kitStep())
	}
	if t, ok := b.terminal(cfg); ok {
		order = append(order, b.accessStep(t), b.handOverStep(t))
	}
	for i := range order {
		order[i].Area = AreaBoot
		if i > 0 {
			order[i].Needs = []string{order[i-1].Name}
		}
	}
	return order
}

// terminal is the terminal kit.toml names, as kit knows it: for kit-config
// starting on this Mac, before it's cloned, the one chosen for it.
func (b *Boot) terminal(cfg *config.Config) (apps.Terminal, bool) {
	name := ""
	if cfg != nil {
		name = cfg.Terminal
	} else if st, _ := kitstate.Load[state](b.State, stateName); st.Fresh != nil {
		name = st.Fresh.Terminal
	}
	t, ok := apps.Terminals[name]
	return t, ok
}

// in reports whether kit is running in the terminal t.
func (b *Boot) in(t apps.Terminal) bool {
	running, ok := apps.Running(b.Getenv)
	return ok && running.Name == t.Name
}

// Held reports whether sudo has the password, given this run.
func (b *Boot) Held() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.held
}

// Arrive notes that kit, handed over to, has taken the boot on in the
// terminal, for the kit that handed over, waiting on it, to finish.
func (b *Boot) Arrive() error {
	if !b.HandedOver {
		return nil
	}
	return kitstate.Update(b.State, stateName, func(st *state) { st.Arrived = b.Now() })
}

// sudoAsks is the prompt the boot has sudo give when it wants the password,
// for kit to ask for it in its own field.
const sudoAsks = "[kit: sudo wants the password]"

// passwordStep has sudo take the password, once, for what the boot
// installs, and turns Touch ID on for sudo where it's declared, so sudo is
// a fingerprint after: done when nothing ahead needs the password, or sudo
// has it, and Touch ID is as declared or was tried. macOS asks before the
// terminal kit runs in changes sudo's settings; refused, Touch ID stays off,
// for kit apply, and the boot goes on.
func (b *Boot) passwordStep() engine.Step {
	touchID := func(ctx context.Context) (wanted, on bool) {
		wanted = b.declares(steps.FeatureTouchIDSudo)
		on = steps.TouchIDSudo(b.Run, nil, b.SudoLocal).Check(ctx).State == check.OK
		return wanted, on
	}
	return engine.Step{
		Name: PasswordStep, Title: "Password", Waiting: "once",
		Check: func(ctx context.Context) check.Result {
			wanted, on := touchID(ctx)
			// Handed over to, the boot tried Touch ID before it handed over.
			b.mu.Lock()
			held, tried := b.held, b.triedTouchID || b.HandedOver
			b.mu.Unlock()
			switch {
			case wanted && !on && !tried, !held && !runner.Has(b.Run, "brew"):
				return check.Result{State: check.Attention, Summary: "not given yet"}
			case on:
				return check.Result{State: check.OK, Summary: "Touch ID on for sudo"}
			case wanted:
				return check.Result{State: check.OK, Summary: "given · Touch ID off: kit apply asks again"}
			case held:
				return check.Result{State: check.OK, Summary: "given"}
			}
			return check.Result{State: check.OK, Summary: "not needed"}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if !b.Held() {
				if err := b.givePassword(ctx); err != nil {
					return err
				}
			}
			if wanted, on := touchID(ctx); !wanted || on {
				return nil
			}
			b.mu.Lock()
			b.triedTouchID = true
			b.mu.Unlock()
			b.Sink.Emit(event.Doing{Time: b.Now(), Step: PasswordStep, Says: "turning Touch ID on for sudo",
				Todo: fmt.Sprintf("macOS asks whether %s may administer your computer: choose Allow", apps.RunningIn(b.Getenv))})
			step := steps.TouchIDSudo(b.Run, &steps.Admin{Held: func(context.Context) bool { return b.Held() }}, b.SudoLocal)
			// Refused, it's left off: kit apply asks again.
			_ = step.Apply(ctx, step.Check(ctx))
			return nil
		},
	}
}

// givePassword asks for the password in the step's row each time sudo
// wants it, giving sudo what's typed on its input, never on a command line;
// once sudo takes it, it's kept for the run.
func (b *Boot) givePassword(ctx context.Context) error {
	var typed string
	_, err := b.Run.Run(ctx, runner.Command{
		Name: "sudo", Args: []string{"-S", "-p", sudoAsks, "-v"}, Timeout: 10 * time.Minute, Asks: sudoAsks,
		Answer: func(ctx context.Context, asked int) (string, error) {
			question := "this Mac's password, once"
			if asked > 0 {
				question = "that wasn't it: try again"
			}
			var err error
			typed, err = b.Ask.Secret(ctx, PasswordStep, question)
			return typed, err
		},
	})
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		return errors.New("sudo refused the password: paste the install line into Terminal again")
	}
	b.mu.Lock()
	b.held = true
	b.mu.Unlock()
	if typed != "" && b.Keep != nil {
		b.Keep(typed)
	}
	return nil
}

// declares reports whether this Mac's declarations switch the feature
// named on.
func (b *Boot) declares(feature string) bool {
	cfg := b.config()
	mac, err := config.ReadMachine(b.State)
	if cfg == nil || err != nil || mac == "" {
		return false
	}
	list, err := cfg.List(config.FeaturesKind, mac)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(list.Entries, func(e config.Entry) bool { return e.Name == feature })
}

// homebrewStep installs Homebrew, with its own installer, unattended, the
// Xcode tools with it: done when brew is there.
func (b *Boot) homebrewStep() engine.Step {
	return engine.Step{
		Name: HomebrewStep, Title: "Homebrew",
		Check: func(ctx context.Context) check.Result {
			res, err := b.Run.Run(ctx, runner.Command{Name: "brew", Args: []string{"--version"}})
			switch {
			case errors.Is(err, runner.ErrNotFound):
				return check.Result{State: check.Attention, Summary: "not installed"}
			case err != nil:
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			first, _, _ := strings.Cut(string(res.Stdout), "\n")
			says := []string{strings.TrimSpace(strings.TrimPrefix(first, "Homebrew"))}
			if tools := b.xcodeTools(ctx); tools != "" {
				says = append(says, "Xcode tools "+tools)
			}
			return check.Result{State: check.OK, Summary: strings.Join(says, " · ")}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if !b.Held() {
				return errors.New("needs this Mac's password: paste the install line into Terminal")
			}
			b.doing(HomebrewStep, "installing")
			return brew.Install(ctx, b.Run)
		},
	}
}

// kitFormula is kit's own formula, named in full, so brew taps its tap
// itself.
const kitFormula = "leeovery/tools/kit"

// OwnBuild reports whether the boot runs a build of kit's own, as the
// install script given one (KIT_FROM) starts it: it then hands over to that
// build, and Homebrew's kit is kit apply's to install, as kit-config
// declares it.
func (b *Boot) OwnBuild() bool {
	return b.Getenv("KIT_FROM") != ""
}

// homebrewKit is where Homebrew puts kit: in its prefix's bin, as brew
// --prefix says, /opt/homebrew on Apple silicon and /usr/local on Intel.
func (b *Boot) homebrewKit(ctx context.Context) (string, error) {
	res, err := b.Run.Run(ctx, runner.Command{Name: "brew", Args: []string{"--prefix"}})
	if err != nil {
		return "", fmt.Errorf("find Homebrew: %w", err)
	}
	return filepath.Join(strings.TrimSpace(string(res.Stdout)), "bin", "kit"), nil
}

// CarriesOnAs is the kit the boot carries on as in the terminal kit.toml
// names, handed over to or there already: Homebrew's, which the boot
// installed, so kit apply and all after run from it; or, running a build of
// its own, that build (Self).
func (b *Boot) CarriesOnAs(ctx context.Context) (string, error) {
	if b.OwnBuild() {
		return b.Self, nil
	}
	return b.homebrewKit(ctx)
}

// Becomes is the kit the boot, running as self in the terminal kit.toml
// names already, becomes to carry on there: Homebrew's, which it installed;
// "" when it carries on itself, being that kit, or a build of its own.
func (b *Boot) Becomes(ctx context.Context, self string) (string, error) {
	if b.OwnBuild() {
		return "", nil
	}
	kit, err := b.homebrewKit(ctx)
	if err != nil || sameFile(kit, self) {
		return "", err
	}
	return kit, nil
}

// sameFile reports whether a and b are one file, links followed, as
// Homebrew's kit is a link to its version's.
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

// kitStep installs kit itself through Homebrew, which keeps it up to date,
// for the boot to hand over to: done when Homebrew's kit is there, and no
// older than the boot's own, as it may be on a boot run again, or with the
// tap a release behind; a boot that isn't a release's compares none.
func (b *Boot) kitStep() engine.Step {
	return engine.Step{
		Name: KitStep, Title: "kit",
		Check: func(ctx context.Context) check.Result {
			kit, err := b.homebrewKit(ctx)
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			if !runner.Has(runner.Exec{}, kit) {
				return check.Result{State: check.Attention, Summary: "not installed"}
			}
			res, err := b.Run.Run(ctx, runner.Command{Name: kit, Args: []string{"--version"}})
			if err != nil {
				return check.Result{State: check.Failed, Reason: fmt.Sprintf("read Homebrew's kit's version: %v", err)}
			}
			have := strings.TrimPrefix(strings.TrimSpace(string(res.Stdout)), "kit version ")
			if config.Older(have, b.Version) {
				return check.Result{State: check.Attention, Summary: fmt.Sprintf("%s, older than this kit, %s", have, b.Version)}
			}
			return check.Result{State: check.OK, Summary: have + " · through Homebrew"}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			verb, doing := "install", "installing"
			if kit, err := b.homebrewKit(ctx); err == nil && runner.Has(runner.Exec{}, kit) {
				verb, doing = "upgrade", "upgrading"
			}
			b.doing(KitStep, doing)
			if _, err := b.Run.Run(ctx, runner.Command{Name: "brew", Args: []string{verb, "--formula", kitFormula}, Timeout: 15 * time.Minute, SudoAsks: true}); err != nil {
				return fmt.Errorf("%s kit through Homebrew: %w", verb, err)
			}
			return nil
		},
	}
}

// xcodeTools is the Xcode Command Line Tools' version, as in 27.0, or ""
// when they're not installed as their own package.
func (b *Boot) xcodeTools(ctx context.Context) string {
	res, err := b.Run.Run(ctx, runner.Command{Name: "pkgutil", Args: []string{"--pkg-info=com.apple.pkg.CLTools_Executables"}})
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(res.Stdout)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "version: "); ok {
			parts := strings.SplitN(v, ".", 3)
			return strings.Join(parts[:min(2, len(parts))], ".")
		}
	}
	return ""
}

// app is an app kit.toml names, for the boot to install.
type app struct {
	title, cask, bundle string
}

// apps are the apps kit.toml names: the terminal, the one app the boot
// installs, everything else being kit apply's.
func (b *Boot) apps(cfg *config.Config) []app {
	var wanted []app
	if t, ok := b.terminal(cfg); ok {
		wanted = append(wanted, app{title: t.Title, cask: t.Cask, bundle: t.Bundle})
	}
	return wanted
}

// appsStep installs the apps kit.toml names, before the rest of the Mac:
// done when each is in the Applications folder. It installs those missing
// as it applies, whatever its check found, and runs nothing when none is.
func (b *Boot) appsStep(wanted []app) engine.Step {
	return engine.Step{
		Name: AppsStep, Title: "Apps",
		Check: func(context.Context) check.Result {
			var have, missing []string
			var items []check.Item
			for _, a := range wanted {
				version, ok := b.version(a.bundle)
				if !ok {
					missing = append(missing, a.title)
					items = append(items, check.Item{ID: "cask:" + a.cask, Name: a.cask, State: "missing", Action: kind.Install})
					continue
				}
				have = append(have, strings.TrimSpace(a.title+" "+version))
			}
			if len(missing) > 0 {
				return check.Result{State: check.Attention, Summary: "to install: " + and(missing), Items: items}
			}
			return check.Result{State: check.OK, Summary: strings.Join(have, " · ")}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			var casks []string
			for _, a := range wanted {
				if _, ok := b.version(a.bundle); !ok {
					casks = append(casks, a.cask)
				}
			}
			if len(casks) == 0 {
				return nil
			}
			b.doing(AppsStep, "installing")
			_, err := b.Run.Run(ctx, runner.Command{Name: "brew", Args: slices.Concat([]string{"install", "--cask"}, casks), Timeout: 30 * time.Minute, SudoAsks: true})
			return err
		},
	}
}

// version is the version of the app bundle in the Applications folder, as
// it says, and whether it's there.
func (b *Boot) version(bundle string) (string, bool) {
	path := filepath.Join(b.Applications, bundle)
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(path, "Contents", "Info.plist"))
	if err != nil {
		return "", true
	}
	info, err := plist.Decode(data)
	if err != nil {
		return "", true
	}
	dict, _ := info.(map[string]any)
	version, _ := dict["CFBundleShortVersionString"].(string)
	return version, true
}

// configStep clones the config repository, through GitHub's CLI, handed
// the sign-in, then links its files into the home folder, so the terminal
// opens set up: done when it's cloned and its files are linked. A new Mac
// is added to kit.toml, uncommitted: git has no name to commit with till
// kit-config's settings are applied. kit-config starting on this Mac has
// its repository created, if it's to be, and its first files pushed
// (pushFirst), before it's done.
func (b *Boot) configStep() engine.Step {
	return engine.Step{
		Name: ConfigStep, Title: "kit-config",
		Check: func(ctx context.Context) check.Result {
			st, err := kitstate.Load[state](b.State, stateName)
			switch {
			case err != nil:
				return check.Result{State: check.Failed, Reason: err.Error()}
			case !b.cloned():
				return check.Result{State: check.Attention, Summary: "not cloned yet"}
			case st.Fresh != nil:
				return check.Result{State: check.Attention, Summary: "cloned · first files not pushed yet"}
			}
			files, err := b.files()
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			found := files.Check(ctx)
			if found.State == check.Failed || found.State == check.Deferred {
				return found
			}
			says := []string{"cloned"}
			if st.Started {
				says = append(says, "first files pushed")
			}
			says = append(says, fmt.Sprintf("%d %s", found.Counts["linked"], look.Plural(found.Counts["linked"], "file linked", "files linked")))
			for _, it := range found.Items {
				if it.Action != "" {
					return check.Result{State: check.Attention, Summary: "cloned · files to link", Items: found.Items}
				}
			}
			if n := found.Counts[linked.Diverged]; n > 0 {
				says = append(says, fmt.Sprintf("%d %s", n, look.Plural(n, "differs: kit reconcile settles it", "differ: kit reconcile settles them")))
			}
			if st.NoTerminal {
				if cfg, err := config.Load(b.Config, b.Kinds); err == nil && cfg.Terminal == "" {
					says = append(says, "no terminal named: kit terminal <name> names one")
				}
			}
			if st.NewMac != "" {
				if cfg, err := config.Load(b.Config, b.Kinds); err == nil && cfg.Knows(st.NewMac) {
					says = append(says, st.NewMac+" added to kit.toml")
				}
			}
			return check.Result{State: check.OK, Summary: strings.Join(says, " · ")}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if !b.cloned() {
				if err := b.clone(ctx); err != nil {
					return err
				}
			}
			if err := b.pushFirst(ctx); err != nil {
				return err
			}
			if err := b.addNewMac(); err != nil {
				return err
			}
			files, err := b.files()
			if err != nil {
				return err
			}
			b.doing(ConfigStep, "linking its files")
			return files.Apply(ctx, files.Check(ctx))
		},
	}
}

// cloned reports whether the config repository is cloned where kit reads
// it.
func (b *Boot) cloned() bool {
	_, err := os.Stat(filepath.Join(b.Config, ".git"))
	return err == nil
}

// clone clones the config repository through GitHub's CLI, installed if
// it's not there and handed the sign-in, which kit's keychain item then
// gives up: from then on it's GitHub's CLI's. It clones the repository's
// own branch alone, which is all it fetches from then on, so apps'
// settings, on its prefs branch, never come with it.
func (b *Boot) clone(ctx context.Context) error {
	if _, err := os.Stat(b.Config); err == nil {
		return fmt.Errorf("%s is there, and isn't a clone of kit-config: move it aside, then boot again", b.Config)
	}
	token, login := b.signedIn()
	if token == "" {
		return errors.New("not signed in to GitHub")
	}
	st, err := kitstate.Load[state](b.State, stateName)
	if err != nil {
		return err
	}
	repo := cmp.Or(st.Repo, b.repo(login))
	if err := b.account().HandOver(ctx, ConfigStep, token); err != nil {
		return err
	}
	if st.Fresh != nil && st.Fresh.Create {
		if err := b.create(ctx, token, repo); err != nil {
			return err
		}
	}
	return b.account().Clone(ctx, ConfigStep, repo, b.Config)
}

// addNewMac adds a new Mac, as chosen, to the clone's kit.toml, unless
// it's there already: a copy of the Mac it starts from, as kit machine add
// --from makes, when it starts from one.
func (b *Boot) addNewMac() error {
	st, err := kitstate.Load[state](b.State, stateName)
	if err != nil || st.NewMac == "" {
		return err
	}
	cfg, err := config.Load(b.Config, b.Kinds)
	if err != nil {
		return err
	}
	switch {
	case cfg.Knows(st.NewMac):
		return nil
	case st.From != "":
		return cfg.AddMac(st.NewMac, st.From, "")
	}
	path := filepath.Join(b.Config, config.File)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		data = append(data, '\n')
	}
	data = append(data, fmt.Sprintf("\n[macs.%s]\n", st.NewMac)...)
	return os.WriteFile(path, data, 0o644)
}

// files are the config repository's linked files, for this Mac.
func (b *Boot) files() (*linked.Files, error) {
	mac, err := config.ReadMachine(b.State)
	if err != nil {
		return nil, err
	}
	return &linked.Files{
		Repo: gitrepo.Repo{Dir: b.Config, Run: b.Run}, Home: b.Home, StateDir: b.State,
		Scopes: []string{config.Shared, mac},
	}, nil
}

// accessStep has the person give the terminal Full Disk Access before it
// first opens, as an app takes it up only when it starts: System Settings
// is opened at the list, and Finder at the app, to drag in. From Terminal
// it's done when the person says so; in the terminal, when it can read
// what only Full Disk Access shows.
func (b *Boot) accessStep(t apps.Terminal) engine.Step {
	app := filepath.Join(b.Applications, t.Bundle)
	return engine.Step{
		Name: AccessStep, Title: "Full Disk Access", Waiting: "for " + t.Title,
		Check: func(ctx context.Context) check.Result {
			if b.in(t) {
				if steps.FullDiskAccess(b.Home).Check(ctx).State == check.OK {
					return check.Result{State: check.OK, Summary: "granted to " + t.Title}
				}
				return check.Result{State: check.Attention, Summary: "not granted to " + t.Title, Reason: fmt.Sprintf(
					"%s hasn't Full Disk Access: quit it, add it in System Settings › Privacy & Security › Full Disk Access, then open it and run kit bootstrap", t.Title)}
			}
			if st, _ := kitstate.Load[state](b.State, stateName); st.Access == t.Name {
				return check.Result{State: check.OK, Summary: "granted to " + t.Title}
			}
			return check.Result{State: check.Attention, Summary: "not granted yet"}
		},
		Apply: func(ctx context.Context, found check.Result) error {
			if b.in(t) {
				return errors.New(found.Reason)
			}
			_, _ = b.Run.Run(ctx, runner.Command{Name: "open", Args: []string{steps.AccessSettings}})
			_, _ = b.Run.Run(ctx, runner.Command{Name: "open", Args: []string{"-R", app}})
			todo := fmt.Sprintf("System Settings is open at Full Disk Access, and Finder at Applications: drag %s into the list, then press enter", t.Title)
			if _, err := b.Ask.Wait(ctx, AccessStep, "for "+t.Title+", before it first opens", todo, []Key{{Key: "enter", Does: "done"}}); err != nil {
				return err
			}
			return kitstate.Update(b.State, stateName, func(st *state) { st.Access = t.Name })
		},
	}
}

// handOverStep opens the terminal running kit bootstrap again, which
// carries the boot on there, its window maximized for kit apply's screen:
// done once that kit has arrived, or when this is it. The kit it runs is
// the one the boot carries on as (CarriesOnAs).
func (b *Boot) handOverStep(t apps.Terminal) engine.Step {
	return engine.Step{
		Name: TerminalStep, Title: t.Title,
		Check: func(context.Context) check.Result {
			if b.in(t) {
				return check.Result{State: check.OK, Summary: "opened"}
			}
			if st, _ := kitstate.Load[state](b.State, stateName); !st.Arrived.IsZero() {
				return check.Result{State: check.OK, Summary: "opened · kit carries on there"}
			}
			return check.Result{State: check.Attention, Summary: "not opened yet"}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			kit, err := b.CarriesOnAs(ctx)
			if err != nil {
				return err
			}
			env := []string{"SUDO_ASKPASS="}
			if b.OwnBuild() {
				env = append(env, "KIT_FROM="+b.Getenv("KIT_FROM"))
			}
			// The terminal takes on the environment it's opened with: not this
			// run's askpass, which ends with it; and the build of its own the
			// boot runs, so the boot handed over to knows it.
			w := apps.Window{Command: []string{kit, "bootstrap", "--handed-over"}, Env: env, Maximized: true}
			if err := t.Open(ctx, b.Run, b.Applications, w); err != nil {
				return fmt.Errorf("open %s: %w", t.Title, err)
			}
			b.Sink.Emit(event.Doing{Time: b.Now(), Step: TerminalStep, Says: "opening", Todo: fmt.Sprintf("macOS asks before %s first opens: choose Open", t.Title)})
			tick := time.NewTicker(250 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-tick.C:
					if st, _ := kitstate.Load[state](b.State, stateName); !st.Arrived.IsZero() {
						return nil
					}
				}
			}
		},
	}
}
