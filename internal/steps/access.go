package steps

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/leeovery/kit/internal/apps"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
)

// FullDiskAccessName names the step checking the Full Disk Access of what
// kit runs in: the terminal, or the SSH session.
const FullDiskAccessName = "full-disk-access"

// unnamedTerminal is the terminal kit runs in, said when kit can't name it.
const unnamedTerminal = "the terminal kit runs in"

// The switch giving SSH sessions Full Disk Access.
const (
	remoteAccessTitle   = "Allow Full Disk Access for remote users"
	remoteAccessSetting = "System Settings › General › Sharing › Remote Login › ⓘ"
)

// Granting is how Full Disk Access is given to what kit runs in, each place
// kit runs from having its own way: Step is the step telling whether it's
// given; Title, Why and Steps the to-do giving it, and Do that in a line,
// for kit status. Raised is whether the to-do is the full-disk-access
// step's own, raised at the run's end: at a terminal. Over SSH the switch
// is the config's own step by hand, when it declares one, and kit only
// names it; from kit.app, AppAccess raises kit.app's.
type Granting struct {
	Step           string
	Title, Why, Do string
	Steps          []string
	Raised         bool
}

// GrantingFor is Granting where kit runs: kit.app's, scheduled; else, as
// getenv says, an SSH session's, or the terminal's, named as TERM_PROGRAM
// says when kit knows it.
func GrantingFor(getenv func(string) string, scheduled bool) Granting {
	switch {
	case scheduled:
		return Granting{Step: AppAccessName, Title: appAccessTitle, Why: appAccessWhy, Do: appAccessDo}
	case getenv("SSH_CONNECTION") != "":
		return Granting{
			Step: FullDiskAccessName, Title: remoteAccessTitle,
			Why:   "SSH sessions here can't read what only Full Disk Access shows, which keeping folders out of backups and reading apps' settings need.",
			Do:    remoteAccessSetting + ": turn on Allow full disk access for remote users",
			Steps: []string{remoteAccessSetting, "Turn on Allow full disk access for remote users"},
		}
	}
	app := apps.RunningIn(getenv)
	if app == apps.Unnamed {
		app = unnamedTerminal
	}
	return Granting{
		Step: FullDiskAccessName, Raised: true,
		Title: "Give " + app + " Full Disk Access, then reopen it",
		Why:   "Only apps with Full Disk Access can keep folders out of backups or read apps' settings. " + strings.ToUpper(app[:1]) + app[1:] + " takes it up only once it's reopened.",
		Do:    "System Settings › Privacy & Security › Full Disk Access: turn on " + app + ", then quit it and open it again",
		Steps: []string{"System Settings › Privacy & Security › Full Disk Access, turn on " + app, "Quit " + app + " and open it again"},
	}
}

// Then is g ending by applying the steps that wait for it, steps, as its
// last step: g as it is, with none.
func (g Granting) Then(steps []string) Granting {
	if len(steps) > 0 && len(g.Steps) > 0 {
		g.Steps = append(slices.Clone(g.Steps), "Run kit apply "+strings.Join(steps, " "))
	}
	return g
}

// denied is how the full-disk-access step stands without it, as summary
// says: the to-do giving it, raised as the run ends when it's the step's
// own, else named.
func (g Granting) denied(summary string) check.Result {
	if !g.Raised {
		return problem(FullDiskAccessName, summary, [3]string{"denied", g.Title, g.Do})
	}
	return yours(FullDiskAccessName, summary, "denied", g.Do, check.ToDo{Title: g.Title, Why: g.Why, Steps: g.Steps, Group: groupBackups})
}

// Refusal is a program kit ran refusing for want of Full Disk Access, the
// probe having read: the full-disk-access step finds it not granted once
// one has, as it isn't, whatever the probe says.
type Refusal struct {
	mu sync.Mutex
	by string
}

// Note notes program refused.
func (r *Refusal) Note(program string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.by = program
}

// program is the program that refused, "" while none has.
func (r *Refusal) program() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.by
}

// FullDiskAccess checks what kit runs in can read what only Full Disk Access
// shows, as keeping folders out of backups and a capture or restore of apps'
// settings run there need, and that no program kit ran has refused for want
// of it (refused, nil for none to note). Without it, it's what g says to do.
// Not in the hourly launch's runs: kit.app's grant is AppAccess's.
func FullDiskAccess(home string, g Granting, refused *Refusal) engine.Step {
	return engine.Step{
		Name: FullDiskAccessName, Title: "Full Disk Access", Area: AreaBackups,
		Check: func(context.Context) check.Result {
			if by := refused.program(); by != "" {
				return g.denied("not granted: " + by + " needs it")
			}
			switch granted, found := ProbeFullDiskAccess(home); {
			case granted:
				return check.Result{State: check.OK, Summary: "granted"}
			case !found:
				return check.Result{State: check.OK, Summary: "nothing protected to read"}
			}
			return g.denied("not granted")
		},
	}
}
