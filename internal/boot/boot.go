// Package boot is a new Mac's bootstrap: a self-test, then the boot's steps,
// one after another, from GitHub's sign-in on the phone to the hand-off to
// the terminal kit.toml names. Signing in comes first, as it reads the
// config repository the rest of the boot is planned from. A step done is
// found so by its check, so a boot stopped anywhere picks up where it was.
// It asks what it must through an Asker, in the row it's about, and says
// what it's doing through events.
package boot

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/runner"
	kitstate "github.com/leeovery/kit/internal/state"
)

// The boot's steps, by name.
const (
	GitHubStep = "github"
	MacStep    = "mac"
)

// Asker is how the boot asks the person at the Mac something, in the row of
// the step it's about. Without a terminal, each question fails, naming the
// flag that answers it.
type Asker interface {
	// Choose asks step's question, its answers under it: the answer's place.
	Choose(ctx context.Context, step, question string, answers []Answer) (int, error)
	// Name asks for a name, typed.
	Name(ctx context.Context, step, question string) (string, error)
	// Pick waits for one of keys, the keys at the foot saying what each
	// does, till ctx is done: the key pressed.
	Pick(ctx context.Context, step string, keys []Key) (string, error)
	// Secret asks for a password, typed without being shown.
	Secret(ctx context.Context, step, question string) (string, error)
	// Wait shows what the person's to do, says in step's row and todo
	// under it, till they press one of keys: the key pressed.
	Wait(ctx context.Context, step, says, todo string, keys []Key) (string, error)
}

// Answer is one answer to a question: its label, and what it means.
type Answer struct {
	Label, Does string
}

// Key is a key a step waits for, and what pressing it does.
type Key struct {
	Key, Does string
}

// The areas the boot's steps are in: signing in, which fetches the config
// the boot needs, then the boot itself.
const (
	AreaSignIn = "Sign in"
	AreaBoot   = "Boot order"
)

// signInKeys are the ways to sign in to GitHub: with the phone, a QR code
// to scan; or here, in the browser.
var signInKeys = []Key{{Key: "enter", Does: "with your phone"}, {Key: "s", Does: "here, in Safari"}}

// Boot is a new Mac's boot, and what it runs with.
type Boot struct {
	Run    runner.Runner
	Sink   event.Sink
	Ask    Asker
	GitHub GitHub
	Now    func() time.Time
	// State is kit's state folder, Data its data folder, and Config where
	// the config repository is cloned.
	State, Data, Config string
	// Repo is the config repository, as owner/name: the person's own
	// kit-config when "".
	Repo string
	// Mac is this Mac's name when it's given, rather than asked.
	Mac string
	// Getenv reads the environment kit runs in.
	Getenv func(string) string
	// Home is the home folder, Applications where apps are installed, and
	// SudoLocal sudo's file of local settings, where Touch ID is turned on.
	Home, Applications, SudoLocal string
	// Self is kit, as the terminal handed over to runs it.
	Self string
	// Keep keeps the password given, for the rest of the run, for sudo
	// when the programs the boot runs ask for it (askpass).
	Keep func(password string)
	// HandedOver is whether this is the boot handed over to the terminal,
	// started by the boot before it.
	HandedOver bool

	mu    sync.Mutex
	token string
	login string
	// held is whether sudo has the password, given this run, and
	// triedTouchID whether turning Touch ID on was tried, which macOS can
	// refuse.
	held, triedTouchID bool
}

// SignIn is signing in, the boot's first steps, which read the config
// repository the boot order is planned from.
func (b *Boot) SignIn() []engine.Step {
	return []engine.Step{b.gitHubStep()}
}

func (b *Boot) doing(step, says string) {
	b.Sink.Emit(event.Doing{Time: b.Now(), Step: step, Says: says})
}

// signedIn is the sign-in the boot holds, and who it's for.
func (b *Boot) signedIn() (token, login string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.token, b.login
}

func (b *Boot) hold(token, login string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.token, b.login = token, login
}

// archive is where the boot keeps its copy of the config repository, read
// before it's cloned.
func (b *Boot) archive() string { return filepath.Join(b.Data, "bootstrap", "kit-config") }

