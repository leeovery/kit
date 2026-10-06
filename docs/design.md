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

**Kinds.** A kind is a list of things of one sort. Built: Homebrew formulae (`brew`) and casks
(`cask`), App Store apps (`app`), npm and Composer global packages (`npm`, `composer`), Go tools
(`go`), GitHub CLI extensions (`gh`), tmux plugins (`tmux`), login items (`login`) and Claude
Code's MCP servers (`claude-mcp`) and plugins (`claude-plugin`), git's settings (`git`) and
macOS settings (`default`), power settings (`power`), and the backup and Spotlight exclusions
(`exclusion`, `spotlight`). Later: Claude Code's skills and secrets.
Each kind supplies five parts:

- **declared:** what the config lists, the shared file and then this Mac's;
- **actual:** what's on the Mac;
- **install** and **remove;**
- **adopt:** write a thing found on the Mac into the right file, in its sorted place.

The engine does the rest: install what's declared but missing, offer what's on the Mac but
undeclared for adopting, removing or snoozing, and report both.

| Kind | Declared as | Found by | Install, remove |
|---|---|---|---|
| `brew`, `cask` | `jq`, `owner/tap/tool`, `php@8.5` | `brew list`, `brew leaves` | `brew install`, `brew uninstall` |
| `app` | `xcode@497799835`: the name, then the id | `mas list` | `mas install`, `mas uninstall`, through sudo |
| `npm` | `typescript@5` (a version optional) | `npm ls --global` | `npm install --global`, `npm uninstall --global` |
| `composer` | `laravel/valet:^4.0` (a constraint optional) | `composer global show --direct` | `composer global require`, `composer global remove` |
| `go` | `golang.org/x/tools/cmd/goimports` (`@version` optional) | the programs in Go's bin folder, by the package each was built from | `go install`; removing deletes the program |
| `gh` | `owner/gh-extension` | `gh extension list` | `gh extension install`, `gh extension remove` |
| `tmux` | `set -g @plugin` lines in tmux's config | the folders in TPM's plugin folder | cloned as TPM does; removing deletes the folder |
| `login` | the app's bundle id, `com.example.app` | System Events' login items | System Events (JavaScript for Automation) |
| `claude-mcp` | a name, then `claude mcp add`'s options; a project's in `[claude mcp <folder>]` | `~/.claude.json`, read directly | `claude mcp add-json`, `claude mcp remove` (a project's in local scope, in its folder) |
| `claude-plugin` | `plugin@owner/repo`: the plugin and its marketplace's repository; `@owner/repo` a marketplace alone | Claude Code's records of plugins, marketplaces and what's enabled | the marketplace added when needed, then `claude plugin install`; `uninstall`, `marketplace remove` |
| `git` | `[git config]`: a key, then its value as one word, as `git config --global` takes them (`alias.st "status -sb"`) | `git config --global --list` | `git config --global --replace-all`, `--unset-all` |
| `power` | `[power settings]`: pmset's words, the source then the setting and value (`-c sleep 0`; `-a` every source, `-b` the battery, `-u` a UPS), named `charger:sleep` | `pmset -g custom`, by source | `sudo pmset`; undeclaring leaves the value |
| `exclusion` | `[backup exclusions]`: a path a line, `~` and `*` globs, expanded on every check (a plain path stands for itself, there or not) | Time Machine's fixed-path exclusions (`SkipPaths`), and each glob match excluded where it is (`tmutil isexcluded`); Arq inherits both | a plain path: `sudo tmutil addexclusion -p` (Full Disk Access for the terminal); a glob's match: `tmutil addexclusion`, no password, so `kit nightly` does it unattended; `removeexclusion -p` |
| `spotlight` | `[spotlight exclusions]`: a folder a line | Spotlight asked for files in each folder (`mdfind -count`); its own list is root's alone, so nothing extra is found | added to Spotlight's privacy list through sudo, taking effect after a restart (kit remembers, and says so); removing is System Settings' |
| `default` | `[macos settings]`: `defaults write`'s words (`com.apple.dock tilesize -int 60`; `-currentHost` first for this host's; `-dict-add` and an entry's XML; or a value as XML), named `domain:key[:entry]` | `defaults export` of each domain | `defaults write` (through sudo for a domain under `/Library`), then the app that reads it restarted or keyboard shortcuts reloaded; `defaults delete` |

- **Matching:** a kind may match on part of a name: an App Store app on its id (so an app the
  store renames still matches), npm and Composer packages without their versions, GitHub
  repositories and bundle ids without regard to case.
- **Changed on purpose:** a setting set otherwise than declared (git's) was changed on the
  Mac, so it's *diverged*: applying leaves it, and `kit reconcile` adopts it (the declared
  line takes the Mac's value) or reverts it (the declared value set again). A kind of
  settings is checked only while something of it is declared.
- **macOS settings** keep a record (`settings.json` in kit's state directory). A declared
  setting is diverged only once kit has seen it as declared: one the Mac has never had as
  declared (a new Mac, a new line) is missing when unset, or changed when set otherwise;
  applying sets either, and reconcile can adopt a changed one instead (as for power
  settings, and any kind whose things carry a value). Values compare as
  macOS reads them (a boolean and 1 or 0 alike). kit also watches a set of Apple's domains
  (the Dock, Finder, the global domain, the trackpad, the keyboard, screenshots, the menu
  bar clock, Control Center, Stage Manager, Spaces, keyboard shortcuts) and every declared
  setting's domain: the first look takes their values; a setting changed since, not
  declared, and not one macOS changes by itself (a list in kit: window frames, recent
  items, timestamps, the Dock's apps) is extra, and reconcile adopts it or puts it back as
  it was (a dict whose entries are declared is watched entry by entry).
- **A kind's program:** while it isn't installed, a kind with nothing declared for the Mac
  isn't checked at all, and one with something declared is deferred, saying what it needs.
  The kinds whose programs are formulae are applied after the formulae, so a new Mac has
  them first; login items after the casks and App Store apps they open.
- **A kind declared in a file of its own** (tmux's plugins, in tmux's config, where the
  plugin manager reads them): kit reads it and edits it in place, through a link to the
  file it leads to. `kit add` and adopting add a plugin's line after the last one (before
  the line that starts the plugin manager, for the first); `kit remove` and undeclaring
  take it out. When the file is in the config repository (a linked file), the change is
  committed with the rest.
- **MCP servers** are a line each: the server's name, then `claude mcp add`'s options
  (`--transport http <url> --header "…"`, or `--env K=V -- <command> <args>`), or JSON for
  what they can't say; kit's own `--off` first declares a server off: never installed, and
  left alone when it is. A server installed otherwise than declared is changed, and
  applying replaces it. Keys are only ever named, as `${VAR}`, which Claude Code fills from
  its environment: a line holding one in plain text is refused, whether declared, adopted or
  added, and what kit reads from `~/.claude.json` is never shown. A project's server waits
  while its folder isn't on the Mac. A server is added with Claude Code's own
  `claude mcp add`, then declared with `kit add claude-mcp` or reconcile's adopt;
  `kit claude-mcp on|off` declares one on or off and installs or removes it.
- **Claude's plugins** carry their marketplace in their names, as formulae carry their taps:
  there's no list of marketplaces. kit adds a plugin's marketplace when it installs it; a
  marketplace no plugin comes from is an unused dependency, offered for removal, unless
  declared alone (`@owner/repo`) to keep it for browsing. A plugin turned off is changed,
  and applying turns it on.
- **Finding what's meant:** `kit add app` finds an app from its name or id (several matches
  are a choice at a terminal, listed without one); `kit add login` takes an app's name, path
  or bundle id. Declaring a login item writes the app's name as the note.

**Features.** Each piece of kit stands alone, and is on when it's declared: a list by having
entries (the kinds, later the folders to restore), a piece with nothing to list by a switch
in a Mac's `[features]` section (`time-machine`, `arq`, `scratch`, `settings-capture`,
`oh-my-zsh`, `touch-id-sudo`, `remote-login`, `file-sharing`), so
turning one off loses nothing that isn't its own. Built-ins are generic and lasting; what's
specific to a user, or to the moment, goes in their config: checks of their own in
`[checks]`, jobs of their own in `[hourly]` and `[nightly]`. `kit feature
on|off <name>` (`--shared` for every Mac) changes one. kit is built for its author's Macs:
built-in support is for the tools they use, and the switches exist because those Macs
differ.

**Checks.** Steps with a check and no apply, each problem an item with a stable id saying
what's wrong and what to do: the Mac's (`disk`, `memory`, `file-events`, `load`), always;
the backups' (`time-machine`, `arq`: each runs on its own schedule, and kit checks it did,
only the plans Arq is scheduled to run), when switched on; the config repository's
(`config-sync`: commits that won't push; `config-private`); the scheduled runs' (`nightly`: the hourly run within two
hours, the nightly one finished within 26 and none stuck over six, each job's last outcome;
quiet until `kit nightly` has run on a schedule); with their features, `scratch` (mounted,
out of Spotlight and Time Machine, `tmp` writable, Claude Code's temporary files sent there;
applying, through sudo, makes the volume with its change log off, turns Spotlight off,
excludes it and makes `tmp`)
and `full-disk-access` (what settings capture needs); and the user's own, in a `[checks]`
section, a line each:
a name, then `--` and a command, which exits 0 when all's well, or prints what's wrong on
its first line (a minute's timeout), each failing one an item of the `checks` step. Every step has an area (Backups, Jobs, Mac, Drift,
Config, Checks), which the status document carries.

**The config repository's own edits** are drift, not a problem: linked files mean edits
land in the repository outside kit (through a link, by hand, by an app), and kit commits only
its own changes. The `config` step makes each file changed and not committed an item
(`config:shared/home/.zshrc`, edited, added or deleted, with the lines it adds and removes),
quiet for a day as drift is; `kit reconcile` shows an edit's diff and offers adopt (commit it,
then push, `--note` the message's end), revert (back to the last commit; a new file to the
Bin) or snooze. kit pulls with rebase before it pushes, so two Macs don't fight.

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
kit                          What needs attention, at a glance: a line an area, and what to run
kit add <kind> <name>...     Install, and declare for this Mac   [--shared] [--temp] [--note] [--group]
kit remove <kind> <name>...  Uninstall, and undeclare            [--shared]
kit reconcile [<kind>...] [<id>...]  Settle drift: adopt, remove, install, undeclare or snooze
kit list [kind]              What's declared for this Mac: file, group, note, installed or not
kit why <name>               Where it's declared, whether it's installed, what needs it
kit claude-mcp on|off <name>...  Declare Claude's MCP servers on or off, and install or remove them
kit feature on|off <name>... Switch features on or off for this Mac   [--shared]
kit nightly [job...]         Run the scheduled jobs due, then every check (the hourly launch)   [--plan]
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
those casks wait, saying why (requirement 4). App Store apps need the password for every
install, as mas runs as root; `kit add` and reconcile's installs ask up front the same way.

