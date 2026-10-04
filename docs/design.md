# kit: design

kit sets a Mac up from a config repository and keeps it that way. One command rebuilds a Mac
from nothing, applies changes as the config changes, notices what has drifted from it, and says
when something needs attention: pretty at a terminal, plain and `--json` without one, so an
agent drives it with the same commands a person runs.

It's built for its author's Macs, public because nothing personal is in it: every path, name
and service comes from the config repository, which stays private. General only where that
costs nothing. Pre-1.0: commands, config and output can change between releases.

## Two repositories

- **kit**, this one: the engine. Released through a Homebrew tap; a brand-new Mac downloads a
  release before anything is signed in.
- **The config repository**, each user's own and always private: what each Mac declares (lists
  of packages, settings, linked files, references to secrets), per-Mac files, and bespoke steps.
  kit refuses to run against one that's public. Shared through releases of the engine, never
  merged with an upstream: when kit changes the config format, it migrates the config itself.

## Concepts

**Macs.** `kit.toml` names every Mac the config knows (`laptop`, `studio`) and one primary,
which hears about every Mac's problems. A Mac's name is settled first, before any config is
read, and kept in kit's state; a replacement Mac can take an old one's name.

**Kinds.** A kind is a list of things of one sort: Homebrew formulae (`brew`) and casks
(`cask`) first; App Store apps, npm, Composer and Go tools, GitHub CLI extensions, Claude Code's
MCP servers, plugins and skills, tmux plugins, login items, secrets, macOS settings and backup
exclusions later. Each kind supplies five parts:

- **declared:** what the config lists, the shared file and then this Mac's;
- **actual:** what's on the Mac;
- **install** and **remove;**
- **adopt:** write a thing found on the Mac into the right file, in its sorted place.

The engine does the rest: install what's declared but missing, offer what's on the Mac but
undeclared for adopting, removing or snoozing, and report both.

**Steps.** Everything kit applies is a step in one pipeline. A step has:

- **check:** is it done and right? Mandatory, cheap, no side effects. Checks feed `kit status`
  and the hourly run, so a broken step shows the day it breaks.
- **apply** (optional): does it, safe to repeat.
- **manual** (optional): what a person must do when it can't be automated, shown while the
  check fails.
- **needs:** what must be true first (other steps). Unmet needs defer the step, with the
  reason, and the next run picks it up.
- **Macs:** which Macs it runs on.

A kind becomes a step whose check compares declared with actual.

**A failed step never ends the run.** It's recorded, the steps that need it are deferred with
the reason, the rest carry on, and the summary always prints. A check that panics fails its
step alone.

**The engine** orders steps so each comes after what it needs (a cycle, or a need that isn't
a step, is refused when the pipeline is built), and starts each, in that order, once what it
needs is done and one of its jobs (4) is free: independent checks run side by side, and with
one job they run in order. Applying a step that doesn't stand ok runs its apply, then its
check again. What a person must do by hand shows as an item while the step needs attention.
A run that's stopped (an interrupt) fails the steps it hadn't started.

## Commands

Built:

```
kit add <kind> <name>...     Install, and declare for this Mac   [--shared] [--temp] [--note] [--group]
kit remove <kind> <name>...  Uninstall, and undeclare            [--shared]
kit apply [step...] [--plan] Install what's declared and missing (--plan: say what, do nothing)
kit status [step...]         How this Mac stands against the config: what needs attention
kit log                      What the last run did: every check, every command, with timings
kit machine [<name>]         This Mac's name: shown, or set (one of kit.toml's Macs)
kit version                  As --version
```

`kit apply` checks every step and applies each that doesn't stand ok, or has something to
do, then checks it again: each step then says what it did ("installed: jq"). It never removes
or adopts: reconcile does those. At a terminal, when a missing cask installs through a
package, kit asks for an administrator's password once, through `sudo -v`, before anything is
applied, and keeps sudo's hold fresh while it runs; without a terminal it asks nothing, and
those casks wait, saying why (requirement 4).

`kit add` installs each name, unless it's installed, and declares it in this Mac's file, or
for every Mac with `--shared` (taking it out of each Mac's own file); at a terminal it first
asks which of the file's groups each goes in ("To be sorted" first), and `--group` answers
without asking. `--temp` installs without declaring: quiet for 7 days, then reconcile asks.
`kit remove` uninstalls and takes each name out of this Mac's file; one declared for every Mac
needs `--shared`. Both handle each name on its own (one failing leaves the others), then commit
the config's changed files, with a message saying what, for which Mac and why, and push to
main, pulling with rebase first. A push that fails leaves the commit, and says so.

`kit status` runs every step's check (named steps run with what they need): Homebrew, the
formulae (`brew`), the casks (`cask`), and the config repository being private on GitHub
(`config-private`, asked through `gh`; one with no remote, or elsewhere, isn't public
there).

Every command takes `--json` (one document), `--plain` (plain lines, as without a terminal)
and `--verbose` (commands' output kept whole in the run's log).

Exit statuses: 0 when all's well, 1 when something needs attention (a Mac with no name yet,
drift), 2 when kit couldn't do its job (no config repository, a config error, an unknown
command).

