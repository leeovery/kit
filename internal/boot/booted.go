package boot

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/steps"
)

// Booted are the boot's steps as the terminal handed over to takes them, a
// light of their own beside kit apply's: checked again, not applied, as
// the boot applied them; named apart from kit apply's steps, each needing
// the one before.
func (b *Boot) Booted() []engine.Step {
	booted := slices.Concat(b.SignIn(), b.Order())
	for i := range booted {
		booted[i].Name = "boot-" + booted[i].Name
		booted[i].Area, booted[i].Apply, booted[i].Needs = AreaBooted, nil, nil
		if i > 0 {
			booted[i].Needs = []string{booted[i-1].Name}
		}
	}
	return booted
}

// InTerminal reports whether kit is running in the terminal kit.toml
// names: where the boot carries on, handed over to or not.
func (b *Boot) InTerminal() bool {
	t, ok := b.terminal(b.config())
	return ok && b.in(t)
}

// How long the password manager's CLI may take to answer, and to be let
// in, which waits on you.
const (
	cliTimeout   = 15 * time.Second
	letInTimeout = time.Minute
)

// How often signing in to the password manager looks again, and how long it
// waits after the app's refused to let its CLI in, before it asks again.
var signInPoll, refusedPause = 2 * time.Second, 10 * time.Second

// PasswordManagerStep is the password manager kit.toml names, signed in,
// with its CLI on, which kit reads secrets through: false when kit.toml
// names none kit knows. Its check asks the CLI who's signed in, which asks
// nothing of you. Applying opens the app and says what to do, then waits:
// for the CLI to list an account, then for the app to let it in, which the
// app asks you.
func (b *Boot) PasswordManagerStep() (engine.Step, bool) {
	cfg := b.config()
	if cfg == nil {
		return engine.Step{}, false
	}
	pm, ok := apps.PasswordManagers[cfg.PasswordManager]
	if !ok {
		return engine.Step{}, false
	}
	return engine.Step{
		Name: pm.Name, Title: pm.Title, Area: steps.AreaDrift,
		Check: func(ctx context.Context) check.Result {
			switch {
			case !runner.Has(b.Run, pm.Program):
				return check.Result{State: check.Attention, Summary: "its CLI isn't installed"}
			case b.cli(ctx, pm, cliTimeout, "whoami") != nil:
				return check.Result{State: check.Attention, Summary: "not signed in"}
			}
			return check.Result{State: check.OK, Summary: "signed in"}
		},
		Apply: func(ctx context.Context, _ check.Result) error {
			if !runner.Has(b.Run, pm.Program) {
				return fmt.Errorf("%s isn't installed: declare its cask, %s, then run kit apply", pm.Program, pm.CLI)
			}
			return b.signInTo(ctx, pm)
		},
	}, true
}

// signInTo waits for the password manager to let its CLI in: once it lists
// an account, the app asks you; till then, the app is opened, and what to
// do said.
func (b *Boot) signInTo(ctx context.Context, pm apps.PasswordManager) error {
	opened := false
	for {
		wait := signInPoll
		switch {
		case b.listsAccount(ctx, pm):
			b.Sink.Emit(event.Doing{Time: b.Now(), Step: pm.Name, Says: "asking you",
				Todo: fmt.Sprintf("%s asks whether %s may use it: allow it", pm.Title, b.runningIn())})
			if b.cli(ctx, pm, letInTimeout, "vault", "list", "--format", "json") == nil {
				return nil
			}
			wait = refusedPause
		case !opened:
			opened = true
			_, _ = b.Run.Run(ctx, runner.Command{Name: "open", Args: []string{filepath.Join(b.Applications, pm.Bundle)}})
			b.Sink.Emit(event.Doing{Time: b.Now(), Step: pm.Name, Says: "not signed in", Todo: pm.SignIn})
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// listsAccount reports whether the password manager's CLI lists an
// account: the app's, once it's signed in with its CLI turned on.
func (b *Boot) listsAccount(ctx context.Context, pm apps.PasswordManager) bool {
	res, err := b.Run.Run(ctx, runner.Command{Name: pm.Program, Args: []string{"account", "list", "--format", "json"}, Timeout: cliTimeout})
	if err != nil {
		return false
	}
	var accounts []json.RawMessage
	return json.Unmarshal(res.Stdout, &accounts) == nil && len(accounts) > 0
}

// cli runs the password manager's CLI with args, for whether it answered.
func (b *Boot) cli(ctx context.Context, pm apps.PasswordManager, timeout time.Duration, args ...string) error {
	_, err := b.Run.Run(ctx, runner.Command{Name: pm.Program, Args: args, Timeout: timeout})
	return err
}