// config is the config repository: the clone once there is one, else the
// boot's copy; nil when there's neither.
func (b *Boot) config() *config.Config {
	for _, dir := range []string{b.Config, b.archive()} {
		if cfg, err := config.Load(dir); err == nil {
			return cfg
		}
	}
	return nil
}

// repo is the config repository's name on GitHub, for login.
func (b *Boot) repo(login string) string {
	if b.Repo != "" {
		return b.Repo
	}
	return login + "/kit-config"
}

// gitHubStep signs in to GitHub, on the phone, and reads the config
// repository: done when GitHub takes the sign-in kit holds, and the config
// repository is there to read.
func (b *Boot) gitHubStep() engine.Step {
	return engine.Step{
		Name: GitHubStep, Title: "GitHub", Area: AreaSignIn,
		Waiting: "This Mac is set up from your kit-config, a private repository on GitHub. Sign in to GitHub, and kit fetches it.",
		Check: func(ctx context.Context) check.Result {
			token := b.known(ctx)
			if token == "" {
				return check.Result{State: check.Attention, Summary: "not signed in"}
			}
			login, err := b.GitHub.login(ctx, token)
			switch {
			case errors.Is(err, errSignedOut):
				return check.Result{State: check.Attention, Summary: "signed out"}
			case err != nil:
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			b.hold(token, login)
			cfg := b.config()
			if cfg == nil {
				return check.Result{State: check.Attention, Summary: login + " · kit-config not read yet"}
			}
			return check.Result{State: check.OK, Summary: login + " · kit-config has " + and(cfg.MacNames())}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			token, login := b.signedIn()
			if token == "" {
				var err error
				if token, err = b.signIn(ctx); err != nil {
					return err
				}
				if login, err = b.GitHub.login(ctx, token); err != nil {
					return fmt.Errorf("find who signed in: %w", err)
				}
				b.hold(token, login)
				b.keep(ctx, token)
			}
			repo := b.repo(login)
			b.doing(GitHubStep, "reading "+repo)
			private, err := b.GitHub.private(ctx, token, repo)
			switch {
			case errors.Is(err, errNotFound):
				return fmt.Errorf("%s isn't on GitHub, or this sign-in can't see it: name yours with --config owner/name", repo)
			case err != nil:
				return fmt.Errorf("find %s: %w", repo, err)
			case !private:
				return fmt.Errorf("%s is public on GitHub, and a config repository is always private: make it private, then boot again", repo)
			}
			if err := b.GitHub.fetch(ctx, token, repo, b.archive()); err != nil {
				return fmt.Errorf("read %s: %w", repo, err)
			}
			return kitstate.Update(b.State, stateName, func(st *state) { st.Repo = repo })
		},
	}
}

// signIn signs in to GitHub through its device flow, the way chosen first:
// with the phone, scanning a QR code; or here, in the browser, which kit
// opens. The code comes only then, a new one if it expires before it's
// entered; either way can be chosen again while it waits.
func (b *Boot) signIn(ctx context.Context) (string, error) {
	how, err := b.Ask.Pick(ctx, GitHubStep, signInKeys)
	if err != nil {
		return "", err
	}
	for {
		code, err := b.GitHub.code(ctx)
		if err != nil {
			return "", err
		}
		b.Sink.Emit(event.DeviceCode{Time: b.Now(), Step: GitHubStep, URI: code.URI, Code: code.UserCode, Expires: b.Now().Add(time.Duration(code.ExpiresIn) * time.Second)})
		b.doing(GitHubStep, "waiting for you to sign in")
		waiting, stop := context.WithCancel(ctx)
		go func(how string) {
			for {
				if how == "s" {
					_, _ = b.Run.Run(waiting, runner.Command{Name: "open", Args: []string{code.URI}})
				}
				var err error
				if how, err = b.Ask.Pick(waiting, GitHubStep, signInKeys); err != nil {
					return
				}
			}
		}(how)
		token, err := b.GitHub.token(ctx, code)
		stop()
		if errors.Is(err, errExpired) {
			continue
		}
		return token, err
	}
}

// keychainItem is where kit keeps GitHub's sign-in, in the login keychain,
// till GitHub's CLI has it.
const keychainItem = "kit-github"

// known is the GitHub sign-in at hand: kit's own, kept in the keychain, or
// else GitHub's CLI's; "" when there's none.
func (b *Boot) known(ctx context.Context) string {
	if token, _ := b.signedIn(); token != "" {
		return token
	}
	res, err := b.Run.Run(ctx, runner.Command{Name: "security", Args: []string{"find-generic-password", "-s", keychainItem, "-w"}, Secret: true})
	if err == nil {
		if token := strings.TrimSpace(string(res.Stdout)); token != "" {
			return token
		}
	}
	if !runner.Has(b.Run, "gh") {
		return ""
	}
	res, err = b.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"auth", "token"}, Secret: true})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(res.Stdout))
}