`kit add` installs each name, unless it's installed, and declares it in this Mac's file, or
for every Mac with `--shared` (taking it out of each Mac's own file); at a terminal it first
asks which of the file's groups each goes in ("To be sorted" first), and `--group` answers
without asking. `--temp` installs without declaring: quiet for 7 days, then reconcile asks.
`kit remove` uninstalls and takes each name out of this Mac's file; one declared for every Mac
needs `--shared`. Both handle each name on its own (one failing leaves the others), then commit
the config's changed files, with a message saying what, for which Mac and why, and push to
main, pulling with rebase first. A push that fails leaves the commit, and says so.

`kit reconcile` settles drift. At a terminal it goes through each item that needs attention
(`--all`: quiet ones too; name kinds, as in `kit reconcile brew`, for theirs alone), asking
what to do with it: declare it (for this Mac, or every Mac),
uninstall it, install it, undeclare it, snooze it for 7 days, leave it, or stop; for one
declared, which group. Every question comes first; then it does it all, as `kit add` and
`kit remove` would, and commits and pushes once. Without a terminal, or with `--json`, it lists
the items with their ids and choices, and `kit reconcile <id>... --adopt` (or `--remove`,
`--install`, `--undeclare`, `--snooze`; `--shared`, `--group`, `--note`) settles those, every
id checked before anything's done, in one run and one commit: how an agent carries out a
person's decisions.

`kit status` runs every step's check (named steps run with what they need): Homebrew, each
kind, and the config repository being private on GitHub (`config-private`, asked through
`gh`; one with no remote, or elsewhere, isn't public there).

Every command takes `--json` (one document), `--plain` (plain lines, as without a terminal)
and `--verbose` (commands' output kept whole in the run's log).

Exit statuses: 0 when all's well, 1 when something needs attention (a Mac with no name yet,
drift), 2 when kit couldn't do its job (no config repository, a config error, an unknown
command).