Later: `kit` (at a glance), `apply`, `reconcile`, `add`, `remove`, `share`, `list`, `why`,
`edit`, `update`, `secrets`, `prefs`, `nightly`, `bootstrap`, `takeover`, `retire`,
`decisions`, `decide`, `fleet`.

Every question has a flag that answers it, so everything runs without prompts. Without a
terminal kit never prompts: a question it can't ask is an error naming the flag. `--json`
output is one versioned document whose items carry stable ids (`brew:jq`).

## Files

- **The config repository:** `$KIT_CONFIG`, else `$XDG_CONFIG_HOME/kit`, else
  `~/.config/kit`. Never under a folder a data restore fills on a new Mac.
- **State:** `$XDG_STATE_HOME/kit/`, else `~/.local/state/kit/`: the Mac's name now; later
  drift first-seen times, snoozes and decisions.
- **Logs:** `~/Library/Logs/kit/`, one JSON-lines file per run, kept 30 days.

A relative `XDG_CONFIG_HOME` or `XDG_STATE_HOME` is ignored, as the XDG spec says.

### The config repository

```
kit.toml          the format version, the oldest kit that reads it, the Macs, the primary
brew              formulae every Mac declares
brew.laptop       formulae only laptop declares
cask, cask.studio casks, likewise
paths             PATH's directories, in order, one a line (~ expands)
```

Lists hold one name a line. A `#` at the start of a line or after a space starts a comment;
comments on their own lines head groups, and one after a name records why it's there. Within a
group, names sort by their last part (`oven-sh/bun/bun` sorts as `bun`). A name may appear in
the shared file or a Mac's, never both, and a Mac's file must name a Mac `kit.toml` knows.

```toml
format = 1
minimum_kit = "0.1.0"
primary = "laptop"

[macs.laptop]
description = "MacBook Pro"

[macs.studio]
description = "Mac Studio"
```

### kit's PATH

At start-up kit builds its own PATH from the config's `paths` and the system's directories,
ignoring what it inherited, so it behaves the same from a terminal, launchd or an agent, and
finds tools installed partway through a run.

## Output

The core never prints. It emits events: a run starting, with the steps it will run; a step
starting; a check's result, with its items and reason; every external command, with its
arguments (redacted), exit code, duration and output; a step deferred, with the unmet need; a
step failed, with the error; the run finishing, with its summary. Faces subscribe:

- **pretty** at a terminal: colour brought down to what the terminal shows, a spinner while
  checks run, results in pipeline order whatever order they finish in, and no glyphs that need
  a particular font;
- **plain** without one, or with `--plain`: the same words, one item a line, no colour, no
  animation, never a prompt;
- **json** with `--json`: one document at the end, `"schema": 1`;
- **the log**, always: every event as a JSON line. A command's output is capped at 64 KB a
  stream, its full size noted; `--verbose` keeps it whole.

`kit status` exits 0 when nothing needs attention, 1 when something does, and 2 when it
couldn't do its job, as every command does.

## Homebrew

Formulae and casks are compared by full name: `owner/tap/name` for a tap's, the plain name for
Homebrew's own, so a tap's `php` and Homebrew's own `php` are never taken for each other. A
declared name that matches nothing is looked up (`brew info`, all at once; one at a time when
one is unknown, as brew then answers nothing) to resolve aliases and renames before it's
called missing, and a name Homebrew doesn't know is missing and "unknown". Four states:

- **ok:** declared and installed (installed as another formula's dependency counts);
- **missing:** declared, not installed;
- **extra:** installed on request, needed by nothing installed, not declared;
- **unused dependency:** a formula installed as a dependency that nothing installed needs now
  (what `brew autoremove` removes).

**Installing and removing.** Before its first install in a run kit runs `brew update`, as
installs from stale formulae fail. Formulae install in one `brew install --formula`, a tap's
first: a tap's formula then claims a name it shares with one of Homebrew's own before
anything pulls that in as a dependency. Casks install in one `brew install --cask`. A missing
item kit can't install now says why, and is left: a name another tap's formula holds (two
formulae of one name share a keg), or a cask that installs through a package, needing an
administrator's password kit hasn't got. Items kit would install carry the action `install`,
and a step with one is applied even while its items are quiet, so a package declared on
another Mac is installed the same day. Removing is `brew uninstall`; Homebrew's refusal, when
something needs the package, is passed on.

**Drift counts after a day.** An item a kind's check finds (missing, extra, an unused
dependency) is new for its first 24 hours: shown, quiet, without needing attention, so a
throwaway install that's soon removed never does. kit remembers when it first saw each item,
the snoozes (7 days) and the temporary installs (quiet for 7 days) in
`$XDG_STATE_HOME/kit/drift.json`, written whole under a lock; it forgets an item once a check
of its kind no longer finds it.

