package boot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/runner"
)

// Picker waits for one of keys, as an Asker's Pick does.
type Picker interface {
	Pick(ctx context.Context, step string, keys []Key) (string, error)
}

// Account is the person's GitHub account, as kit signs in to it, the boot
// and kit setup alike: the sign-in GitHub's CLI or kit's keychain holds,
// else one through GitHub's device flow; kept, then handed to GitHub's CLI,
// which clones the config repository.
type Account struct {
	Run    runner.Runner
	Sink   event.Sink
	Ask    Picker
	GitHub GitHub
	Now    func() time.Time
}

func (a Account) doing(step, says string) {
	a.Sink.Emit(event.Doing{Time: a.Now(), Step: step, Says: says})
}

// Known is the GitHub sign-in at hand: kit's own, kept in the keychain, or
// else GitHub's CLI's; "" when there's none.
func (a Account) Known(ctx context.Context) string {
	res, err := a.Run.Run(ctx, runner.Command{Name: "security", Args: []string{"find-generic-password", "-s", keychainItem, "-w"}, Secret: true})
	if err == nil {
		if token := strings.TrimSpace(string(res.Stdout)); token != "" {
			return token
		}
	}
	if !runner.Has(a.Run, "gh") {
		return ""
	}
	res, err = a.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"auth", "token"}, Secret: true})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(res.Stdout))
}

// SignIn signs in to GitHub through its device flow, in step's row, the way
// chosen first: with the phone, scanning a QR code; or here, in the
// browser, which kit opens. The code comes only then, a new one if it
// expires before it's entered; either way can be chosen again while it
// waits.
func (a Account) SignIn(ctx context.Context, step string) (string, error) {
	how, err := a.Ask.Pick(ctx, step, signInKeys)
	if err != nil {
		return "", err
	}
	for {
		code, err := a.GitHub.code(ctx)
		if err != nil {
			return "", err
		}
		a.Sink.Emit(event.DeviceCode{Time: a.Now(), Step: step, URI: code.URI, Code: code.UserCode, Expires: a.Now().Add(time.Duration(code.ExpiresIn) * time.Second)})
		a.doing(step, "waiting for you to sign in")
		waiting, stop := context.WithCancel(ctx)
		asking := make(chan struct{})
		go func(how string) {
			defer close(asking)
			for {
				if how == "s" {
					_, _ = a.Run.Run(waiting, runner.Command{Name: "open", Args: []string{code.URI}})
				}
				var err error
				if how, err = a.Ask.Pick(waiting, step, signInKeys); err != nil {
					return
				}
			}
		}(how)
		token, err := a.GitHub.token(ctx, code)
		stop()
		<-asking
		if errors.Is(err, errExpired) {
			continue
		}
		return token, err
	}
}

// Keep keeps token in the login keychain, given on the security tool's
// input, never its command line, so a run started again needn't ask the
// phone. A keychain that won't take it, locked as over SSH, only means the
// next run asks again.
func (a Account) Keep(ctx context.Context, token string) {
	_, _ = a.Run.Run(ctx, runner.Command{Name: "security", Args: []string{"-i"}, Input: fmt.Sprintf("add-generic-password -U -s %s -a kit -w %s\n", keychainItem, token)})
}

// HandOver hands token to GitHub's CLI, in step's row, installing it
// through Homebrew if it's not there and signing it in unless it is; kit's
// keychain item then gives the sign-in up: from then on it's GitHub's
// CLI's.
func (a Account) HandOver(ctx context.Context, step, token string) error {
	if !runner.Has(a.Run, "gh") {
		a.doing(step, "installing GitHub's CLI")
		if _, err := a.Run.Run(ctx, runner.Command{Name: "brew", Args: []string{"install", "--formula", "gh"}, Timeout: 15 * time.Minute, SudoAsks: true}); err != nil {
			return fmt.Errorf("install GitHub's CLI: %w", err)
		}
	}
	if !a.CLISignedIn(ctx) {
		a.doing(step, "signing GitHub's CLI in")
		if _, err := a.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"auth", "login", "--hostname", "github.com", "--with-token"}, Input: token + "\n"}); err != nil {
			return fmt.Errorf("sign GitHub's CLI in: %w", err)
		}
	}
	_, _ = a.Run.Run(ctx, runner.Command{Name: "security", Args: []string{"delete-generic-password", "-s", keychainItem}})
	return nil
}

// CLISignedIn reports whether GitHub's CLI is signed in to github.com.
func (a Account) CLISignedIn(ctx context.Context) bool {
	_, err := a.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"auth", "status", "--hostname", "github.com"}})
	return err == nil
}

// gitCredentials has git ask GitHub's CLI for the sign-in, for the config
// repository alone: set in its own settings, not the person's.
const gitCredentials = "!gh auth git-credential"

// Clone clones the repository named repo, as owner/name, into dir through
// GitHub's CLI, signed in, in step's row, git set to sign in through it.
// It clones the repository's own branch alone, which is all it fetches
// from then on, so apps' settings, on its prefs branch, never come with it.
func (a Account) Clone(ctx context.Context, step, repo, dir string) error {
	a.doing(step, "cloning "+repo)
	if _, err := a.Run.Run(ctx, runner.Command{Name: "gh", Args: []string{"repo", "clone", repo, dir, "--", "--quiet", "--single-branch"}, Timeout: 10 * time.Minute}); err != nil {
		return fmt.Errorf("clone %s: %w", repo, err)
	}
	if _, err := a.Run.Run(ctx, runner.Command{Name: "git", Args: []string{"-C", dir, "config", "credential.https://github.com.helper", gitCredentials}}); err != nil {
		return fmt.Errorf("have git sign in through GitHub's CLI: %w", err)
	}
	return nil
}

// FirstKey picks as a run without a terminal does: the first of keys, the
// first time it's asked, so signing in shows its code in plain lines, to
// enter on any device; asked again, it waits, as no key will come.
type FirstKey struct{ picked bool }

func (f *FirstKey) Pick(ctx context.Context, _ string, keys []Key) (string, error) {
	if !f.picked && len(keys) > 0 {
		f.picked = true
		return keys[0].Key, nil
	}
	<-ctx.Done()
	return "", ctx.Err()
}
