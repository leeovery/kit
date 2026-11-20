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
// given, and Wait what's held for it says; Title, Why and Steps the to-do
// giving it, and Do that in a line, for kit status. Raised is whether the
// to-do can be the full-disk-access step's own, raised at the run's end once
// something's held for it (Access.Held): at a terminal, with steps waiting
// for it (For). Over SSH the switch is the
// config's own step by hand, when it declares one, and kit only names it;
// from kit.app, AppAccess raises kit.app's.
type Granting struct {
	Step, Wait     string
	Title, Why, Do string
	Steps          []string
	Raised         bool
	// app is the terminal it's given to, "" for an SSH session or kit.app.
	app string
}

// GrantingFor is Granting where kit runs: kit.app's, scheduled; else, as
// getenv says, an SSH session's, or the terminal's, named as TERM_PROGRAM
// says when kit knows it.
func GrantingFor(getenv func(string) string, scheduled bool) Granting {
	switch {
	case scheduled:
		return Granting{Step: AppAccessName, Wait: "needs kit.app's Full Disk Access", Title: appAccessTitle, Why: appAccessWhy, Do: appAccessDo}
	case getenv("SSH_CONNECTION") != "":
		return Granting{
			Step: FullDiskAccessName, Wait: "needs Full Disk Access", Title: remoteAccessTitle,
			Do:    remoteAccessSetting + ": turn on Allow full disk access for remote users",
			Steps: []string{remoteAccessSetting, "Turn on Allow full disk access for remote users"},
		}
	}
	app := apps.RunningIn(getenv)
	if app == apps.Unnamed {
		app = unnamedTerminal
	}
	return Granting{
		Step: FullDiskAccessName, Wait: "needs Full Disk Access", app: app,
		Title: "Give " + app + " Full Disk Access, then reopen it",
		Do:    "System Settings › Privacy & Security › Full Disk Access: turn on " + app + ", then quit it and open it again",
		Steps: []string{"System Settings › Privacy & Security › Full Disk Access, turn on " + app, "Quit " + app + " and open it again"},
	}
}

// For is the full-disk-access step's g, for what needs it here: again, the
// steps waiting for it, the backup exclusions', so keeping folders out of
// backups; and prefs, whether apps' settings are saved here. With steps
// waiting, it ends by applying them, and, at a terminal, can be raised; with
// none, it's named alone, as apps' settings need it only when a capture or
// restore runs there. kit.app's is as it is.
func (g Granting) For(again []string, prefs bool) Granting {
	if g.Step != FullDiskAccessName {
		return g
	}
	var uses []string
	if len(again) > 0 {
		uses = append(uses, "keep folders out of backups")
	}
	if prefs {
		uses = append(uses, "read apps' settings")
	}
	if g.app != "" {
		g.Why = "Only apps with Full Disk Access can " + strings.Join(uses, " or ") + ". " + strings.ToUpper(g.app[:1]) + g.app[1:] + " takes it up only once it's reopened."
	} else {
		g.Why = "SSH sessions here can't " + strings.Join(uses, " or ") + " without Full Disk Access for remote users."
	}
	if len(again) == 0 {
		return g
	}
	run := "kit apply " + strings.Join(again, " ")
	g.Steps = append(slices.Clone(g.Steps), "Run "+run)
	g.Do += ", then run " + run
	g.Raised = g.app != ""
	return g
}

// denied is how the full-disk-access step stands without it, as summary
// says: the to-do giving it, raised as the run ends when it can be the
// step's own and something's been held for it (held), else named.
func (g Granting) denied(summary string, held bool) check.Result {
	if !g.Raised || !held {
		return problem(FullDiskAccessName, summary, [3]string{"denied", g.Title, g.Do})
	}
	return yours(FullDiskAccessName, summary, "denied", g.Do, check.ToDo{Title: g.Title, Why: g.Why, Steps: g.Steps, Group: groupBackups})
}

// Access is whether what kit runs in has Full Disk Access, in Home: as far
// as the probe can tell, and unless a program kit ran has refused for want
// of it all the same (Refused); and whether anything's been held for want
// of it (Held).
type Access struct {
	Home string

	mu   sync.Mutex
	by   string
	held bool
}

// Held notes something was held for want of Full Disk Access, as an
// exclusion to apply: from then on its to-do is raised, when it can be.
func (a *Access) Held() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.held = true
}

// holding reports whether anything's been held for want of it.
func (a *Access) holding() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.held
}

// Refused notes program refused for want of Full Disk Access: from then on
// it isn't granted, whatever the probe says.
func (a *Access) Refused(program string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.by = program
}

// refusedBy is the program that refused, "" while none has.
func (a *Access) refusedBy() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.by
}

// Granted reports whether it's granted: so with nothing there for the
// probe to read, a program's refusal left to say otherwise.
func (a *Access) Granted() bool {
	return a.refusedBy() == "" && HasFullDiskAccess(a.Home)
}

// FullDiskAccess checks what kit runs in has Full Disk Access, as a, as
// keeping folders out of backups and a capture or restore of apps' settings
// run there need. Without it, it's what g says to do: raised, when it can
// be, only once something's been held for it, as with nothing to exclude
// there's nothing waiting. Not in the hourly launch's runs: kit.app's grant
// is AppAccess's.
func FullDiskAccess(g Granting, a *Access) engine.Step {
	return engine.Step{
		Name: FullDiskAccessName, Title: "Full Disk Access", Area: AreaBackups,
		Check: func(context.Context) check.Result {
			if by := a.refusedBy(); by != "" {
				return g.denied("not granted: "+by+" needs it", a.holding())
			}
			switch granted, found := ProbeFullDiskAccess(a.Home); {
			case granted:
				return check.Result{State: check.OK, Summary: "granted"}
			case !found:
				return check.Result{State: check.OK, Summary: "nothing protected to read"}
			}
			return g.denied("not granted", a.holding())
		},
	}
}