// keep keeps token in the login keychain, given on the security tool's
// input, never its command line, so a boot started again needn't ask the
// phone. A keychain that won't take it, locked as over SSH, only means the
// next boot asks again.
func (b *Boot) keep(ctx context.Context, token string) {
	_, _ = b.Run.Run(ctx, runner.Command{Name: "security", Args: []string{"-i"}, Input: fmt.Sprintf("add-generic-password -U -s %s -a kit -w %s\n", keychainItem, token)})
}

// macStep settles which of the config's Macs this is, or that it's a new
// one: done when kit has a name for this Mac that the config knows, or that
// was chosen for a new Mac.
func (b *Boot) macStep() engine.Step {
	return engine.Step{
		Name: MacStep, Title: "This Mac",
		Check: func(context.Context) check.Result {
			name, err := config.ReadMachine(b.State)
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			if name == "" {
				return check.Result{State: check.Attention, Summary: "not named yet"}
			}
			st, err := kitstate.Load[state](b.State, stateName)
			if err != nil {
				return check.Result{State: check.Failed, Reason: err.Error()}
			}
			if cfg := b.config(); cfg != nil && !cfg.Knows(name) && st.NewMac != name {
				return check.Result{State: check.Attention, Summary: "named " + name + ", which kit-config doesn't know"}
			}
			return check.Result{State: check.OK, Summary: name}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			cfg := b.config()
			if cfg == nil {
				return errors.New("kit-config hasn't been read")
			}
			name, isNew := b.Mac, false
			if name == "" {
				macs := cfg.MacNames()
				answers := make([]Answer, 0, len(macs)+1)
				for _, m := range macs {
					answers = append(answers, Answer{Label: m, Does: cfg.Macs[m].Description})
				}
				answers = append(answers, Answer{Label: "new", Does: "a Mac kit-config doesn't have yet"})
				i, err := b.Ask.Choose(ctx, MacStep, "which of your Macs is this?", answers)
				if err != nil {
					return err
				}
				if i < len(macs) {
					name = macs[i]
				}
			}
			if name == "" || !cfg.Knows(name) {
				var err error
				if name == "" {
					if name, err = b.Ask.Name(ctx, MacStep, "its name, as kit-config will know it"); err != nil {
						return err
					}
				}
				if !config.ValidName(name) {
					return fmt.Errorf("%q can't name a Mac: lower case letters, digits and dashes, as in studio", name)
				}
				isNew = true
			}
			if err := config.WriteMachine(b.State, name); err != nil {
				return err
			}
			if !isNew {
				return nil
			}
			return kitstate.Update(b.State, stateName, func(st *state) { st.NewMac = name })
		},
	}
}

// and lists names as words do: a, b and c.
func and(names []string) string {
	switch len(names) {
	case 0:
		return "no Macs"
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// state is what a boot keeps between its runs, beside the checks: the
// config repository it read; a new Mac's name till kit-config has it; the
// terminal given Full Disk Access, as the person said, before its first
// launch; and when kit, handed over to, took the boot on there.
type state struct {
	Repo    string    `json:"repo,omitempty"`
	NewMac  string    `json:"new_mac,omitempty"`
	Access  string    `json:"access,omitempty"`
	Arrived time.Time `json:"arrived,omitzero"`
}

const stateName = "bootstrap.json"
