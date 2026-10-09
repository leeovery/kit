package boot

import (
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
	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
	kitstate "github.com/leeovery/kit/internal/state"
	"github.com/leeovery/kit/internal/steps"
)

// The boot order's steps, after This Mac, by name.
const (
	PasswordStep = "password"
	HomebrewStep = "homebrew"
	AppsStep     = "apps"
	ConfigStep   = "kit-config"
	AccessStep   = "full-disk-access"
	TerminalStep = "terminal"
)

// Order is the boot order, planned from the config repository signing in
// read: this Mac; the password, once; Homebrew; the apps kit.toml names;
// kit-config, cloned, its files linked; then, with a terminal named, Full
// Disk Access for it, and the hand-off to it. Each needs the one before.
func (b *Boot) Order() []engine.Step {
	cfg := b.config()
	order := []engine.Step{b.macStep(), b.passwordStep(), b.homebrewStep()}
	if wanted := b.apps(cfg); len(wanted) > 0 {
		order = append(order, b.appsStep(wanted))
	}
	order = append(order, b.configStep())
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

// terminal is the terminal kit.toml names, as kit knows it.
func (b *Boot) terminal(cfg *config.Config) (apps.Terminal, bool) {
	if cfg == nil {
		return apps.Terminal{}, false
	}
	t, ok := apps.Terminals[cfg.Terminal]
	return t, ok
}

// in reports whether kit is running in the terminal t.
func (b *Boot) in(t apps.Terminal) bool {
	return b.Getenv("TERM_PROGRAM") == t.Program
}

// runningIn is the app kit's running in, by its name, as macOS names it
// asking for what it may do.
func (b *Boot) runningIn() string {
	program := b.Getenv("TERM_PROGRAM")
	if program == "Apple_Terminal" {
		return "Terminal"
	}
	for _, t := range apps.Terminals {
		if t.Program == program {
			return t.Title
		}
	}
	return "your terminal"
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
				Todo: fmt.Sprintf("macOS asks whether %s may administer your computer: choose Allow", b.runningIn())})
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
		return errors.New("sudo refused the password: run kit bootstrap again")
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
				return errors.New("needs this Mac's password: run kit bootstrap at a terminal")
			}
			b.doing(HomebrewStep, "installing")
			return brew.Install(ctx, b.Run)
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
// done when each is in the Applications folder.
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
		Apply: func(ctx context.Context, found check.Result) error {
			var casks []string
			for _, it := range found.Items {
				casks = append(casks, it.Name)
			}
			b.doing(AppsStep, "installing")
			_, err := b.Run.Run(ctx, runner.Command{Name: "brew", Args: slices.Concat([]string{"install", "--cask"}, casks), Timeout: 30 * time.Minute})
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

// gitCredentials has git ask GitHub's CLI for the sign-in, for the config
// repository alone: set in its own settings, not the person's.
const gitCredentials = "!gh auth git-credential"

// configStep clones the config repository, through GitHub's CLI, handed
// the sign-in, then links its files into the home folder, so the terminal
// opens set up: done when it's cloned and its files are linked. A new Mac
// is added to kit.toml, uncommitted: git has no name to commit with till
// kit-config's settings are applied.
func (b *Boot) configStep() engine.Step {
	return engine.Step{
		Name: ConfigStep, Title: "kit-config",
		Check: func(ctx context.Context) check.Result {
			if !b.cloned() {
				return check.Result{State: check.Attention, Summary: "not cloned yet"}
			}
			files, err := b.files()
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			found := files.Check(ctx)
			if found.State == check.Failed || found.State == check.Deferred {
				return found
			}
			says := []string{"cloned", plural(found.Counts["linked"], "file linked", "files linked")}
			for _, it := range found.Items {
				if it.Action != "" {
					return check.Result{State: check.Attention, Summary: "cloned · files to link", Items: found.Items}
				}
			}
			if n := found.Counts[linked.Diverged]; n > 0 {
				says = append(says, plural(n, "differs: kit reconcile settles it", "differ: kit reconcile settles them"))
			}
			if st, _ := kitstate.Load[state](b.State, stateName); st.NewMac != "" {
				if cfg, err := config.Load(b.Config); err == nil && cfg.Knows(st.NewMac) {
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
// gives up: from then on it's GitHub's CLI's.
func (b *Boot) clone(ctx context.Context) error {
	if _, err := os.Stat(b.Config); err == nil {
		return fmt.Errorf("%s is there, and isn't a clone of kit-config: move it aside, then boot again", b.Config)
	}
	token, login := b.signedIn()
	if token == "" {
		return errors.New("not signed in to GitHub")
	}
	repo := b.repo(login)
	if st, _ := kitstate.Load[state](b.State, stateName); st.Repo != "" {
		repo = st.Repo
	}
	if !runner.Has(b.Run, "gh") {
		b.doing(ConfigStep, "installing GitHub's CLI")
		if _, err := b.Run.Run(ctx, runner.Command{Name: "brew", Args: []string{"install", "--formula", "gh"}, Timeout: 15 * time.Minute}); err != nil {
			return fmt.Errorf("install GitHub's CLI: %w", err)
		}
	}
	if _, err := b.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"auth", "status", "--hostname", "github.com"}}); err != nil {
		b.doing(ConfigStep, "signing GitHub's CLI in")
		if _, err := b.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"auth", "login", "--hostname", "github.com", "--with-token"}, Input: token + "\n"}); err != nil {
			return fmt.Errorf("sign GitHub's CLI in: %w", err)
		}
	}
	_, _ = b.Run.Run(ctx, runner.Command{Name: "security", Args: []string{"delete-generic-password", "-s", keychainItem}})
	b.doing(ConfigStep, "cloning "+repo)
	if _, err := b.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "clone", repo, b.Config, "--", "--quiet"}, Timeout: 10 * time.Minute}); err != nil {
		return fmt.Errorf("clone %s: %w", repo, err)
	}
	if _, err := b.Run.Run(ctx, runner.Command{Name: "git", Args: []string{"-C", b.Config, "config", "credential.https://github.com.helper", gitCredentials}}); err != nil {
		return fmt.Errorf("have git sign in through GitHub's CLI: %w", err)
	}
	return nil
}

// addNewMac adds a new Mac, as chosen, to the clone's kit.toml, unless
// it's there already.
func (b *Boot) addNewMac() error {
	st, err := kitstate.Load[state](b.State, stateName)
	if err != nil || st.NewMac == "" {
		return err
	}
	cfg, err := config.Load(b.Config)
	if err != nil {
		return err
	}
	if cfg.Knows(st.NewMac) {
		return nil
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

// accessSettings is System Settings, opened at Full Disk Access's list.
const accessSettings = "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_AllFiles"

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
			_, _ = b.Run.Run(ctx, runner.Command{Name: "open", Args: []string{accessSettings}})
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
// carries the boot on there: done once that kit has arrived, or when this
// is it.
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
			// The terminal takes on open's environment: not this run's askpass,
			// which ends with it.
			if _, err := b.Run.Run(ctx, runner.Command{Name: "open", Args: t.Launch(b.Applications, b.Self, "bootstrap", "--handed-over"), Env: []string{"SUDO_ASKPASS="}}); err != nil {
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

// plural is n of something, one or many.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