What's installed comes from four listings run side by side: formulae with full names, leaves,
leaves installed on request, and casks. kit runs brew in an environment of its own: the
user's, its own PATH, and Homebrew kept from updating itself, nagging or colouring its
output.

## Architecture

### Packages

| Package | Owns |
|---|---|
| `cmd/kit` | `main`: builds the system (environment, clock, home, terminal, the real runner) and the command tree; exits with its status |
| `internal/cli` | Cobra commands, thin: flags, calling the engine, picking the faces |
| `internal/config` | Finding the config repository, reading `kit.toml`, the one list reader (the shared file, then the Mac's), validation, `paths`, and the Mac's name in the state directory |
| `internal/check` | What a check finds: its state, summary and items |
| `internal/event` | The events the core emits, and the sinks they go to |
| `internal/render` | The pretty, plain and json faces |
| `internal/logs` | The run log, its retention, and reading it back for `kit log` |
| `internal/redact` | Hiding secrets in what's logged |
| `internal/runner` | The one place a process starts: kit's PATH, a clean environment, timeouts, output captured, each command reported as an event. `runner/runnertest` is the fake |
| `internal/engine` | Steps, ordering by needs, running checks side by side, deferral, failure isolation, the summary |
| `internal/kind` | The kind interface and the comparison that makes a kind a step |
| `internal/kind/brew` | Homebrew's formulae and casks |
| `internal/status` | The status document behind `kit status` and `--json` |
| `internal/testguard` | Every package's `TestMain`: keeps tests off the real system (see Test isolation in `CLAUDE.md`) |

## Requirements and their tests

Lessons from the shell-script setup kit replaces, each a requirement with a named test as it's
built:

| # | Requirement | Test |
|---|---|---|
| 1 | A failed step never ends the run: it's recorded, what needs it is deferred, the rest carry on, the summary prints | `engine.TestAFailedStepNeverEndsTheRun` |
| 2 | The Mac's name is settled before any config is read, and every reader uses it | `cli.TestStatusNeedsTheMacsNameFirst` |
| 3 | kit sets its own PATH, from the same list the shell uses | `cli.TestStatusRunsProgramsOnKitsOwnPath` |
| 4 | Questions up front, then unattended: no step asks its own | `cli.TestApplyWithoutATerminalNeverPrompts`, `cli.TestApplyAtATerminalAsksForThePasswordUpFront` |
| 5 | Full Disk Access for the terminal kit runs in, checked before anything that needs it | later |
| 6 | 1Password signed in, with an unlocked session, before secrets | later |
| 7 | GitHub over SSH only once the key exists; GitHub's host keys seeded | later |
| 8 | A secret that fails to fetch keeps its previous value; a partial fetch never shrinks the file | later |
| 9 | Settings restore and capture wait for a fully synced store | later |
| 10 | Notifications allowed as a step, with a check, while someone's at the screen | later |
| 11 | Data restore works on a different Mac, at the right moment | later |
| 12 | Steps declare their Macs: no step asks whether it applies | later |
| 13 | Heavy, independent steps run last or alongside, never blocking quick ones | later |
| 14 | Nothing needs Go installed: a new Mac downloads a release | later |
| 15 | The network check is an HTTPS request, not a ping | later |

## Milestones

**1. Status, read-only — done.** The repository and its isolation; config and the Mac's
name; the runner and its fake; events, the faces and the log; the engine; Homebrew's formulae
and casks; `kit status`, `kit log`, `kit machine`, `kit version`; release tooling, for when kit
is ready. One pull request each, in that order (#1–#10). Nothing on the Mac changes but kit's
own state and logs. Unreleased: the maintainer releases, with mint, once kit is ready.

**2. Packages, read-write — next.** `kit apply` installs what's missing; `kit add` and
`kit remove` (`--shared`, `--temp`, `--note`); `kit reconcile` with drift counted after 24
hours and snoozes; `kit list`, `kit why`, `--plan`.

**Then:** the other kinds; one set of checks behind bare `kit` and an hourly run, with
notifications and the primary reading the other Macs; steps for linked files, the shell, git,
macOS settings, backup exclusions and secrets; settings capture and restore; syncing the config
repository; the bootstrap and its install script, tested in a virtual machine; data restore and
moving to a new Mac; an agent's daily check, with decisions queued for a person. Each gets a
short plan before code.

## Open-source hygiene

- No personal data in the repo or its history, ever (see House rules in `CLAUDE.md`);
  `scripts/personal-data-scan` checks the files and every commit in CI.
- Public, under the MIT licence. Released by the maintainer, once kit is ready, with mint,
  GoReleaser and a Homebrew tap.