Bare `kit` runs every check, as `kit status` does, and shows a line an area (Backups, Mac,
Drift, Config, Checks): the steps' short forms when all's well ("Time Machine 16:00 · Arq
01:05"), else what needs attention (a drift item's kind, name, what's wrong and for how long)
and what to run (`kit reconcile`, `kit status`); `--json` is `kit status --json`'s document.

`kit nightly` is what the hourly launch runs: the jobs due, in order (one failing never stops
the others), then every check. It installs only what a kind lets it install with no one
there, cheap and safe, and removes nothing: today, a new folder matching a backup
exclusion's glob, excluded where it is. Besides the user's own jobs, two are built in, nightly, each
with its feature: `scratch`'s clean-up (`/Volumes/Scratch/tmp` cleared of items untouched for a
week; Claude Code's session folders judged one by one over 30 days, kept while their
transcript changed) and, last, `settings-capture`'s `prefsync capture` (niced, stopped after
15 minutes). The hourly jobs run every time; the nightly ones once a day,
when the nightly run falls due (`nightly_at` in `kit.toml`, 03:00 unless it says), or on the
first run after it's been missed, as by a Mac asleep then. Each job's outcome is kept in kit's
state (`nightly.json`). It installs and removes nothing. `--plan` says what's due; naming jobs
runs those, now. Each run leaves its report (`report.txt` in kit's logs folder, `kit status`'s
plain lines) for a notification's click. `--alerts` prints, in place of the run, what the
hourly launch's app should notify about, as JSON (`[{id, title, body}]`): each problem a check
found, and each step that failed or was deferred, at once; the drift that needs attention as
one digest, its id naming the list, changing at most once a day and gone when nothing's
left (kept in kit's state, `alerts.json`); a job's own failure is the runs check's, not
twice.

Later: `share`, `edit`, `update`, `secrets`, `prefs`,
`bootstrap`, `takeover`, `retire`, `decisions`, `decide`, `fleet`.

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
kit.toml                the format, the oldest kit that reads it, the Macs, the primary
shared/declarations     what every Mac declares (made once something's declared for every Mac)
laptop/declarations     what the Mac named laptop declares, and so for each Mac
```

A folder for every Mac, and one shared by all, each the same shape: its declarations file,
its `home` folder of files linked into the home folder, and in later milestones its own
tools. A folder holding a declarations file for a Mac `kit.toml` doesn't name is refused.

A declarations file is sections, each a header in brackets and its lines; the header's
words are the nesting, a tool then its list, and for a kind with one, a project folder:

```
[paths]
~/.local/bin
/opt/homebrew/bin

[homebrew formulae]
# Shell
jq                # for the skills' scripts
oven-sh/bun/bun

[homebrew casks]
ghostty

[claude mcp]
docs --transport http https://docs.example.com/mcp
mail --env MAIL_KEY=${MAIL_KEY} -- npx -y mail-mcp

[claude mcp ~/Code/site]
pages --transport http https://pages.example.com/mcp
```

The sections, in the order kit writes them: `features` (the switches), `paths` (kit's PATH, in order, `~` expands; the
shared file's, then the Mac's), `homebrew formulae`, `homebrew casks`, `app store apps`,
`npm packages`, `composer packages`, `go tools`, `github extensions`, `macos login items`,
`claude mcp` (and `claude mcp <folder>`), `claude plugins`, `checks`, `hourly`, `nightly`
(jobs of the user's own: a name, then `--` and a command, `~/` expanding in any word). A section kit doesn't know is refused, never
skipped; so is a line before any section, a section twice, or a file named for a Mac
`kit.toml` doesn't know.

Each section's lines read one way. A name list's: one name a line, a version or a
constraint allowed after it (`typescript@5`, `laravel/valet:^4.0`). A path's: the whole line,
spaces and all. A command's: a name, then options, split into words as a shell splits them.
A `#` at the start of a line or after a space (outside quotes, in a command) starts a
comment: on its own line it heads a group, after an entry it's the note saying why it's
there. Within a group, names sort by their last part (`oven-sh/bun/bun` sorts as `bun`). A
name is declared in the shared declarations or a Mac's, never both. A Mac can't be called
`shared`.

kit edits these files in place, leaving every other line as it is; it checks the result
reads back, and writes the file whole or not at all. A file it can't read, it doesn't edit.

```toml
format = 1
minimum_kit = "0.1.0"
primary = "laptop"
nightly_at = "03:00"   # when the nightly run falls due: 03:00 unless set

[macs.laptop]
description = "MacBook Pro"

[macs.studio]
description = "Mac Studio"
```

### Linked files

Every file in a scope's `home` folder is linked into the home folder at the same path
(`shared/home/.zshrc` to `~/.zshrc`), a file at a time, never a folder whole, so an app's
other files beside one stay the app's; the folder is the declaration, with no list beside
it. Files git ignores in the repository are never linked. A file in two scopes' home folders
is refused. The step `file` finds each one:

- **missing:** not linked; applying links it, making its folders.
- **changed:** linked otherwise with nothing lost by linking it again (a copy the same as
  the repository's, a link to one the same, or a file under `~/.ssh` or `~/.gnupg` others
  can read); applying puts it right.
- **diverged:** a different file where the link belongs, such as an app that saves by
  replacing the file. Applying never overwrites it; `kit reconcile` offers adopt (the Mac's
  copy into the repository, then linked), revert (the Mac's copy to the Bin, then linked) or
  snooze.
- **dead:** a link into the repository whose file has gone (found among the links kit made,
  and in every folder a home folder mirrors); applying removes it.

`kit add file <path>` moves a file, or every file in a folder, into this Mac's home folder
(`--shared`: every Mac's) and links it back; `kit remove file <path>` puts a copy back in
place of the link and takes the file out. Both commit and push.

### kit's PATH, and the shell's

At start-up kit builds its own PATH from the config's `paths` and the system's directories,
ignoring what it inherited, so it behaves the same from a terminal, launchd or an agent, and
finds tools installed partway through a run.

The shell uses the same list, the code review's "one PATH list": the `path` step writes it
(`~` expanded, colon-separated) to `path` in kit's state directory, and the shell's startup
puts the file's contents ahead of the PATH it inherits, reading it without running anything,
so a config that doesn't read never leaves a shell without its PATH. Relative entries
(`node_modules/.bin`) are kept for the shell and left out of kit's own PATH. A `$`, a colon,
or a `~` that doesn't start the home folder is refused. `kit add path <dir>` puts a
directory last (`--shared`: on every Mac), `kit remove path <dir>` takes it off; both write
the file and commit.

The `oh-my-zsh` feature checks Oh My Zsh is installed, and applying runs its installer,
unattended, keeping `.zshrc` and the login shell.

Three features change the system, each applied through sudo: `touch-id-sudo` (sudo takes a
fingerprint, by a line in `/etc/pam.d/sudo_local`, which macOS updates leave alone),
`remote-login` (SSH) and `file-sharing` (SMB), each a launchd service on unless launchd lists
it disabled. A step whose apply needs an administrator's password says so (`Admin`), and
`kit apply` and `kit reconcile` settle the password before anything is applied, as for the
kinds whose installs need it: asked once at a terminal; without one, the step waits.

## Output

The core never prints. It emits events: a run starting, with the steps it will run; a step
starting; a check's result, with its items and reason; every external command, with its
arguments (redacted), exit code, duration and output; a step deferred, with the unmet need; a
step failed, with the error; the run finishing, with its summary. Faces subscribe:

- **pretty** at a terminal: colour brought down to what the terminal shows, and no glyphs
  that need a particular font (block elements and box drawing are in every Mac's). `kit
  status` and `kit apply` open with kit's wordmark, the command, the Mac and the time beside
  it; shorter commands with a line and a rule. A spinner counts the steps done and names
  those running; then the steps under their areas (Backups, Jobs, Mac, Drift, Config,
  Checks, as bare `kit` orders them), each a mark, its title and its summary (wrapping under
  itself), and under it what applying did and what needs attention, a thing a line, its
  name and what's wrong in a column beside it (or under it, when there's no room), the
  loud before the quiet, a long list cut to six and counted; then a rule and the summary,
  marked;
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

**2. Packages, read-write — done.** The list writer; syncing the config repository (commit,
pull with rebase, push); drift counted after 24 hours, with snoozes and temporary installs;
Homebrew's installs and removes; `kit apply` with `--plan`, an administrator's password asked
up front; `kit add` and `kit remove` (`--shared`, `--temp`, `--note`, `--group`, the group
asked at a terminal); `kit reconcile`, at a terminal and by id; `kit list` and `kit why`
(#12–#19).

**3. The other kinds — built.** Every kind through one table; a step can come after another
while applying, and a check can defer its own step; the administrator's password up front for
any kind; App Store apps; npm and Composer packages; Go tools and GitHub CLI extensions; tmux
plugins; login items (#21–#26).

**4. Claude Code's MCP servers — built.** The `claude-mcp` kind, user-level and per
project; `kit claude-mcp on|off`; reconcile by kind (#30–#32).

**5. One declarations file per Mac — built.** The config repository is `kit.toml`, `shared`
and a file per Mac, each sections of flat lines; MCP servers as `claude mcp add`'s
options.

**6. Claude Code's plugins — built.** The `claude-plugin` kind, marketplaces carried in
plugins' names.

**7. Health checks and switches — built.** The Mac's checks and the config repository's sync
(#36); `[features]`, `kit feature on|off`, Time Machine and Arq (#37); checks of the user's
own (#38); the scan made to see untracked files (#39); bare `kit` (#40).

**8. `kit nightly` — built.** Jobs of the user's own (`[hourly]`, `[nightly]`), the nightly
record and its rule, `kit nightly`; Arq checked by its own schedules (#41); the Scratch
clean-up and settings capture (#42); the checks on the runs, Scratch and Full Disk Access
(#43); `--alerts`, drift's daily digest and the report (#44).

**9. Steps — in progress.** A folder per Mac in the config repository; linked files; the
config repository's own edits settled by `kit reconcile`; the shell and PATH, git, macOS
settings and backup exclusions as kinds, secrets, what a person must do by hand; a command
adding lines to `[checks]`, `[hourly]` and `[nightly]`.

**Then:** settings capture and restore in kit; the
primary reading the other Macs; the bootstrap and its install script, tested in a virtual
machine; data restore and moving to a new Mac; an agent's daily check, with decisions queued
for a person. Claude Code's skills wait for the installer they'll go through. Each gets a
short plan before code.

## Open-source hygiene

- No personal data in the repo or its history, ever (see House rules in `CLAUDE.md`);
  `scripts/personal-data-scan` checks the files and every commit, in `scripts/gates`.
- Public, under the MIT licence. Released by the maintainer, once kit is ready, with mint,
  GoReleaser and a Homebrew tap.
