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
(`cask`), App Store apps (`mas`), npm and Composer global packages (`npm`, `composer`), Go tools
(`go`), GitHub CLI extensions (`gh`), tmux plugins (`tmux`), login items (`login-item`), Claude
Code's MCP servers (`claude-mcp`), Claude's skills for every project (`claude-skill`, installed
by agntc), git's settings (`git-config`) and
macOS settings (`defaults`), power settings (`power`), the backup and Spotlight exclusions
(`backup-exclusion`, `spotlight-exclusion`), and secrets (`secret`).
Not Claude Code's plugins: its user settings (`~/.claude/settings.json`) declare them, and
it fetches the plugins they enable itself, so a list of kit's would be a second copy (a
`claude-plugin` kind was built, then retired).
Each kind supplies five parts:

- **declared:** what the config lists, the shared file and then this Mac's;
- **actual:** what's on the Mac;
- **install** and **remove;**
- **adopt:** write a thing found on the Mac into the right file, in its sorted place.

The engine does the rest: install what's declared but missing, offer what's on the Mac but
undeclared for adopting, removing or snoozing, and report both.

**A kind is self-contained.** Its package describes it (`kind.Descriptor`): its name and
label, its section's header and how its lines read, its group (packages, settings or
secrets), its command's help and any commands it brings (`kit claude-mcp on|off`, for a
kind whose things can be declared off), the steps it needs and is applied after, the row of
applying's screen it sits in, and how it's made from what every kind is made from (the
runner, the home, XDG's config folder, kit's state folder, and what kinds share, as
Homebrew's formulae and casks share Homebrew). `internal/kinds` lists them, in the order a
run takes them; the config's parsing, the commands, the run and the reports' parts read
them from there, so adding a kind is its package and a line in that list, and removing one
the reverse. The faces don't: a kind's step, and the steps it needs, carry its row and
tool to them, as its title does. The rows and their order are the screen's design, the
face's own, and a kind picks one of them. A declarations file has the kinds' sections between
the config's own (`features`, `backups`, `paths` first; `prefs …`, `steps`, `manual`, `checks`,
`hourly`, `nightly` last): the settings' first, then the packages', then the secrets', each
in the run's order.

**A kind only when declared.** A kind is in a run (check, apply, status, drift, the record)
only while the config has its section for the Mac, in the shared file or its own, even an
empty one: an empty section says to look after the kind with nothing of it declared yet,
so what's installed is drift `kit reconcile` offers, and, with its program not installed,
it's fine without waiting for it. With no section, kit doesn't look at the kind at all,
its program installed or not (`kit list` of it refuses), and the drift record forgets what
it held of it; tmux's plugins, declared in tmux's config,
are looked after while it declares one or starts TPM. A step a kind needs, as Homebrew's
for its formulae and casks, is in the run only with a kind needing it. `kit <kind> add`
writes the section when there's none, `--temp` too, so reconcile asks once the
throwaway's time is up; removing a kind's last thing leaves its section as it was, header
and all, empty, when it's the last declaring the kind for the Mac (a Mac's own, the shared
file having none, or the shared file's), so the kind's still looked after. A section any
removal leaves without lines stays too while it holds comments of its own, its header and
those comments, as nothing written by hand goes without being asked.

| Kind | Declared as | Found by | Install, remove |
|---|---|---|---|
| `brew`, `cask` | `jq`, `owner/tap/tool`, `php@8.5` | `brew list`, `brew leaves` | `brew install`, `brew uninstall` |
| `mas` | `xcode@497799835`: the name, then the id | `mas list`, which reads Spotlight's index; with a declared app missing from it, an app in the Applications folders carrying the App Store's receipt that it doesn't list yet, its id looked up by its bundle id (`mas lookup --bundle`), once, what the store said kept in kit's state (that it hasn't the app, for a day); a wait for an app and `kit mas add|remove` go by the same | `mas install` (an app at a time, the first the check that someone's signed in), `mas uninstall`, through sudo |
| `npm` | `typescript@5` (a version optional) | `npm ls --global` | `npm install --global`, `npm uninstall --global` |
| `composer` | `laravel/valet:^4.0` (a constraint optional) | `composer global show --direct` | `composer global require`, `composer global remove` |
| `go` | `golang.org/x/tools/cmd/goimports` (`@version` optional) | the programs in Go's bin folder, by the package each was built from | `go install`; removing deletes the program |
| `gh` | `owner/gh-extension` | `gh extension list` | `gh extension install`, `gh extension remove` |
| `tmux` | `set -g @plugin` lines in tmux's config | the folders in TPM's plugin folder | cloned as TPM does; removing deletes the folder |
| `login-item` | the app's bundle id, `com.example.app` | System Events' login items | System Events (JavaScript for Automation) |
| `claude-mcp` | a name, then `claude mcp add`'s options; a project's in `[claude mcp <folder>]` | `~/.claude.json`, read directly | `claude mcp add-json`, `claude mcp remove` (a project's in local scope, in its folder) |
| `claude-skill` | `[claude skills]`: agntc's key for an install, a GitHub repo and what in it, `github/gh-stack/gh-stack` (the skill of that name; a path, the pack there; the repo alone when it's one skill or pack), `@version` or a range optional | agntc's record, `~/.agntc/manifest.json`, read directly (`agntc list` asks every repo what's newer) | `agntc add -g` (`--all` for a repo alone, `--skill` for a name, else `--pack`), `agntc remove -g -y`; `--json` for agntc's own words when one fails |
| `git-config` | `[git config]`: a key, then its value as one word, as `git config --global` takes them (`alias.st "status -sb"`) | `git config --global --list` | `git config --global --replace-all`, `--unset-all` |
| `power` | `[power settings]`: pmset's words, the source then the setting and value (`-c sleep 0`; `-a` every source, `-b` the battery, `-u` a UPS), named `charger:sleep` | `pmset -g custom`, by source | `sudo pmset`; undeclaring leaves the value |
| `backup-exclusion` | `[backup exclusions]`: a path a line, `~` and `*` globs, expanded on every check (a plain path stands for itself, there or not) | what the backup tools the Mac lists in `[backups]` keep out, each through its driver (a path declared kept out while every one keeps it out): Time Machine's fixed-path exclusions (`SkipPaths`), and each glob match excluded where it is (`tmutil isexcluded`); Arq keeps none of its own, following Time Machine's list ("Skip items excluded by Time Machine rules"), so Arq alone has Time Machine's written, no backup disk needed; with no tool listed the check fails, saying so | through each tool's driver, Time Machine's: a plain path `sudo tmutil addexclusion -p` (Full Disk Access for the terminal); a glob's match `tmutil addexclusion`, no password, so `kit nightly` does it unattended; `removeexclusion -p` |
| `spotlight-exclusion` | `[spotlight exclusions]`: a folder a line | Spotlight asked for files in each folder (`mdfind -count`); its own list is root's alone, so nothing extra is found | added to Spotlight's privacy list through sudo, taking effect after a restart (kit remembers, and says so); removing is System Settings' |
| `secret` | `[secrets <item>]`, a section for each item a file keeps secrets in, in any store kit has a driver for, as in `[secrets op://vault/item]` (the header says where they are; several spread them across items and stores), `--as <name>` after the item naming the block, so commands can say `--in <name>` (a name stands for one item, in the files a Mac reads): what it fills (a variable's name, or a file's path) then the field or attachment, short (`GitHub/token`); a plain `[secrets]` for references in full (`op://vault/item/field`); a name once a file. `--mode 644` for a file's. GitHub Actions secrets aren't kit's: they're a repository's, not the Mac's | `~/.secrets.zsh`'s exports, the files with their modes: never the stores, which may ask the person each time (1Password for a fingerprint each hour) | a sync: each value read through its reference's store driver, picked by the reference's scheme (`op://` is 1Password's), a store's values in one read where its driver does that, as a store may have the person approve each, each store waiting for its own sign-in (one signed out holds back its own secrets alone); should that fail, the values read in halves, down to each alone. 1Password's driver reads many in one run of op (`op inject`, the references on its input, its output kept from the log), each run in a session of its own, and one alone with `op read`; `~/.secrets.zsh` written whole with each value single-quoted (one that fails to read keeps its line), files written beside and renamed in; `kit secret sync` forces one. Removing takes a secret off the Mac, its value kept in its store or deleted from it (`--keep-value`, `--delete-value`, asked at a terminal) |
| `defaults` | `[macos settings]`: `defaults write`'s words (`com.apple.dock tilesize -int 60`; `-currentHost` first for this host's; `-dict-add` and an entry's XML; or a value as XML), named `domain:key[:entry]` | `defaults export` of each domain | `defaults write` (through sudo for a domain under `/Library`), then the app that reads it restarted or keyboard shortcuts reloaded; `defaults delete` |

