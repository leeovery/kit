// Package rendertest holds runs kit's faces are tested against, the runs
// their golden files hold: kit status on a Mac with some of everything, a
// new Mac's steps as kit apply runs them, 1Password's sign-in among them,
// the boot's order, and kit setup on a Mac in use. The capture tool
// (cmd/capturetool) plays the README's clips with the very same steps.
package rendertest

import (
	"time"

	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind/secret/onepassword"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/setup"
)

// At is when the runs start.
var At = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// StatusRun is a run of kit status as the pretty face shows it: steps in
// their areas, Config's with their parts, one of each state, and items of
// each sort.
func StatusRun() []event.Event {
	steps := []event.Step{
		{Name: "homebrew", Title: "Homebrew", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages},
		{Name: "brew", Title: "Formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Part: render.PartPackages},
		{Name: "cask", Title: "Casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew", Part: render.PartPackages},
		{Name: "defaults", Title: "macOS settings", Area: "Drift", Row: "Mac settings", Part: render.PartSettings},
		{Name: "config-private", Title: "Privacy", Area: "Config", Part: render.PartConfig},
		{Name: "config-sync", Title: "Sync", Area: "Config", Part: render.PartConfig},
		{Name: "mas", Title: "App Store", Area: "Drift", Row: "App Store apps", Part: render.PartPackages},
		{Name: "time-machine", Title: "Time Machine", Area: "Backups"},
		{Name: "disk", Title: "Disk space", Area: "Mac"},
		{Name: "fonts", Title: "fonts", Area: "Steps"},
	}
	brew := check.Result{
		State: check.Attention, Summary: "6 declared, 4 installed", Counts: map[string]int{"declared": 6, "installed": 4},
		Items: []check.Item{
			{ID: "brew:jq", Name: "jq", State: "missing", Action: "install"},
			{ID: "brew:ffmpeg", Name: "ffmpeg", State: "extra", Since: At.Add(-50 * time.Hour)},
			{ID: "brew:hello", Name: "hello", State: "extra", Quiet: "new"},
		},
	}
	ok := func(summary string, counts map[string]int) check.Result {
		return check.Result{State: check.OK, Summary: summary, Counts: counts}
	}
	return []event.Event{
		event.RunStarted{Time: At, Command: "status", Machine: "laptop", Version: "0.1.0", Steps: steps},
		event.StepFinished{Time: At, Step: "homebrew", Result: ok("/opt/homebrew", nil)},
		event.StepFinished{Time: At, Step: "cask", Result: ok("3 declared, all installed", map[string]int{"installed": 3})},
		event.StepFinished{Time: At, Step: "defaults", Result: ok("9 declared, all set", map[string]int{"installed": 9})},
		event.StepFinished{Time: At, Step: "config-private", Result: check.Result{State: check.OK, Summary: "private on GitHub", Glance: "private"}},
		event.StepFinished{Time: At, Step: "config-sync", Result: check.Result{State: check.Failed, Reason: "not a git repository"}},
		event.StepFinished{Time: At, Step: "brew", Result: brew},
		event.StepFinished{Time: At, Step: "mas", Result: check.Result{State: check.Deferred, Reason: "needs Homebrew"}},
		event.StepFinished{Time: At, Step: "time-machine", Result: ok("last backup 02:35", nil)},
		event.StepFinished{Time: At, Step: "disk", Result: check.Result{State: check.Attention, Summary: "8% free", Items: []check.Item{
			{ID: "disk:low", Name: "only 8% free on the startup disk", State: "problem", Detail: "free some space: kit status says where it went"},
		}}},
		event.StepFinished{Time: At, Step: "fonts", Result: ok("16 installed", nil)},
		event.RunFinished{Time: At, Duration: 1800 * time.Millisecond, Counts: map[check.State]int{check.OK: 6, check.Attention: 2, check.Failed: 1, check.Deferred: 1}},
	}
}

// NewMac are a new Mac's steps, as kit apply runs them once the boot's in
// the terminal: the boot's, checked again, kit's own, by the names kit
// gives them, the user's own, and two steps by hand: 1Password's sign-in,
// which the secrets wait for, and the rest, at the end.
func NewMac() []event.Step {
	return []event.Step{
		{Name: "boot-github", Title: "GitHub", Area: boot.AreaBooted},
		{Name: "boot-mac", Title: "This Mac", Area: boot.AreaBooted},
		{Name: "boot-homebrew", Title: "Homebrew", Area: boot.AreaBooted},
		{Name: "boot-kit-config", Title: "kit-config", Area: boot.AreaBooted},
		{Name: "homebrew", Title: "Homebrew", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew"},
		{Name: "file", Title: "Linked files", Area: "Drift", Part: render.PartFiles},
		{Name: "path", Title: "Shell PATH", Area: "Drift"},
		{Name: "touch-id-sudo", Title: "Touch ID for sudo", Area: "Drift"},
		{Name: "defaults", Title: "macOS settings", Area: "Drift", Row: "Mac settings"},
		{Name: "power", Title: "Power settings", Area: "Drift", Row: "Mac settings"},
		{Name: "brew", Title: "Homebrew formulae", Area: "Drift", Row: "Homebrew formulae", Tool: "Homebrew", Waits: []string{"homebrew"}},
		{Name: "cask", Title: "Homebrew casks", Area: "Drift", Row: "Homebrew casks", Tool: "Homebrew", Waits: []string{"homebrew"}},
		{Name: "mas", Title: "App Store apps", Area: "Drift", Row: "App Store apps"},
		{Name: "npm", Title: "npm packages", Area: "Drift", Row: "Developer tools", Waits: []string{"brew"}},
		{Name: "composer", Title: "Composer packages", Area: "Drift", Row: "Developer tools", Waits: []string{"brew"}},
		{Name: "login-item", Title: "macOS login items", Area: "Drift", Row: "Login items", Waits: []string{"cask", "mas"}},
		{Name: "secret", Title: "Secrets", Area: "Drift", Row: "Secrets", Waits: []string{"1password"}},
		{Name: "git-config", Title: "git config", Area: "Drift", Row: "Git", Waits: []string{"secret"}},
		{Name: "config", Title: "kit-config edits", Area: "Drift"},
		{Name: "config-sync", Title: "Sync", Area: "Config"},
		{Name: "time-machine", Title: "Time Machine", Area: "Backups"},
		{Name: "arq", Title: "Arq", Area: "Backups", Waits: []string{"cask"}},
		{Name: "prefs", Title: "App settings", Area: "Backups", Waits: []string{"cask"}},
		{Name: "disk", Title: "Disk space", Area: "Mac"},
		{Name: "load", Title: "Load", Area: "Mac"},
		{Name: "claude-code", Title: "claude-code", Area: "Steps"},
		{Name: "fonts", Title: "fonts", Area: "Steps"},
		{Name: "mise", Title: "mise", Area: "Steps", Waits: []string{"brew"}},
		{Name: "1password", Title: "1password", Area: "Manual", ByHand: []event.Hand{{Name: "1password", Title: "Sign in to 1Password"}}},
		{Name: "manual", Title: "By hand", Area: "Manual", Waiting: "at the end, once the rest is done", ByHand: []event.Hand{
			{Name: "sharing", Title: "Allow remote users Full Disk Access"},
			{Name: "editor", Title: "Install the editor from its site"},
			{Name: "karabiner", Title: "Allow Karabiner-Elements"},
		}},
	}
}

// SignIn is 1Password's sign-in come up at at, a step by hand the secrets
// wait for: why, as its notes say; its title and steps, a subheading among
// them, its driver's own; and kit tells by itself once it's done.
func SignIn(at time.Time) event.NeedsYou {
	about := onepassword.Driver{}.About().SignIn
	return event.NeedsYou{Time: at, Step: "1password", Item: "1password", Title: about.Title,
		Why:   "Every secret kit puts in place comes from 1Password, so it needs the app signed in and its command line turned on.",
		Steps: about.Steps, Tells: true}
}

// BootOrder are the boot's steps, in Terminal: signing in to GitHub, then
// the boot order, ending in the terminal it hands over to.
func BootOrder() []event.Step {
	return []event.Step{
		{Name: "github", Title: "GitHub", Area: boot.AreaSignIn, Waiting: "This Mac is set up from your kit-config, a private repository on GitHub. Sign in to GitHub, and kit fetches it."},
		{Name: "mac", Title: "This Mac", Area: boot.AreaBoot},
		{Name: "password", Title: "Password", Area: boot.AreaBoot, Waiting: "once"},
		{Name: "homebrew", Title: "Homebrew", Area: boot.AreaBoot},
		{Name: "apps", Title: "Apps", Area: boot.AreaBoot},
		{Name: "kit-config", Title: "kit-config", Area: boot.AreaBoot},
		{Name: "kit", Title: "kit", Area: boot.AreaBoot},
		{Name: "full-disk-access", Title: "Full Disk Access", Area: boot.AreaBoot, Waiting: "for Ghostty"},
		{Name: "terminal", Title: "Ghostty", Area: boot.AreaBoot},
	}
}

// SetupDone is kit setup on a Mac in use, at at, done: kit-config made
// anew, the Mac declared as it is, a part at a time, its questions
// answered, committed and pushed; three apps installed by hand.
func SetupDone(at time.Time) []event.Event {
	steps := []event.Step{
		{Name: setup.GitHubStep, Title: "GitHub", Area: setup.AreaSetup},
		{Name: setup.ConfigStep, Title: "kit-config", Area: setup.AreaSetup},
		{Name: setup.MacStep, Title: "This Mac", Area: setup.AreaSetup},
		{Name: setup.SnapshotStep, Title: "Declared", Area: setup.AreaDeclared, Waiting: "everything on this Mac, as it is"},
		{Name: setup.DataStep, Title: "Your data", Area: setup.AreaDeclared},
		{Name: setup.PrefsStep, Title: "Apps' settings", Area: setup.AreaDeclared},
		{Name: setup.FilesStep, Title: "Linked files", Area: setup.AreaDeclared},
		{Name: setup.InstallStep, Title: "To install", Area: setup.AreaDeclared},
		{Name: setup.CommitStep, Title: "Committed", Area: setup.AreaDeclared},
		{Name: setup.ByHandStep, Title: "Installed by hand", Area: setup.AreaDeclared},
		{Name: setup.SecretsStep, Title: "Secrets", Area: setup.AreaDeclared},
	}
	events := []event.Event{event.RunStarted{Time: at, Command: "setup", Machine: "MacBook Pro", Steps: steps}}
	ok := func(step, summary string) {
		events = append(events, event.StepStarted{Time: at, Step: step}, event.StepFinished{Time: at, Step: step, Result: check.Result{State: check.OK, Summary: summary}})
	}
	ok(setup.GitHubStep, "someone · GitHub's CLI's sign-in")
	ok(setup.ConfigStep, "someone/kit-config · new, private")
	ok(setup.MacStep, "laptop")
	events = append(events, event.StepStarted{Time: at, Step: setup.SnapshotStep})
	counts := map[string]int{}
	for _, p := range []struct {
		kind, label string
		n           int
		names       []string
	}{
		{"brew", "Homebrew formulae", 126, nil}, {"cask", "Homebrew casks", 23, nil}, {"mas", "App Store apps", 7, nil}, {"npm", "npm packages", 4, nil},
		{"login", "macOS login items", 5, nil}, {"mcp", "Claude MCP", 3, nil}, {"git-config", "git config", 12, nil}, {"defaults", "macOS settings", 186, nil},
		{"features", "Features", 2, []string{"Touch ID for sudo", "Remote Login"}}, {"backups", "Backups", 2, []string{"Time Machine", "Arq"}},
	} {
		counts[p.kind] = p.n
		events = append(events, event.Finding{Time: at, Step: setup.SnapshotStep, Part: p.label, Found: true, Count: p.n, Names: p.names})
	}
	events = append(events, event.StepFinished{Time: at, Step: setup.SnapshotStep, Result: check.Result{State: check.OK, Counts: counts}})
	ok(setup.DataStep, "3 folders · 56.3 GB")
	ok(setup.PrefsStep, "every night")
	ok(setup.FilesStep, "5")
	ok(setup.InstallStep, setup.NothingNew)
	events = append(events, event.StepStarted{Time: at, Step: setup.CommitStep},
		event.StepFinished{Time: at, Step: setup.CommitStep, Result: check.Result{State: check.OK, Summary: "370 declared · committed and pushed kit.toml and laptop/", Counts: counts}})
	byHand := check.Result{State: check.OK, Summary: "3, which kit doesn't look after: if kit sets this Mac up again, they're yours to install, through Homebrew or the App Store, if they're there"}
	for _, app := range []string{"Sketchpad", "Tally", "Mac Monitor"} {
		byHand.Items = append(byHand.Items, check.Item{ID: setup.ByHandStep + ":" + app, Name: app, State: "installed by hand"})
	}
	events = append(events, event.StepFinished{Time: at, Step: setup.ByHandStep, Result: byHand})
	ok(setup.SecretsStep, "kit keeps secrets in a store, never in kit-config: kit secret add")
	return append(events, event.RunFinished{Time: at, Counts: map[check.State]int{check.OK: len(steps)}})
}