- **Matching:** a kind may match on part of a name: an App Store app on its id (so an app the
  store renames still matches), npm and Composer packages without their versions, GitHub
  repositories and bundle ids without regard to case.
- **Changed on purpose:** a setting set otherwise than declared (git's) was changed on the
  Mac, so it's *diverged*: applying leaves it, and `kit reconcile` adopts it (the declared
  line takes the Mac's value) or reverts it (the declared value set again).
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
- **A kind's program:** while it isn't installed, a kind in the run is deferred, saying what
  it needs, or, with nothing declared, fine.
  Where a kind's command comes from isn't kit's to know: while kit applies (not `kit
  status`, not the nightly run), a kind whose command isn't on kit's PATH yet (npm's `npm`,
  each secrets store's, as 1Password's `op`, MCP's `claude`; none for tmux's plugins, read
  from its config and cloned with git) waits for it before its check, saying so, looking
  again as installs end, till it's there or nothing still at work in the run could bring it,
  then stands deferred, saying it isn't there yet. The config's waits order the rest (the
  secrets after the sign-in, `--before secret`; npm after mise, `--before npm`). Two orders stay, being kit's
  own data: git's settings after the secrets they sign with, login items after the casks
  and App Store apps they open.
- **A list's lines held back:** a list's section can say, after its header, what its lines
  wait for, each as often as wanted: `--after` a step, or one of a step's things
  (`cask:firefox`), order alone; `--secret` a secret a secrets section declares (one it
  doesn't is a config error, failing the kind's step), which they need, and so wait for, as
  in `[homebrew formulae --secret NAME]`. Applying installs the kind's other lines at once,
  then each such section's as one command once what it waits for is in, the step saying
  meanwhile what it's waiting for; a secret that's a variable is set for that command alone,
  from `~/.secrets.zsh` as kit wrote it, never the environment kit inherited, and its value
  is hidden in whatever's said of it. A wait ends when the thing's in, when its step has
  finished without it, or when the run's stopped: without it, the section's lines stay
  missing, saying what isn't there. Otherwise they're the list's like any other: checked,
  listed, sorted, removed from whichever section holds them, a section going once empty
  (but a kind's last, as above).
- **What's waited for goes first:** a thing a step names (`--after cask:firefox`) is
  installed ahead of the rest of its list, as one command, then the rest as one; where kinds
  share a tool's lock (Homebrew's formulae and casks), neither's rest begins till the other's
  waited-for things are in, or their step has finished. A wait looks again whenever an
  install ends or counts a thing done, and otherwise every 30 seconds.
- **A kind declared in a file of its own** (tmux's plugins, in tmux's config, where the
  plugin manager reads them): kit reads it and edits it in place, through a link to the
  file it leads to. `kit tmux add` and adopting add a plugin's line after the last one (before
  the line that starts the plugin manager, for the first); `kit tmux remove` and undeclaring
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
  `claude mcp add`, then declared with `kit claude-mcp add` or reconcile's adopt;
  `kit claude-mcp on|off` declares one on or off and installs or removes it.
- **Finding what's meant:** `kit mas add` finds an app from its name or id (several matches
  are a choice at a terminal, listed without one); `kit login-item add` takes an app's name, path
  or bundle id. Declaring a login item writes the app's name as the note.

**Features.** Each piece of kit stands alone, and is on when it's declared: a list by having
entries (the kinds, later the folders to restore), a piece with nothing to list by a switch
in a Mac's `[features]` section (`prefs`, `touch-id-sudo`, `remote-login`,
`file-sharing`), so
turning one off loses nothing that isn't its own. Built-ins are generic and lasting; what's
specific to a user, or to the moment, goes in their config: checks of their own in
`[checks]`, jobs of their own in `[hourly]` and `[nightly]`. `kit feature
on|off <name>` (`--shared` for every Mac) changes one. A features section can say what its
features wait for, `--after` alone, as a list's can: under `[features --after cask:app]`, as
kit applies, the features' steps wait till the app's cask is in (installed first, as anything
a step names is) or the casks' step has finished; a section placed after what isn't a step
fails its features' steps, saying so. kit is built for its author's Macs: built-in support is
for the tools they use, and the switches exist because those Macs differ.

**Backups.** A Mac's `[backups]` section names the backup tools it uses, a line each: the
tool, then `--after` and `--before` as on any line (`arq --after cask:arq`: as kit applies,
Arq's check waits till its cask is in, rather than reading "not installed" while the cask
waits its turn; one placed after or before what isn't a step fails its check, saying so). A
Mac with no `[backups]` has no backup steps; the tools' steps run in kit's order (Time
Machine, then Arq), whatever the files' order. `kit backup add <tool>` (`--after`, `--before`,
`--shared`, `--note`) declares one (with `--shared`, for every Mac, taking it out of each Mac's
own file), refusing one declared already, in the shared file or the one it goes in, or placed
after what isn't a step, and `kit backup remove <tool>` (`--shared`) takes it out, each committed and pushed. Each tool is a driver behind one contract
(`internal/backup/tool`), a folder each (`internal/backup/timemachine`, `.../arq`), listed in
`backup.Tools`, so adding one is its folder and a line there: a tool kit doesn't know is a
config error naming those it has, and `time-machine` or `arq` in `[features]`, where they were
before, is refused, saying they go in `[backups]` and the `kit backup add` that puts them there
(`kit feature on|off` of one names `kit backup add|remove`). The driver says
everything that's the tool's: its name; whether it's set up, and, when it isn't, setting it
up as a to-do (its title, why, its steps, what kit watches for, and in a line for `kit
status`); when its backups last finished, as plans when it has several, and whether one is
running; what's wrong, in its words, each with a key and what to do; its summary when its
words differ from kit's; and what it does with `[backup exclusions]`, keeping them as its own
(Time Machine) or following another tool's list (Arq). kit's shared code makes the step: a
tool not set up is the person's to-do, in Backups; its problems are items keyed by the tool
(`time-machine:stale`); otherwise kit says how it stands from the backups ("last backup
11:20", plans named and sorted, or "first backup under way" or "soon" before any has finished)
and its glance ("Time Machine 11:20"). Time Machine: a backup disk, automatic backups on, the
last backup within 3 hours (the disk away said so) and none stuck in a phase over 3 hours; a
disk just added, its first backup under way or none tried yet with the disk there, is fine
for now. Arq, through `arqc stats`: the plans it runs on a schedule (one marked "Manual" isn't
expected to run), one at least, each backed up within 26 hours; not installed, or its status
unreadable, says so; its licence agreement not yet accepted, or no plan made, is not set up.

**Checks.** Steps with a check and no apply, each problem an item with a stable id saying
what's wrong and what to do: the Mac's (`disk`, `memory`, `file-events`, `load`), always;
the backup tools' a Mac lists in `[backups]` (`time-machine`, `arq`: each runs on its own
schedule, and kit checks it did, only the plans Arq is scheduled to run; Backups above); the config repository's
(`config-sync`: commits that won't push; `config-private`); the scheduled runs' (`nightly`: the hourly run within two
hours, the nightly one finished within 26 and none stuck over six, each job's last outcome;
quiet until `kit nightly` has run on a schedule); with its feature, `prefs` (apps' settings saved: capture switched on, this Mac owning its folder, a capture
in the last day without errors, pushed within a day; read from kit's record alone) with
`full-disk-access` (what capture needs); and the user's own, in a `[checks]`
section, a line each:
a name, then `--` and a command, which exits 0 when all's well, or prints what's wrong on
its first line (a minute's timeout), each failing one an item of the `checks` step. Every step has an area (Backups, Jobs, Mac, Drift,
Config, Checks), which the status document carries. What only the person at the Mac can do
about a check is a to-do, an item with a title, why, steps, what kit watches for, a group and,
for a choice, its keys, raised at the end of applying (Steps by hand): bringing back apps'
settings (`prefs`, paused), a Time Machine disk to add, Arq's backup plan to set up; titled the
same wherever the item shows. The rest (disk space, a stale backup, commits that won't push)
are how the Mac stands, not to-dos. Full Disk Access isn't one: a grant takes effect only once
the terminal restarts, so kit couldn't watch for it.

**Adding a secret:** `kit secret add <name>` takes its value typed (unshown), on standard
input (`--stdin`, how an agent passes one, never on a command line), from a file (`--from`,
kept as an attachment), or as an existing reference (`--ref`, nothing stored: its line goes in
the file's block for its item, short, when the file has one, else in the plain `[secrets]`, in
full). A new value goes where it's told, never by default: `--in <name>`, a block by the name
its header gives it (`--as`), or `--item` and an item, in full; with neither it's refused,
listing the blocks, as `kit secret places` does (each block's name, store, item, file and how
many it holds). `--field` says where in the item, in its store's words (1Password's
`<section>/<field>`). The line goes in the file's block for that item, which kit makes when
there's none, going by the name `--in` gave. Everything's checked before the value's asked
for: where it goes, the line as the config would read with it, the store answering, and
nothing kept there already. The value's kept through its store's driver (1Password's edits the
item with its JSON on op's standard input), then read back and compared, declared, and synced,
never shown. `--mode` as on the line. `--shared` declares it in the shared file, in the block
`--in` or `--item` names, or, with `--ref`, as above (the shared file having no block for its
item, in one made for the item of the Mac's block holding it, going by that block's name), and
takes it out of each Mac's own file, whatever block it was in, by the rule for moving a line
(below): a Mac's line keeping it elsewhere, or of another mode, is refused before
anything's stored, saying the `--ref` and `--mode` that share the value it has; one the shared
file has already is refused. `kit secret remove <name>` takes it off the Mac, undeclares it, and
keeps or deletes its value in its store (`--keep-value`, `--delete-value`; asked at a terminal);
a value given in full, outside the items' blocks, is left where it is.

**Steps by hand** go in a `[manual]` section, a line each: a name, what to do in quotes, then
`--after` and `--before` (as on any line), `--group <name>` (the group it's in, one word or
quoted, at most once), and `--test` and a command saying whether it's done (exit 0: done), the
rest of the line, as in `tool "Install the tool from its site" --test test -d
/Applications/Tool.app`. The comment lines directly above it, no blank line between, are its
notes: why it's there, one paragraph. The lines indented under it are its steps, one each, in
order, which kit keeps with it, notes and all; a step ending with a colon heads the steps after
it, as in `If it doesn't ask, in System Settings:`, shown muted and unnumbered, the numbering
carrying on across it. kit opens nothing for a step by hand: its steps say what to open, and the
person does it (`--open` is refused, saying so). One with no command is done once marked on this
Mac (`kit manual done <name>`, `--undo` to unmark). Those not done are items of their step, in
the Manual area, saying what to do, so `kit status` shows them; what kit can check itself stays
a check that says what to do. While `kit apply` runs at a terminal, one that comes due is raised
as a card on its screen, front and centre: its title, why it's there, said once (its notes, or,
for a line with none, kit's words for why the run waits for it), its steps numbered, and a
notification while it's up (taken away while it's set aside, posted again as it comes back). `space` says it's done (`enter`
too, unshown: marked as `kit manual done` marks it; one with a command is looked at at once, as
it is every few seconds anyway); a card with a command offers no key, kit carrying on by itself
once it's done. There's no putting a card off: it's done, or the run's stopped. One card is up
at a time, the rest waiting their turn, in order of how much of the run waits on each (the steps
that need it, come after it or wait for it, and those waiting on them in turn), as many in the
order they came: one coming due that more waits on goes up in place of the card up, which waits
its turn again, as a password manager's sign-in, which the secrets and everything after them
wait for, comes before the App Store's. While another waits, `tab` moves on to it, the card up
staying to do and coming back after (the card says so, and tab then chooses no row); the
to-dos at the run's end are never moved on from. A card waiting its turn is still looked at
every few seconds: done meanwhile, it's done, the log says so, and what it held up carries
on at once. A line with `--before` is a step of its own, raised mid-run once what it comes
after is in, as the steps it comes before wait for it; the rest, `--after` alone only ordering
them, come up at the run's end, after every other step, with the to-dos kit's own checks left
(Checks). That end is a list worked through one to-do at a time, in kit's order, with no choosing
and nothing skipped: kit's own that come first (bringing back apps' settings, so the apps opened
next start with them), then the lines not done, in order, then kit's own the rest (the backups');
grouped, each group where its first comes (kit's own in Apps and Backups). Each is looked at
while it's up, a line by its command every few seconds, one of kit's own by its step's check, and
once done the log says so, how long it took and what kit saw, and the next comes up; `space` says
one with no command is done; a choice is answered by its keys, none offered while kit does what
the one pressed says, its words turning in their place, and keys pressed meanwhile dropped
(apps' settings, whose why says first that bringing them back opens some apps once, and, when
the config has `[prefs files]`, that settings files come back too: `r` brings them back from
this Mac's last save, its date said, as `kit prefs restore` does, saying how many came back, how
many wait for their apps, how many apps to open once (a to-do that comes next, kit's own done
leading to those its check finds then) and how many are held for `kit prefs restore --pending`,
the log naming each of those with why; a watched to-do of kit's own, found done, finishing what
it waited for, as bringing those apps' settings back; `f` starts afresh, as `kit prefs
start-fresh` does, saying saving's on and how many still wait to come back). Stopping there
(`q`'s Stop, between two to-dos as well, as the one up is found done, and while kit does what a
key says, taken even with another key waiting unread) leaves what's left to do, the one up done
if it was, with the to-dos it left, in that order in the record, the run ending as it stands. After the last, kit checks again the steps the
to-dos held, those they came from, so their rows and the record are right, then finishes.
Without a terminal, nothing's raised: kit's own stay items of their steps. A step a line comes
before can find, as it applies, that the line wasn't done after all, as the App Store's first
install finds no one signed in (signing in has no `--test`: mas can't say who's signed in, and
macOS keeps it private): the line, marked done no more, comes up again as it did, card and
notification, the step waiting for it meanwhile, its row saying it waits for you, and the log
that it's waiting for you, then, once it's done, done and how long it took; then the step tries
again, as often as it finds it not done. So kit installs App Store apps one at a time, the
first the check: mas, with no one signed in, says so or waits for good, saying nothing at all,
so till an app's in, mas saying nothing for five minutes is taken as no one signed in, as one
mas says it of is. Once mas has said anything, as that it's downloading, the install takes as
long as it takes (it draws its progress only at a terminal, so through kit it says nothing more
till it's done), up to an hour and a half (Xcode is gigabytes); one failing holds none of the
others up. What's left of an install kit ended, in its process group, is stopped before the
next try: mas runs as root, which only root can stop. While someone's at the terminal, no app
fails for want of a sign-in. A line whose test
still passes has nothing to raise again; without a terminal nothing is raised, and the install
fails at once, naming the fix (sign in to the App Store, then try again), with what failed
before it; an install that fails otherwise says what mas said, or that mas didn't finish, never
mas's command line, which the run's log has. `kit check|hourly|nightly add
<name> -- <command>` and `kit manual add <name> "<what to do>" [-- <command>]` (written as
`--test`; `--group` as on the line) declare checks, jobs and steps by hand (a check, and a
step's command, run once as it's added, to say how it stands); `kit remove
check|hourly|nightly|manual <name>` undeclares one.

**Steps of the user's own** are set-up that's done or not, too particular to be kit's (a tool
installed by its maker's script, a certificate authority trusted, a key imported). Each is a
line in a `[steps]` section, in the form of the command that adds it: a name, what it does in
quotes, then `--after <step>` (applied after that step, if it's there, or after one of a step's
things, as in `cask:firefox`), `--before <step>`, `--secret <name>` (a secret a secrets
section declares: applied once it's in place, its script run with it set when it's a variable,
its value hidden in what the script says), each as often as wanted, and `--sudo` (applying needs
an administrator's password, asked for as the run starts, as for installs), as in `tool "Installs the
tool, from its maker" --after brew`. Its folder, `<scope>/steps/<name>/` beside its declarations
file, holds `run`, an executable, and whatever data it uses; which Macs it's for is the folder
it's in. kit runs `run check` and `run apply` in that folder, in its own environment, with
nothing on their input. check: exit 0 says it's done, its first line what kit shows; 1 that it
isn't, so it's an item to run; anything else, or a minute passing, that the check couldn't tell.
apply, given 15 minutes, runs only when the check says not done, and is believed only when the
check then passes; when it fails, the last line it printed (standard error first) says why.
Everything both print is in the run's log. Each is a step of its own in the Steps area, its name
its title; `kit status` says how each stands, and what isn't done says `kit apply`. A line that
doesn't read, a `--before` naming no step here, an `--after` naming neither a step nor a kind,
or a `--secret` no secrets section declares, makes its step fail, saying where; a name one of
kit's steps has is refused. `kit step add <name> "<what it does>"` declares one and writes its
`run` from a template that follows the contract (its check says "not written yet" until it's
written); `kit step remove <name>` takes out the line and the folder. kit never undoes what a
step did.

**The config repository's own edits** are drift, not a problem: linked files mean edits land in
the repository outside kit (through a link, by hand, by an app), and kit commits only its own
changes: the paths it changed, each whole as it is on disk (a rename's two sides, a deletion, a
new folder's every file, a file become a folder), whatever the user's git config says,
committed from an index of the last commit's and those paths alone, so nothing else staged goes
in or is touched. As `git commit` does, it holds git's index (`index.lock`, taken only when no
other git holds it, else tried again for a moment, then refused, saying how old the lock is
and, should no git be running, to remove it) from before it reads the last commit until the
user's index holds the files it committed as committed, written through a copy (keeping the
index's time, for git's racy-entry rule, and its mode) and put in place as git does; kit's own
index files go only while it holds the lock. With nothing committed, the user's index is left
as it is. kit's commits are its own, made with git's plumbing: no commit hooks run
(`pre-commit`, `commit-msg`, `post-commit` and the rest); the two git runs on any write of an
index or a ref do, as for any git command (`post-index-change`, given kit's own index, and
`reference-transaction`, which can refuse HEAD's move: then nothing's committed, git's error
said). The commit is of its index's tree onto the last commit, signed when git's config would
have `git commit` sign it (`commit.gpgsign`, which `git commit-tree` doesn't read itself, so
kit passes `-S`, signing with the program and key the config names, as `git commit` does), its
author and committer git's own, its message as `git commit -m` leaves it (each line's trailing
whitespace and blank lines first, last or doubled gone). The user's index's next contents are
made before HEAD moves, written into the lock, so once HEAD has moved all that's left is the
lock taking the index's place: a run that stops between leaves the lock, never an index from
which a `git commit` would undo kit's, and should the lock not take its place, kit names the
`git reset -q -- <files>` that brings the index up to date. A lock so left, the next commit
puts in the index's place, as the stopped run would have, and says so ("finished the last run's
index"), once the lock has been left alone for as long as kit waits for git's index and facts
say it's kit's: HEAD's last move kit's, and that commit's files as committed in the lock but
not in the user's index, which tells it from a lock a git took since, as `git commit -a`'s
while its editor's open; any other lock it names, with what to do should no git be running.
HEAD moves onto the commit only
from the last commit, so when another git moved HEAD meanwhile, git refuses, and kit, still
holding the index, makes the commit again onto where HEAD is now, a few times at most; then
nothing's committed, HEAD and the user's index stay as they were, and kit says so, and that
`kit reconcile` commits it. A branch's lock another git holds is named, as the index's is. It
refuses while git is part way through a merge, rebase, cherry-pick, revert or `git am`, or a
conflict in its paths waits to be resolved: its change waits till that's finished. Nothing's
pushed after a commit that failed. Once git has made the commit, HEAD's move and the index
catching up are done whatever becomes of the run. What git can't keep (an ignored file, an
empty folder, a repository nested in kit-config, which `kit reconcile` names and only snoozes)
is left out. With nothing to commit it says nothing changed, pushing only commits still waiting
for the upstream, as when a push failed. Drift is kit-config's files on disk against the last
commit: the `config` step makes each file whose working copy differs from it an item
(`config:shared/home/.zshrc`, edited, added, as one to be added with `git add -N` is, or
deleted, with the lines it adds and removes; untracked, a file taken out of git's index and
kept on this Mac as git ignores it, changed or not, as committing it deletes it from the other
Macs; a repository, by its folder, `config:vendor/tool/`), quiet for a day as drift is; any
other difference only in git's index, the file on disk as last committed, is the user's own
business in git, never drift. `kit reconcile` shows an edit's diff and offers adopt (commit it,
then push, `--note` the message's end; an untracked file is committed as deleted, kept on this
Mac), revert (back to the last commit; a new file to the Bin; an untracked file tracked again,
as it is) or snooze. kit pulls before it pushes, and pulls the prefs store's clone the same way
(`kit prefs`): it refuses while git is part way through one of its operations, or a conflict
waits, as setting work aside would end the one; then it fetches, pruning the remote's branches
gone from it (a remote out of reach said plainly). An upstream not there on a remote with no
branches yet, as a new, empty one's, has nothing to bring in, and the branch is pushed to it;
one gone from a remote that has others, as when it's renamed or deleted there, is refused,
naming it, as pushing would make it anew. A branch with no upstream follows the remote's branch
of its name when there is one, made its upstream and caught up with; otherwise it's pushed to
the remote it's pushed to (`branch.<name>.pushRemote`, then `remote.pushDefault`, then
`origin`, or the only remote), which becomes its upstream, and with several remotes and none of
those set, kit refuses. HEAD on no branch is refused. Commits waiting to be pushed are those
the upstream hasn't, or, with it not there or none, those on none of the remote's branches, so
a failed first push, or commits made offline, show as waiting. When the remote has commits this
Mac hasn't, kit rebases onto them by the commits since the two parted (never git's fork-point,
which can leave out a commit, so the rebase changes no more than the remote's commits do), so
two Macs don't fight. Anything not committed is set aside in a stash of kit's own and put back
as it was, staged or not; but when the remote's commits change what's not committed here,
nothing is set aside, rebased or pushed, and kit names the files and what to do: a file changed
here (`kit reconcile` commits or undoes it); a change only staged, its file on disk as last
committed, which drift doesn't show (the user's to commit or unstage); a file git ignores here,
which the rebase would replace without a word (move it aside), and, of a folder git ignores
whole, only what the remote's would replace (a file at its path, a file where it needs a
folder, or the folder itself, where it has a file), as git adds to such a folder harmlessly;
counting a folder of the remote's where a file is here, or the other way round, and, where git
folds case (`core.ignorecase`, as on macOS's own disks), names differing only by the case of
their ASCII letters, as git folds them. Each file is named once, a file both changed and
ignored as changed. `kit status`'s Sync line says so too, as far as the last fetch tells, while
it holds commits back. A stash that won't go back is kept, named, never dropped, kit saying
whether the work is wholly in it or back with git's conflict markers in which files; a rebase
judged under way by git's own record of it is aborted, one that never started leaves nothing to
undo, and nothing is pushed. What a program says, kit looks for on the line that carries its
error (git's `fatal:`, or ssh's own lines above it, some with no prefix of ssh's; mas's
`Error:` or `[ERROR]`; brew's `Error:`; osascript's `execution error:`; sudo's own), as the
reason is often above the last line, and a line that merely mentions it isn't the error; git
runs in the C locale for it, as git translates `fatal:`.

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

**The engine** orders steps so each comes after what it needs (a cycle, or a need that isn't a
step, is refused when the pipeline is built), and starts each, in that order, once what it needs
is done and one of its jobs (4) is free: independent checks run side by side, and with one job
they run in order. A step waiting for one of another's things, before it's checked or partway
through applying, gives its job up meanwhile, and the steps that thing's step comes after or
waits for take it as done, so none waits on it while it waits on them; one of them needing it is
refused when the pipeline is built. A step waiting for a thing no step in particular puts in
place (a command) waits till it's there, or till the run's quiet: no step at work, none able
to start, none about to look again at what it waits for; then each such wait ends together.
Applying a step that doesn't stand ok runs its apply, then its check again; a step marked to be
always applied (the steps by hand at the run's end, which raise the rest's to-dos too) is applied
however it stands. What a person must do by hand shows as an item while the step needs
attention. A running step can work on the run's others: see those that have finished, as they
stand; look at one, its check run as that step's own, the run's record of it left; check one
again, what it finds taking its place in the report and emitted as the step finishing again, for
the faces and the log, with what applying it did earlier kept; and use one's choice, the keys of
the to-dos its check finds, run as that step's own; and say what it comes after wasn't done after
all: each of those steps that can be done again (a step by hand of its own, its card raised again)
is, in order, as its own, then checked again as above, the step saying meanwhile that it waits for
it, and carrying on once they stand ok. A run that's stopped (an interrupt) fails the steps it
hadn't started.

## Commands

Built:

```
kit                          At a terminal, kit's menu, at once: what to run next (else help)

This Mac:
kit status [step...]         How this Mac stands against the config: what needs attention
kit apply [step...] [--plan] Install what's declared and missing (--plan: say what, do nothing)
kit reconcile [<kind>...] [<id>...]  Settle drift: adopt, remove, install, undeclare or snooze
kit nightly [job...]         Run the scheduled jobs due, then every check (the hourly launch)   [--plan]
kit list [kind]              What's declared for this Mac: file, note, installed or not
kit why <name>               Where it's declared, whether it's installed, what needs it
kit log                      What the last run did: every check, every command, with timings
kit machine [<name>]         This Mac's name: shown, or set (one of kit.toml's Macs)

What kit manages, a command each:
kit <kind> add <name>...     Install, and declare for this Mac   [--shared] [--temp] [--note]
kit <kind> remove <name>...  Uninstall, and undeclare            [--shared]
    (brew, cask, mas, npm, composer, go, gh, tmux, claude-mcp, claude-skill, login-item,
     defaults, power, git-config, backup-exclusion, spotlight-exclusion)
kit claude-mcp on|off <name>...  Declare Claude's MCP servers on or off, and install or remove them
kit feature on|off <name>... Switch features on or off for this Mac   [--shared]
kit backup add|remove <tool> The backup tools this Mac uses, each checked   [--after] [--before] [--shared]
kit file add|remove <path>...    Files linked into the home folder from kit-config
kit path add|remove <dir>...     Directories on the PATH
kit secret add <name>        Keep a secret in its store, declare it, sync it   [--stdin] [--from] [--ref] [--in] [--item] [--field] [--mode] [--shared]
kit secret remove <name>...  Take it off the Mac, undeclare it     [--keep-value] [--delete-value] [--shared]
kit secret sync              Read every secret from its store, and put each in place
kit secret places            The blocks secrets are declared in: name, store, item, file, count
kit prefs capture|restore|history|pending|start-fresh   Apps' settings, saved to a private repository and restored
kit check|hourly add <name> -- <command>, kit nightly add <name> -- <command>   Your own checks and jobs
kit manual add <name> "<what to do>" [-- <command>]; kit manual done <name>...  Steps by hand   [--group]
kit step add <name> "<what it does>"; kit step remove <name>...  Steps of your own   [--after] [--before] [--secret] [--sudo] [--shared]
kit version                  As --version
```

The command shape (Lee, 6 Oct): a command about one kind of thing names it first, then what to
do, as in `kit brew add`, `kit secret sync`, `kit manual done`; one about the whole Mac is a verb
alone (`status`, `apply`, `reconcile`, `nightly`, `list`, `why`, `log`, `machine`), a kind named
after it a filter. Each thing's help shows only its own flags and verbs. The help lists the
commands in groups: This Mac, Packages, Settings, Files and secrets, Your own. `kit nightly`
runs jobs by name, so no nightly job is named `add` or `remove`. Kinds are named as words read
after `kit`: `git-config` (not `git`, read as git's own add), `spotlight-exclusion` and
`backup-exclusion` (not `spotlight`, read as adding to it), `login-item`, `defaults` (the lines
are `defaults write`'s words), `mas` (casks are apps too; package kinds are named after their
tool).

`kit apply` checks every step and applies each that doesn't stand ok, or has something to
do, then checks it again: each step then says what it did ("installed: jq"). It never removes
or adopts: reconcile does those. At a terminal, when a missing cask installs through a
package, kit asks for an administrator's password once, through `sudo -v`, as the run starts,
under its heading, which holds still till it's answered. The run goes on meanwhile, and only
what needs the password waits for it, its row saying so, however long the answer takes: kit
gives the question no time limit, and an answer that came after sudo's own five minutes ended
it is given to sudo again. What a kind is done with for now, tried, failed included, or put off
for the answer, nothing waits behind for order's sake, as the formulae otherwise wait behind
what the casks install first: once the casks step has put a cask off, a cask from a tap it's
just added as much as any, only what waits for that cask itself waits for the answer. Once it's given, kit keeps sudo's hold fresh while it runs,
giving the password again if sudo forgets it (the person asked only if sudo refuses the one
kept), what needs it waiting meanwhile, an App Store app at a time; an App Store install sudo
says has forgotten it, as after the Mac slept, has kit find out at once, ask again if it must,
and try the app once more. Not given, what needs it is left, saying why. kit keeps a password
once sudo has taken it, never before. Without a terminal it asks nothing, and those casks wait,
saying why (requirement 4). App Store apps need the password for every install, as mas runs as
root; `kit <kind> add`, `remove` and reconcile's installs ask up front, before anything else, and
wait for it the same way should sudo forget it.
Nothing kit runs asks at its terminal: each command runs in a session of its own, without it, so
a program that asks there fails at once, never stopped unseen in the background; kit's own sudo,
which never asks there (`-n`, or `-S` on its input), stays in kit's session, as sudo holds a
password only for the session it was given in, and is ended through sudo, which passes SIGTERM
on to what it runs as root (its process group killed only once that's been ignored for five
seconds). A program's own sudo asks kit instead (`SUDO_ASKPASS`, and `DISPLAY` set, without
which sudo, having no terminal, won't), and is given the password kept, so a step declared
`--sudo` uses plain `sudo`; `sudo -n` never asks, so goes without. The person's asked one thing
at a time: a program's sudo waits for kit's own question, the run's time limits waiting with it.

`kit <kind> add` installs each name, unless it's installed, and declares it in this Mac's file, in
its sorted place, or for every Mac with `--shared` (taking it out of each Mac's own file); it
asks nothing about where. `--temp` installs without declaring: quiet for 7 days, then reconcile
asks.
`kit <kind> remove` uninstalls and takes each name out of this Mac's file; one declared for every Mac
needs `--shared`. Both handle each name on its own (one failing leaves the others), then commit
the config's changed files, with a message saying what, for which Mac and why, and push to
main, pulling with rebase first. A push that fails leaves the commit, and says so.

**Moving a line.** Every command declaring for every Mac takes the name out of each Mac's own
file in the same edit, losing nothing the lines it moves say, all judged before anything's
stored, installed or asked for:
- What the command can say must be the same, by meaning, as each kind reads its lines: options
  in any order and once each, a macOS setting's value by its type (`-integer 60` is `-int 60`,
  `-bool YES` is `-bool true`) and whether it's a dict's entry, an MCP server in JSON or as
  options, a secret's full reference (short in its block or in full) and its mode. A reading
  only evens out how a line is written, never what it does. Otherwise it's refused, naming the line and
  what differs: give the same, or, for a value kit reads from the Mac (macOS, power and git
  settings, MCP servers), set it the same here, or take that line out first; for a secret,
  with the `--ref` and `--mode` sharing its value.
- What no command can say is carried, the same in every line moved: its section's hold
  (`[homebrew formulae --after cask:x]`), and a step by hand's steps, `--after` and `--before`.
- Their note, unless the command gives one, and the comments above them, compared by their
  words (one set apart from the line by a blank line is the line's, as kit's sorting has it),
  are carried, one between them; a note given stays after the line, the comments above it. A
  note kit makes up, as a login item's app name, is the note only when nothing else gives one.
- So is the name of an item's block (`--as`), for a block the shared file hasn't, unless a
  file gives that name to another item: then the block goes unnamed, kit saying why.
- A step of your own's folder moves with its line: the Macs' folders, all the same, go to the
  shared one, or are left for one there already that's the same.

Lines moved that differ in what's carried are refused, naming each. A name in the shared file
already is refused as before. `kit <kind> add` judges each name before anything's installed
or asked for: one refused fails alone, in its row, and the rest carry on.

`kit reconcile` settles drift. At a terminal it goes through each item that needs attention
(`--all`: quiet ones too; name kinds, as in `kit reconcile brew`, for theirs alone), asking
what to do with it: declare it (for this Mac, or every Mac),
uninstall it, install it, undeclare it, snooze it for 7 days, leave it, or stop. Every question
comes first; then it does it all, as `kit <kind> add` and
`kit <kind> remove` would, and commits and pushes once. Without a terminal, or with `--json`, it lists
the items with their ids and choices, and `kit reconcile <id>... --adopt` (or `--remove`,
`--install`, `--undeclare`, `--snooze`; `--shared`, `--note`) settles those, every
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
At a terminal, in kit's look: the wordmark with the Mac and the time beside it, then a row an
area, the drift and the config repository one area, Config.

`kit nightly` is what the hourly launch runs: the jobs due, in order (one failing never stops
the others), then every check. It installs only what a kind lets it install with no one
there, cheap and safe, and removes nothing: today, a new folder matching a backup
exclusion's glob, excluded where it is. Besides the user's own jobs, two are built in, with
the `prefs` feature: nightly, last, its capture (`kit prefs capture`, stopped after 15
minutes); and hourly, its retry of the apps' settings waiting to be restored, once
their apps are installed and closed and have their settings folders, opening none. The hourly jobs run every time; the nightly ones once a day,
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

The sections, in the order kit writes them: `features` (the switches), `backups` (the backup
tools, a line each, with `--after` and `--before`), `paths` (kit's PATH, in order, `~` expands; the
shared file's, then the Mac's), `homebrew formulae`, `homebrew casks`, `app store apps`,
`npm packages`, `composer packages`, `go tools`, `github extensions`, `macos login items`,
`claude mcp` (and `claude mcp <folder>`), `steps` (the steps of the user's own), `manual`, `checks`, `hourly`, `nightly`
(jobs of the user's own: a name, then `--` and a command, `~/` expanding in any word). A section kit doesn't know is refused, never
skipped; so is a line before any section, a section twice, or a file named for a Mac
`kit.toml` doesn't know.

Each section's lines read one way. A name list's: one name a line, a version or a
constraint allowed after it (`typescript@5`, `laravel/valet:^4.0`). A path's: the whole line,
spaces and all. A command's: a name, then options, split into words as a shell splits them;
a backup tool's the same, its options optional.
A `#` at the start of a line or after a space (outside quotes, in a command) starts a
comment: the comments directly above an entry, and the one after it on its line, are its
notes, saying why it's there. A name is declared in the shared declarations or a Mac's, never
both. A Mac can't be called `shared`.

The lists of things and settings (every section but the PATH, the commands, the jobs and kit
prefs's lists) are kept sorted, with no groups: packages by their names' last part
(`oven-sh/bun/bun` sorts as `bun`, `php@8.5` as `php`), the rest as written, so settings sort
by their keys (a domain's or `alias.`'s together); case aside, numbers by value. Each entry's
notes move with it; comments opening a section, before a blank line, stay at its top. The PATH
keeps its order, the order it's searched in; commands and jobs keep theirs, a new one going in
its place by name.

kit edits these files in place: a sorted section it touches is written sorted, and every other
line is left as it is; it checks the result reads back, and writes the file whole or not at
all. A file it can't read, it doesn't edit.

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

`kit file add <path>` moves a file, or every file in a folder, into this Mac's home folder
(`--shared`: every Mac's) and links it back. A path git ignores itself (kit-config's rules or
the user's global ones), a file or a folder whole, is refused, naming the rule, as kit-config
could never keep it; files it ignores inside a folder stay where they are, on that Mac alone,
the row naming them. A folder that is a repository, or holds one anywhere in it (a `.git`
folder or file), is refused, naming where. `kit file remove <path>` puts a copy back in place of the link and takes
the file out. Both commit and push.

### kit's PATH, and the shell's

At start-up kit builds its own PATH from the config's `paths` and the system's directories,
ignoring what it inherited, so it behaves the same from a terminal, launchd or an agent, and
finds tools installed partway through a run.

The shell uses the same list, the code review's "one PATH list": the `path` step writes it
(`~` expanded, colon-separated) to `path` in kit's state directory, and the shell's startup
puts the file's contents ahead of the PATH it inherits, reading it without running anything,
so a config that doesn't read never leaves a shell without its PATH. Relative entries
(`node_modules/.bin`) are kept for the shell and left out of kit's own PATH. A `$`, a colon,
or a `~` that doesn't start the home folder is refused. `kit path add <dir>` puts a
directory last (`--shared`: on every Mac), `kit path remove <dir>` takes it off; both write
the file and commit.

Three features change the system, each applied through sudo: `touch-id-sudo` (sudo takes a
fingerprint, by a line in `/etc/pam.d/sudo_local`, which macOS updates leave alone),
`remote-login` (SSH) and `file-sharing` (SMB), each a launchd service on unless launchd lists
it disabled. A step whose apply needs an administrator's password says so (`Admin`), and
`kit apply` asks for the password as the run starts, the step waiting for the answer, and
`kit reconcile` before anything is applied, as for the kinds whose installs need it: asked once
at a terminal; without one, the step waits.

## Output

The core never prints. It emits events: a run starting, with the steps it will run; a step
starting; a check's result, with its items and reason; every external command, with its
arguments (redacted), exit code, duration and output; a step deferred, with the unmet need; a
step failed, with the error; the run finishing, with its summary. Faces subscribe:

- **pretty** at a terminal, in kit's look (`internal/look`, the components every screen is
  drawn from; the rules signed off in the look's design, round 4): colour brought down to what
  the terminal shows, no glyphs that need a particular font, and no line past 80 columns but
  on applying's screen, which takes the window. Commands looking at the whole Mac (`status`,
  `apply`, `nightly`, `reconcile`) open with the wordmark, what ran, the Mac and when stacked
  beside it; their steps show a block an area (Backups, Jobs, Mac, Config, Steps, Manual,
  Checks), a row a step: its mark, its title, then what it says, the parts after dots, what to
  do on the line under it. Config takes the drift and the config repository: what needs
  attention, a row a thing (what's wrong, for how long, what to run), then Packages, Settings,
  Files and Secrets counted (each step's part says which it counts towards) and the config
  repository's row. Applying, in a window of 100 by 30 or more, has a screen of its own (the
  look's round 10): the wordmark, the bar (each step one, a running one counting as far as it
  says it has got) and what the run is doing or waiting for you to do; on the left a row for
  each part of the Mac a person recognises (This Mac, Files and shell, Mac settings, Secrets,
  Homebrew formulae, Homebrew casks, App Store apps, Developer tools, Git, Login items, Your
  steps, Backups), each step in one by its kind or feature, a step by hand in the row it holds
  up, each row saying what its steps are doing, then what's for later (with done's mark, as is
  its line in the log: nothing's to do till the end, where the to-dos take the to-do mark); a
  step at work that's printed nothing for two minutes says since when, and the log is told,
  but never a step waiting its turn, which says only what it waits for, while whatever holds
  the turn is watched (Homebrew's update, an install, an uninstall, a tap); on the right the
  log,
  every line of the run with its time, or a row's lines alone once it's chosen (tab or a
  click; esc for all), scrolled back and followed again, with a step by hand's card above it
  while it's up. In a terminal whose driver says it shows progress on its tab (Ghostty's),
  the run's progress is there too. At the run's end its to-dos take the screen (round 12): the state line counts
  what's left and what's done, then says kit's carrying on once they're over; the left pane lists
  them, To do and how many, each group's name muted, done ones ticked, the one up a pill, `q`
  stopping there at its foot; on the right the one up is a panel ruled off over the log, as a
  card is: its title and where it is in the list, why, its steps, then how it ends (kit watching,
  and what for; `space`; a choice's keys). In a smaller window the run is drawn in place: a light
  a row, the bar, what's running and what's wrong, a card or a to-do up in place under them, no
  list. Once applied, its record takes the screen's place: the wordmark with the bar and the
  tally, then what's left to do (the to-dos at the end first, in their order, each its steps as
  how), what failed, what needs you, and the rows done. Commands aimed at one thing (`add`, `remove`, a step named) are a timeline, a row
  a step as its turn comes, no wordmark. A summary ends a whole-Mac command: what failed,
  needs you or wasn't checked, what's fine, and how long it took. `kit log` shows a run as a
  timeline; kit's help and its errors take the look too (an error is a row, what to run about
  it under it: `→ did you mean kit brew add jq`);
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
installs from stale formulae fail, and runs nothing else through brew till it's done: Homebrew
can update itself then, its Ruby with it, and a brew run beside that fails. What waits for it
says so, and the wait doesn't count against its time limit. Formulae and casks install in a
`brew install` for each tap, the taps' first, then one for Homebrew's own: a tap's formula then
claims a name it shares with one of Homebrew's own before anything pulls that in as a
dependency, and a tap that fails fails only its own names. A missing
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
| `internal/look` | kit's look: the components the pretty face, the help and the errors are drawn from |
| `internal/apps` | The Mac's apps kit looks for, and the terminals `kit.toml` can name to hand a new Mac's boot over to, each with its driver: how it opens a window, and whether it shows a run's progress on its tab |
| `internal/logs` | The run log, its retention, and reading it back for `kit log` |
| `internal/redact` | Hiding secrets in what's logged |
| `internal/runner` | The one place a process starts: kit's PATH, a clean environment, timeouts, output captured, each command reported as an event. `runner/runnertest` is the fake |
| `internal/engine` | Steps, ordering by needs, running checks side by side, deferral, failure isolation, the summary |
| `internal/kind` | The kind interface, the descriptor each kind gives of itself, and the comparison that makes a kind a step |
| `internal/kind/<kind>` | A kind each, with its descriptor: `brew` (Homebrew's formulae and casks), `appstore`, `npm`, `composer`, `gotool`, `ghext`, `tmux`, `login`, `mcp`, `skill`, `secret`, `gitconfig`, `defaults`, `power`, `exclusion`, `spotlight` |
| `internal/kinds` | The list of kinds, in the run's order: what the rest of kit reads them from |
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
| 6 | Each secrets store signed in, with a session open, before its secrets | later |
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
up front; `kit <kind> add` and `kit <kind> remove` (`--shared`, `--temp`, `--note`; and `--group`,
with the group asked at a terminal, both gone with the groups in slice 12); `kit reconcile`, at
a terminal and by id; `kit list` and `kit why` (#12–#19).

**3. The other kinds — built.** Every kind through one table; a step can come after another
while applying, and a check can defer its own step; the administrator's password up front for
any kind; App Store apps; npm and Composer packages; Go tools and GitHub CLI extensions; tmux
plugins; login items (#21–#26).

**4. Claude Code's MCP servers — built.** The `claude-mcp` kind, user-level and per
project; `kit claude-mcp on|off`; reconcile by kind (#30–#32).

**5. One declarations file per Mac — built.** The config repository is `kit.toml`, `shared`
and a file per Mac, each sections of flat lines; MCP servers as `claude mcp add`'s
options.

**6. Claude Code's plugins — built, then retired.** The `claude-plugin` kind, marketplaces
carried in plugins' names; retired once Claude Code's own settings, saved by `kit prefs`,
proved to declare plugins, Claude Code fetching what they enable.

**7. Health checks and switches — built.** The Mac's checks and the config repository's sync
(#36); `[features]`, `kit feature on|off`, Time Machine and Arq (#37); checks of the user's
own (#38); the scan made to see untracked files (#39); bare `kit` (#40).

**8. `kit nightly` — built.** Jobs of the user's own (`[hourly]`, `[nightly]`), the nightly
record and its rule, `kit nightly`; Arq checked by its own schedules (#41); the Scratch
clean-up and settings capture (#42); the checks on the runs, Scratch and Full Disk Access
(#43); `--alerts`, drift's daily digest and the report (#44).

**9. Steps — built.** A folder per Mac in the config repository; linked files; the
config repository's own edits settled by `kit reconcile`; the shell and PATH, git, macOS
settings and backup exclusions as kinds, secrets, what a person must do by hand; commands
adding lines to `[checks]`, `[hourly]` and `[nightly]`; commands naming the kind first.

**10. Apps' settings — in progress.** `kit prefs`, the dotfiles' prefsync in Go: its store in a
repository of its own, below.

**Then:** the
primary reading the other Macs; the bootstrap and its install script, tested in a virtual
machine; data restore and moving to a new Mac; an agent's daily check, with decisions queued
for a person. Claude Code's skills wait for the installer they'll go through. Each gets a
short plan before code.

## Apps' settings (`kit prefs`)

Every preferences domain that isn't Apple's or the system's (no dot in its name, or `com.apple.`,
`group.com.apple.`, `systemgroup.`, `.GlobalPreferences`, `org.cups.`), through `defaults export`
and `import` (cfprefsd's view; a plist copied behind its back half-sticks), and the settings files
`[prefs files]` names (folders taken whole, globs with Python's rules: a wildcard never matches a
hidden name). Each Mac saves into its own folder of one git repository, `kit.toml`'s
`prefs_repo`, cloned in kit's data folder (`~/.local/share/kit/prefs`), in prefsync's format byte
for byte: `<mac>/domains/<domain>.plist`, sorted XML as Python's plistlib writes it, the keys that
change by themselves stripped (window frames, list views, file dialogs, Sparkle's check times);
`<mac>/files/` mirroring each file's path under the home folder (`_root/` outside it); `<mac>/owner`,
the hardware UUID of the Mac the folder belongs to.

- **`kit prefs capture`** (nightly, as the `prefs` feature's job): needs capture switched on (a
  full restore, or `kit prefs start-fresh`: a rebuilt Mac never captures defaults over a good
  store), Full Disk Access (without it sandboxed apps export as empty), and this Mac owning its
  folder (claimed by the first capture; another Mac, such as a replacement given the same name,
  is refused until it takes ownership). It pulls, writes only what changed, removes what's gone
  from the Mac (history keeps it), leaves domains waiting to be restored alone, keeps a domain
  that fails to export, commits once in prefsync's message format, and pushes, only to a
  repository private on GitHub (or not on GitHub). A push that fails waits for the next.
- **`kit prefs restore [<domain>...]`** (`--from`, `--at`, `--as`, `--pending`, `--dry-run`): a
  domain's app found from the installed apps' bundle ids (sub-domains, shared domains, group and
  team prefixes, `[prefs apps]` for the rest); not installed or running, it waits; a sandboxed
  app launched hidden once to make its container, the run's log naming it as it opens (its own
  windows, notifications and permission prompts may follow); the stored copy checked, the live
  settings saved, `defaults delete` then `import`, read back and compared. A full restore puts
  back the files that differ (each saved first), writes the pending list, records its commit, and
  switches capture on, failures or not. What it couldn't restore waits on the pending list,
  each with why, and capture leaves it alone: a domain whose app isn't installed or is running;
  one whose sandboxed app has no settings folder, as none came of kit opening it, for the person
  to open once (a to-do, below); and a domain or file that failed otherwise, named with why in
  plain words. kit opens an app at most once, and only in a restore the person started (the
  run's `r`, or `kit prefs restore` by hand). The hourly pass (`--pending`, unattended) opens
  none: it brings back a waiting domain once its app is installed, not running and, sandboxed,
  has its settings folder (one without stays for the person to open), and leaves what failed
  otherwise alone; `kit prefs restore --pending`, by hand, tries everything waiting, failed or
  not, opening an app once. From another Mac's folder, or onto other hardware,
  `[prefs machine-bound]` stays behind (Tailscale's identity and the like). `--at` unpacks the
  folder as it was then from history.
- **`kit prefs history`**, **`pending [--clear]`** (what waits, each with why), **`start-fresh`**.
  An app held for its settings folder is a to-do of its own, watched, titled with the app (`Open
  <app> once and quit it`): it moves on once the app has its folder and is quit, and its settings
  come back then, opening nothing. An app to open that's running stays to open till it's quit. What failed
  otherwise is said by the check, apps' settings and settings files apart, as held for `kit prefs
  restore --pending`, after anything wrong with capture itself. A held
  file's saved copy stays in the store, on the Mac or not. One restore opens an app once,
  however many domains it has. `[prefs deny]` leaves
  domains out.

## Open-source hygiene

- No personal data in the repo or its history, ever (see House rules in `CLAUDE.md`);
  `scripts/personal-data-scan` checks the files and every commit, in `scripts/gates`.
- Public, under the MIT licence. Released by the maintainer, once kit is ready, with mint,
  GoReleaser and a Homebrew tap.
