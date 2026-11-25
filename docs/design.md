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
(`cask`), App Store apps (`mas`), npm and Composer global packages (`npm`, `composer`), GitHub
CLI extensions (`gh`), login items (`login-item`), Claude Code's MCP servers (`claude-mcp`),
Claude's skills for every project (`claude-skill`, installed by agntc), git's settings
(`git-config`) and
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
undeclared for adopting, removing or snoozing, and report both. A kind that can take over
what was put on the Mac apart from it (`kind.Adoptable`: casks, an app installed by hand)
reports each such thing not declared as drift of its own, `by-hand`, offered for adopting or
leaving by hand, never removing.

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
it held of it. A step a kind needs, as Homebrew's for its formulae and casks, is in the run
only with a kind needing it. `kit <kind> add`
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
| `gh` | `owner/gh-extension` | `gh extension list` | `gh extension install`, `gh extension remove` |
| `login-item` | the app's bundle id, `com.example.app` | System Events' login items | System Events (JavaScript for Automation) |
| `claude-mcp` | a name, then `claude mcp add`'s options; a project's in `[claude mcp <folder>]` | `~/.claude.json`, read directly | `claude mcp add-json`, `claude mcp remove` (a project's in local scope, in its folder) |
| `claude-skill` | `[claude skills]`: agntc's key for an install, a GitHub repo and what in it, `github/gh-stack/gh-stack` (the skill of that name; a path, the pack there; the repo alone when it's one skill or pack), `@version` or a range optional | agntc's record, `~/.agntc/manifest.json`, read directly (`agntc list` asks every repo what's newer) | `agntc add -g` (`--all` for a repo alone, `--skill` for a name, else `--pack`), `agntc remove -g -y`; `--json` for agntc's own words when one fails |
| `git-config` | `[git config]`: a key, then its value as one word, as `git config --global` takes them (`alias.st "status -sb"`) | `git config --global --list` | `git config --global --replace-all`, `--unset-all` |
| `power` | `[power settings]`: pmset's words, the source then the setting and value (`-c sleep 0`; `-a` every source, `-b` the battery, `-u` a UPS), named `charger:sleep` | `pmset -g custom`, by source | `sudo pmset`; undeclaring leaves the value |
| `backup-exclusion` | `[backup exclusions]`: a path a line, `~` and `*` globs, expanded on every check (a plain path stands for itself, there or not) | what the backup tools the Mac lists in `[backups]` keep out, each through its driver (a path declared kept out while every one keeps it out): Time Machine's fixed-path exclusions (`SkipPaths`), and each glob match excluded where it is (`tmutil isexcluded`); Arq keeps none of its own, following Time Machine's list ("Skip items excluded by Time Machine rules"), so Arq alone has Time Machine's written, no backup disk needed; with no tool listed the check fails, saying so | through each tool's driver, Time Machine's: a plain path `sudo tmutil addexclusion -p`; a glob's match `tmutil addexclusion`, no password, so `kit nightly` does it unattended; `removeexclusion -p`. Each needs Full Disk Access for what kit runs in (reading them needs none, so the check always looks), so its installs and removals are held ("needs Full Disk Access", "needs kit.app's Full Disk Access") while the probe finds it not granted, nothing there to read holding nothing, tmutil left to say; meanwhile it needs no administrator's password, nor waits for one. At a terminal the step is applied after `full-disk-access`, whose card it waits behind; the hourly run's probes for itself, never after `kit-app-access`, which comes after Homebrew's formulae. A change held, or tmutil refusing for want of it all the same (exit 80, "requires Full Disk Access"), which ends the step at once, said once rather than a path at a time, has that step checked again, not granted, its to-do saying what to do (raised, with exclusions to apply), and the exclusions held behind it; with that step not in the run, as in `kit apply backup-exclusion`, the wait says what to do itself |
| `spotlight-exclusion` | `[spotlight exclusions]`: a folder a line | Spotlight asked for files of any kind in each folder (`mdfind -count`), by their names, a folder holding none, or not there yet (never asked about, nor made), counting as kept out, with nothing to keep out yet, and one whose count runs out of time as indexed; its own list is root's alone, so nothing extra is found | added to Spotlight's privacy list through sudo, taking effect after a restart (kit remembers till it's kept out or undeclared, and says so); removing is System Settings' |
| `secret` | `[secrets <item>]`, a section for each item a file keeps secrets in, in any store kit has a driver for, as in `[secrets op://vault/item]` (the header says where they are; several spread them across items and stores), `--as <name>` after the item naming the block, so commands can say `--in <name>` (a name stands for one item, in the files a Mac reads): what it fills (a variable's name, or a file's path) then the field or attachment, short (`GitHub/token`); a plain `[secrets]` for references in full (`op://vault/item/field`); a name once a file. `--mode 644` for a file's. GitHub Actions secrets aren't kit's: they're a repository's, not the Mac's | `~/.secrets.zsh`'s exports, the files with their modes: never the stores, which may ask the person each time (1Password for a fingerprint each hour) | a sync: each value read alone through its reference's store driver, picked by the reference's scheme (`op://` is 1Password's), each store waiting for its own sign-in (one signed out holds back its own secrets alone), and a read waiting for the person's approval as long as they take, the step saying so; a reference the store names as one it couldn't read fails, in its words, and the rest are read on, while any other failure (not approved, or not in time) holds back the store's secrets not yet read, for the next sync, as reading on would ask the person again for each. 1Password's driver runs op in kit's session, so the person approves once a terminal, the approval covering every op that follows (signed in with a password, there's none to share): a variable's value with `op read`, its output kept from the log; a file's an attachment byte for byte, with `op read --out-file` into a private folder removed after (through a pipe, `op read` ends a value with a newline unless it ends with one, so the two read alike), a field's text with `op read`, ended by one newline; `~/.secrets.zsh` written whole with each value single-quoted, without the newlines it ends with (one that fails to read keeps its line, in whatever form, unless a value of many lines kit once wrote cut it short, a single quote left open), a value holding a line break failing, as a variable is one line (declare it as a file), files written beside and renamed in; `kit secret sync` forces one. A store whose approval needs the person at the Mac's screen (1Password's fingerprint) isn't asked while that screen is locked (ioreg's IOConsoleUsers): kit fails at once, in the store's words, naming `kit secret signin`, left open, which has each store with a password sign-in (`store.Passworded`) ask the person's password at their own terminal, kit never seeing it. A password's session holds only for what runs in that terminal (1Password's, proven 25 Nov 2026: the same session from another shell is refused), so `kit secret signin`, once the person's signed in (it asks nothing if one's open already), listens and stays open there, holding each session in memory alone, never on disk, and relays (`kind/secret/relay`, which knows no store): it listens on a unix socket in kit's state (`secret-signin.sock`, 0600, a peer of another uid refused: the uid is the one boundary, so anything running as the person can use the sign-in while it's open, as the old session file allowed, and the docs say so), and every other kit (an agent's, the hourly run's) asks it once a run whether it holds a store's sign-in that lasts (the driver's `Lasts`, or yes at once while a command runs); if so, that store's commands, as its driver builds them, go there through the runner (beneath the run's log, which records them as ever), and the relay runs each, one at a time, only if it's the store's program, in the session (`Apply`), giving back output and exit, the screen never read; a command the relay doesn't answer fails rather than run here, where the store would ask the person. The relay takes each request up at once, before answering, so a relay stopped (ctrl-Z) costs a client 5 seconds without that, or a minute at most for an answer, once a run: that store's commands after fail at once. It ends at ctrl-C, its terminal closing, `kit secret signout` (a stop request, waited for 30 seconds; with none open, it says so), or the store saying the session's over (asked, or after a command fails): it signs out at the store, removes the socket and says why. A socket nothing answers is stale: clients leave it, and the next `kit secret signin` replaces it, the look and the bind under a lock beside it (`secret-signin.sock.lock`). Removing takes a secret off the Mac, its value kept in its store or deleted from it (`--keep-value`, `--delete-value`, asked at a terminal) |
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
  it was (a dict whose entries are declared is watched entry by entry). One removed since is
  removed: reconcile's `--adopt` accepts it gone, kit forgetting its value and declaring
  nothing, or `--revert` puts it back.
- **A kind's program:** while it isn't installed, a kind in the run is deferred, saying what
  it needs, or, with nothing declared, fine.
  Where a kind's command comes from isn't kit's to know: while kit applies (not `kit
  status`, not the nightly run), a kind whose command isn't on kit's PATH yet (npm's `npm`,
  each secrets store's, as 1Password's `op`, MCP's `claude`) waits for it before its check,
  saying so, looking again as installs end, till it's there or nothing still at work in the run could bring it,
  then stands deferred, saying it isn't there yet. The config's waits order the rest (npm
  after mise, `--before npm`). Three orders stay, being kit's own data: each store's
  secrets after its sign-in, the sign-in after what its driver names it comes as, declared
  for the Mac (1Password's casks), so those install first, or till its program and app are
  there however they came (by hand too, the cask then taking the app over); git's settings after the secrets
  they sign with; login items after the casks and App Store apps they open.
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
  (but a kind's last, as above). Adding or adopting a thing whose install its kind says
  needs a secret declares it in its kind's section held for that secret, made when there's
  none, saying so; refused, before anything's installed, while the secret isn't declared for
  every Mac the line reaches, what finding out changed put back. Homebrew's: a tap's package
  (by its full name, as brew info gives it), whose downloads (a formula's source and its
  bottle; a cask's) are each asked for once without a token, `~/.curlrc` unread: GitHub
  answering 401 or 404 means private, needing `HOMEBREW_GITHUB_API_TOKEN`; any 2xx, open;
  anything else, or no answer, can't say. Its tap, added to look in, is taken out again for
  an add refused; the add's row says it's checking meanwhile. The add's own install has the
  secret set as applying would, once it's in place. A check that can't be made declares the
  line plainly, saying so; one stopped fails the add.
- **What's waited for goes first:** a thing a step names (`--after cask:firefox`) is
  installed ahead of the rest of its list, as one command, then the rest as one, as is what
  a driver says its tool comes as, declared for the Mac (Arq's cask; 1Password's casks,
  before its sign-in); where kinds
  share a tool's lock (Homebrew's formulae and casks), neither's rest begins till the other's
  waited-for things are in, or their step has finished. A wait looks again whenever an
  install ends or counts a thing done, and otherwise every 30 seconds.
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
entries (the kinds, `[restore]`), a piece with nothing to list by a switch
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
waits its turn; one placed after or before what isn't a step fails its check, saying so;
what the tool comes as, its driver's `Package`, declared for the Mac, is waited for
whatever its line says, as a secrets store's sign-in waits for its driver's packages). A
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
everything that's the tool's: its name; what installs it, as a line names one of a step's
things (`cask:arq`; nothing for Time Machine, which comes with macOS); whether it's set up,
and, when it isn't, setting it up as a to-do (its title, why, its steps, what kit watches
for, and in a line for `kit
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
expected to run), one at least, each backed up within 26 hours; not installed says so, and how
to install it, as kit's shared code words it from what the driver says installs it and the
config: `kit apply` installs it, its cask declared for the Mac, or, its cask in but not Arq,
it says that plainly; else `kit cask add arq`. Its status unreadable says so; its licence
agreement not yet accepted, or no plan made, is not set up.

**Checks.** Steps with a check and no apply, each problem an item with a stable id saying
what's wrong and what to do: the Mac's (`disk`, `memory`, `load`, and `kit-version`: a newer release
than the kit running, as `kit nightly` last recorded it, is an upgrade to do, `brew upgrade
leeovery/tools/kit`; a build that isn't a release's, or no record yet, claims nothing), always;
the backup tools' a Mac lists in `[backups]` (`time-machine`, `arq`: each runs on its own
schedule, and kit checks it did, only the plans Arq is scheduled to run; Backups above); the config repository's
(`config-sync`: commits that won't push, and the remote's not brought in for a day; `config-private`); the scheduled runs' (`nightly`: the hourly run within two
hours, or the hourly launch loaded within them, its first run to come; the nightly one finished within 26 and none
stuck over six, each job's last outcome; never run on a schedule, its launch never loaded, is a problem, `kit apply`
installing it); with its feature, `prefs` (apps' settings saved: capture switched on, this Mac owning its folder, a capture
in the last day without errors, pushed within a day; read from kit's record alone) with
`full-disk-access` (the grant of what kit runs in, what a capture there needs, as backup exclusions do, with which it's checked too); and the user's own, in a `[checks]`
section, a line each:
a name, then `--` and a command, which exits 0 when all's well, or prints what's wrong on
its first line (a minute's timeout), each failing one an item of the `checks` step. Every step has an area (Backups, Jobs, Mac, Drift,
Config, Checks), which the status document carries. What only the person at the Mac can do
about a check is a to-do, an item with a title, why, steps, what kit watches for, a group and,
for a choice, its keys, raised at the end of applying (Steps by hand): restoring application
settings (`prefs`, paused), a Time Machine disk to add, Arq's backup plan to set up; titled the
same wherever the item shows. The rest (disk space, a stale backup, commits that won't push)
are how the Mac stands, not to-dos. Full Disk Access isn't one for apps' settings alone: a
grant takes effect only once the terminal restarts, so kit couldn't watch for it. With
backup exclusions to apply, held for want of it, at a terminal, it is one all the same, as they
wait till it's given (with every one in place, it's named alone): "Give Ghostty Full Disk Access, then reopen it" (the terminal named as `TERM_PROGRAM`
says: Ghostty, Terminal, iTerm, else "the terminal kit runs in"), its steps turning it on in
System Settings › Privacy & Security › Full Disk Access, quitting and opening the terminal
again, and `kit apply backup-exclusion`; kit can't see it done in the run, so the person leaves
it for later. Its item names the terminal either way, and its why says what's declared. Over
SSH the item is "Allow Full Disk Access for remote users", naming Remote Login's switch, then
`kit apply backup-exclusion`, never a to-do of kit's: a step by hand of the config's own covers
it, if any. kit.app's grant is `kit-app-access`'s.

**Adding a secret:** `kit secret add <name>` takes its value typed (unshown), on standard
input (`--stdin`, how an agent passes one, never on a command line), from a file (`--from`,
kept as an attachment, named as the file is or as `--field` says, in a section as
`<section>/<name>`), or as an existing reference (`--ref`, nothing stored: its line goes in
the file's block for its item, short, when the file has one, else in the plain `[secrets]`, in
full). A new value goes where it's told, never by default: `--in <name>`, a block by the name
its header gives it (`--as`), or `--item` and an item, in full; with neither it's refused,
listing the blocks, as `kit secret places` does (each block's name, store, item, file and how
many it holds). `--field` says where in the item, in its store's words (1Password's
`<section>/<field>`). The line goes in the file's block for that item, which kit makes when
there's none, going by the name `--in` gave. Everything's checked before the value's asked
for: where it goes, the line as the config would read with it, the store answering, and
nothing kept there already. The value's kept through its store's driver (1Password's edits the
item with its JSON on op's standard input), then read back and compared, declared, and put in
place, never shown: that secret alone, as its store gave it back, no other read from its store
(`--ref`'s read once; a file's value kept as a field read again in a file's form, as a sync
reads it). A file's written alone; a variable's line takes its place in `~/.secrets.zsh`, or
goes where a sync would write it, every other line kept byte for byte, the file made with its
header and that line when there's none (the rest come with the next sync). A variable's value
of many lines is refused before it's stored. `--mode` as on the line. `--shared` declares it in
the shared file, in the block `--in` or `--item` names, or, with `--ref`, as above (the shared
file having no block for its item, in one made for the item of the Mac's block holding it,
going by that block's name), and takes it out of each Mac's own file, by the rule for moving a
line (below): a Mac's line written otherwise is refused before anything's stored; one the
shared file has already is refused. `kit secret remove <name>` takes it off the Mac,
undeclares it, and keeps or deletes its value in its store (`--keep-value`, `--delete-value`;
asked at a terminal); a value given in full, outside the items' blocks, is left where it is.

**Steps by hand** go in a `[manual]` section, a line each: a name, what to do in quotes, then
`--after` and `--before` (as on any line), `--group <name>` (the group it's in, one word or
quoted, at most once), and `--test` and a command saying whether it's done (exit 0: done), the
rest of the line, which can't hold kit's own options (they go before `--test`), as in `tool "Install the tool from its site" --test test -d
/Applications/Tool.app`. The comment lines directly above it, no blank line between, are its
notes: why it's there, one paragraph. The lines indented under it are its steps, one each, in
order, which kit keeps with it, notes and all; a step ending with a colon heads the steps after
it, as in `If it doesn't ask, in System Settings:`, shown muted and unnumbered, the numbering
carrying on across it. kit opens nothing for a step by hand: its steps say what to open, and the
person does it. One with no command is done once marked on this
Mac (`kit manual done <name>`, `--undo` to unmark). Those not done are items of their step, in
the Manual area, saying what to do, so `kit status` shows them; what kit can check itself stays
a check that says what to do. While `kit apply` runs at a terminal, one that comes due is raised
as a card on its screen, front and centre: its title, why it's there, said once (its notes, or,
for a line with none, kit's words for why the run waits for it), its steps numbered (a link in
them, or in its why, one the terminal opens when it's clicked: a URL, or a bare host and path,
as `github.com/owner/tool/releases`, over https; never a version, a path on the Mac or a
command), and a notification while it's up (its title, then the first sentence of its why, or, with
none, its first step that isn't its title, cut after a word past 90 characters; never the title
twice), once a look finds it not done or it's been up three seconds,
whichever's first (taken away while it's set aside, posted again as it comes back). Its turn
come, kit looks at once whether it's done, and it goes up as that look finds it not done, or
after a quarter of a second, whichever's first, so one found done by then never shows, opens
nothing and posts nothing, nor has its row say it waits for you; one that's been up, or that a
look found not done no longer ago than that, goes up at once. Its keys work from the
moment it's up: nothing a card does on the side holds it or them up. Whatever takes time goes
aside: whether it's done, looked at as its
turn comes and every few seconds; what a key says; readying what it says, as the date a
choice's key brings back from (it's up meanwhile saying what it can, then up again saying
that); and, once it's up, its notification and its app, the notification in order with its
taking away, so one waiting for kit's app to be built (a minute, on a new Mac) holds nothing up,
and one taken away before it went out is never posted. A look, a key and readying go one at a
time, keys in the order pressed, ahead of the next look, the card saying at once that a key's
taken, as a choice's does; a key behind a look that finds it done isn't done. A card holds none of the run's jobs while it's up, and,
done, or found done, comes down that moment, every job busy or not: a step with nothing more to
do finishes without waiting for one, and one with more (the restore, which copies once its
source card's answered) waits for a job with the card down. `space` says it's done (`enter`
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
(Checks). That end is a list worked through one to-do at a time, in kit's order, with no choosing:
kit's own that come first (restoring application settings, so the apps opened
next start with them), then the lines not done, in order, then kit's own the rest (the backups');
grouped, each group where its first comes (kit's own in Apps and Backups). Each is looked at
while it's up, a line by its command every few seconds, one of kit's own by its step's check, and
once done its card comes down and the list ticks it at once, the log saying so, how long it took
and what kit saw, while kit looks aside for any to-dos it leaves, and the next comes up; `space`
says one with no command is done; a choice is answered by its keys, none offered while kit does
what the one pressed says, its words turning in their place, and keys pressed meanwhile dropped,
what it does never cut short, even by the run stopping, as half of apps' settings brought back
is worse than none ("Restore application settings", whose why says, when the config has
`[prefs files]`, that settings files are restored too: `r` restores its one source, its date
said: this machine ID's last save ("(26 Oct)"), as `kit prefs restore` does; else, with none,
that of the Mac `kit.toml`'s `from` names ("restores studio's last save (26 Oct) onto this Mac;
studio keeps its own"), as `kit prefs restore --from` does, machine-bound settings left out.
With no source in the store, there's no card: applying the step starts afresh by itself, the log
saying so (a store it can't read leaves the card as it stands). `r` says how many came back, how
many wait for their apps to quit, how many are kept for apps not installed, how many apps to open once (a to-do each, coming next, kit's own done
leading to those its check finds then) and how many are held for `kit prefs restore --pending`,
the log naming each of those with why; `f` starts afresh, as `kit prefs start-fresh` does,
saying saving's on and how many still wait to come back. When other hardware owns this machine
ID's folder, the card says once "This Mac takes
over <id> from <owner>, which stops saving to it" (the owner by its name, else its model, else
its hardware), and any key then writes this Mac's owner file there, after what the key does:
the next capture commits and pushes it, and the other Mac's next capture is refused). Over the
list, the left pane says "kit can't do these itself: work through them one at a time, as the card
on the right says", after "Everything else is done." unless a step failed. No to-do holds the run: every one has `s`, "skips it for now: kit apply brings it back"
(the first letter after it its own keys haven't, should they have `s`, never `q` or `r`), which
takes it down at once, whatever's under way but a key being done (then `s` is gone, the choice made), the next coming up; it
stays to do, nothing written as though it were done, the list marking it skipped, the log
saying so and how long it was up, the record counting it among what's left; a look or a key
under way as it's pressed, left to finish, has it settling meanwhile, and, finding it done after
all, has it done, which the list says the moment it's in, whatever's up then, the to-dos its
step's check finds then coming at the list's end (raised this run if the list's still going,
listed to do if it's over), and the run's end waiting for it, the state line saying what kit's
finishing. Kit finishes what to-dos done waited for one at a time, as two restores of apps'
settings would clash. Stopping there (`q`'s Stop, between two to-dos as well, as the one up is found done, and
while kit does what a key says, taken even with another key waiting unread) leaves what's left
to do at once, the card down and the list stopped, whatever kit's doing aside, and no look
begins after, readying what the card says cut short (it can never find it done, and a store's
first clone cut short leaves nothing, the next look cloning again): the one up done if it was,
or if a look under way then, or a key taken by then, left to finish, finds it done or does it,
which the list says after, with any to-do that leaves, the log saying how it ended only then;
with the to-dos it left, in that order in the record, the run ending as it stands. The run
stopped there, the card comes down and the list stops at once too. After the last, kit checks
again the steps the to-dos held, those they came from, so their rows and the record are right,
then finishes.
Without a terminal, nothing's raised: kit's own stay items of their steps. A step a line comes
before can find, as it applies, that the line wasn't done after all, as the App Store's first
install finds no one signed in (signing in has no `--test`: mas can't say who's signed in, and
macOS keeps it private): the line, marked done with `space`, is marked done no more, so the next
`kit apply` raises its card again; this run doesn't, and the step fails, naming the fix (sign in
to the App Store, then try again), with what failed before it, the rest of the run carrying on. A
line with a `--test`, or one of kit's own, kit tells done itself: there's nothing to unmark. So
kit installs App Store apps one at a time, the first the check: mas, with no one signed in, says
so or waits for good, saying nothing at all, so till an app's in, mas saying nothing for five
minutes is taken as no one signed in, as one mas says it of is, and the apps after it aren't
tried. Once mas has said anything, as that it's downloading, the install takes as long as it
takes (it draws its progress only at a terminal, so through kit it says nothing more till it's
done), up to an hour and a half (Xcode is gigabytes); one failing holds none of the others up.
What's left of an install kit ended, in its process group, is stopped: mas runs as root, which
only root can stop; an install that fails otherwise says what mas said, or that mas didn't finish, never
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
changes, with git's own commit: the paths it changed, each whole as it is on disk (a deletion, a
new folder's every file), and nothing else (`git commit --only`), so the user's staged and
unstaged changes to any other file stay as they are. Files new to git are entered in its index
first as to be added (`git add --intent-to-add`), they alone, never `git add -A`, so a
`git commit` made by hand meanwhile takes none of kit's files; committing nothing, kit takes them
back out (`git rm --cached`), git's index as it found it. No hook runs
(`-c core.hooksPath=/dev/null`, as `--no-verify` still runs `prepare-commit-msg` and
`post-commit`). It's signed as `git commit` signs, by the user's own config: set to sign and
unable to, as when the key isn't on the Mac yet, nothing's committed, and the step waits for the
next apply. Another git holding the index (`index.lock`) a moment, as `git status` does, kit
tries again for about two seconds, then says what git said. Once begun, the commit finishes
whatever becomes of the run. A change git's commit refuses, as a tracked file become a folder,
kit says so, naming the file, and it waits as drift. It refuses while git is part way through a
merge, rebase, cherry-pick, revert or `git am`, or a conflict in its paths waits to be resolved:
its change waits till that's finished. Nothing's pushed after a commit that failed. What git
can't keep (an ignored file, a file added then gone, a repository nested in kit-config, which
`kit reconcile` names and only snoozes) is left out. With nothing to commit, the files on disk
as last committed, it says nothing changed, pushing only commits still waiting for the
upstream, as when a push failed. Drift is kit-config's files on disk against the last commit,
as git's index has them: the `config` step makes each file whose working copy differs from it an
item (`config:shared/home/.zshrc`, edited, added, as one to be added with `git add -N` is, or
deleted, with the lines it adds and removes, a file taken out of git's index and kept on disk,
ignored, deleted as git reads it; a repository, by its folder, `config:vendor/tool/`), quiet for
a day as drift is; any other difference only in git's index, the file on disk as last
committed, is the user's own business in git, never drift. `kit reconcile` shows an edit's diff
and offers adopt (commit it, then push, `--note` the message's end), revert (back to the last
commit; a new file to the Bin) or snooze.

kit pulls before it pushes, and pulls the prefs store's clone the same way (`kit prefs`), with
the repository's one remote: with several, it pushes to none, and says so, and HEAD on no branch
is refused. It refuses while git is part way through one of its operations, or a conflict waits,
as setting work aside would end the one; then it fetches the remote, pruning its branches gone
from it (a remote out of reach said plainly). The push names that remote and the upstream's
branch on it (`git push <remote> HEAD:<branch>`), so nothing in git's config, as `pushRemote` or
`pushDefault`, sends it anywhere but where its URLs were proven private. A branch with no
upstream is pushed to the remote's branch of its name, which becomes its upstream; an upstream
not there yet, as on a new, empty remote, has nothing to bring in. A clone of one branch, as
kit-config's (the boot's) and the store's are, fetches that branch alone, and git fails that
fetch while the remote hasn't the branch. For the store alone, whose branch is missing before
its first push (or once deleted), a fetch failing for want of the upstream's own branch is an
upstream not there yet, its remote-tracking ref deleted so what waits is counted against the
remote as it is; kit-config's branch gone from its remote stays a failure, never pushed anew.
Commits waiting to be pushed
are those the upstream hasn't, or, with none or it not there, those on none of the remote's
branches as the clone fetches them, so a failed first push, or commits made offline, show as
waiting. Two histories with no commit in common, as when two Macs each began the store's branch
before either pushed, are the same pull in a repository whose remote branch may be missing, the
store or kit-config on an empty remote (any other kit-config with no commit in common with its
upstream tracks the wrong branch, and is refused, as git's diff fails): the remote's commits
change every path the remote has (git's diff from where the two parted fails with no such place,
so kit reads the remote's whole tree), and the rebase replays every commit here onto the remote's. A Mac whose empty store clone
committed offline, while the remote's new branch already holds that Mac's own folder, conflicts on
every rebase till the clone is moved aside; that happens only if another source pushed this Mac's
folder. When the upstream
has commits this Mac hasn't, kit rebases onto them by the commits since the two parted (never
git's fork-point, which can leave out a commit, so the rebase changes no more than the remote's
commits do), so two Macs don't fight. What's not committed in the files git tracks is set aside
first, in a stash of its own that's never in git's list of stashes (`git stash create`), the
files reset to the last commit, and put back by it once rebased (`git stash apply --index`), so
what was staged is staged still, as `--autostash` wouldn't have it; files git doesn't track stay
where they are. Should it not go back, it's kept in git's list of stashes, as "kit: your changes
before pulling", so nothing's lost, and kit says so; should the reset fail, everything's left as
it was. But when the remote's commits change a file not committed here, staged or not, new, or
in a repository nested here, nothing is set aside, rebased or pushed, and kit names the files:
put back, the change would conflict. Nor is anything when a file git tracks is a folder now (a
folder on disk where git says the file's deleted or changed in type, which takes in a repository
of its own, whose files git doesn't list), or a folder a file (a file git doesn't track over the
folder's files): reset to the last commit, the path would take the place of what's there,
deleting it, so kit names it. `kit status`'s Sync line says so too, as far as the last fetch
tells, while it holds commits back. A rebase that stops on a conflict, judged under way by git's
own record of it, is aborted and what was set aside put back, so nothing's lost; one that stops
as git, set to sign, can't sign a commit it rebases is aborted the same, and said as a commit
unable to sign is; one that never started leaves nothing to undo; either way nothing is pushed.

kit-config's remote commits also come in as a run starts (`gitrepo`'s `BringIn`): `kit apply`,
`kit status`, `kit reconcile` and `kit nightly`, the hourly run, bring them in before they read
the config, so a change one Mac pushes reaches the other within the hour, and drift is judged
against it. `--plan` runs nothing, so brings nothing in. `kit list`, `kit why`, `kit log`, bare
`kit` and the commands that change kit-config don't, staying quick and offline (those pull as
they push); the boot and `kit setup` clone it. What isn't committed is left exactly as it is,
or nothing's brought in: with no commit here the upstream hasn't, the branch moves on to it
(`git merge --ff-only --no-overwrite-ignore`), which writes only the files the remote's commits
change, an edit elsewhere, staged or not, and a new file untouched (proven on real git: the
same inode and time), and refuses to overwrite a file git ignores; with commits waiting to be
pushed, they're rebased onto the remote's as a push's pull rebases them, only while no file git
tracks is changed and not committed, as the rebase would set it aside and put it back, writing
it twice, and while git doesn't sign its commits, as the rebase would ask for a signature,
perhaps by Touch ID, with nobody there; else they're kept as they are, for the next push by
hand to rebase and sign. As a push's pull does, it refuses, touching nothing, while git is part
way through one of its operations or a conflict waits, and when the remote's commits change a
file not committed here, or add one git ignores that's on disk here (which a push's rebase also
refuses, as git overwrites such a file without a word), naming it. Both refuse, too, while HEAD
holds a commit the remote's history had and dropped, as when another Mac forced a push taking
out a commit this Mac had pulled: rebasing onto it, or pushing, would put the commit back.
`git merge-base --fork-point` finds it in the upstream's reflog, which outlives every fetch, a
run's or one by hand, as a fork point past where HEAD and the upstream meet now (`git
merge-base`); the two agree with nothing rewritten (proven on real git: behind, ahead, both, a
fresh clone, an emptied reflog), and again once the commit's gone from HEAD, as after `git
reset --keep @{u}` by hand, when runs carry on. No fork point (exit 1), as once the reflog's
record of the commit expires, is no refusal; nor is no history in common (`git merge-base`
exiting 1, as the prefs store's branch deleted and pushed anew, unrelated, by another Mac,
the reflog still forking from it), which the pull handles as an unrelated history. `Holding`
checks it too, so `config-sync` says why a dropped commit isn't pushed. A rebase that stops is
aborted. The wait for another kit and the fetch have 20 seconds, the prefs store's look's time,
then the run carries on with the copy it has; once fetched, the rest is git's on the Mac, and finishes. Committing,
pulling and pushing all hold one lock in the git folder (`kit.lock`, kit's own: git has none
but its index lock), Push through the push itself, so the hourly run and a command by hand take
turns, never committing, rebasing, fetching or pushing over each other; each takes it once, at
its outset, and a command by hand waits as long as it takes. It never fails the run: what came
in is read as the run's config, one this kit can't read failing the run as any config it can't
read does. A config this kit can't read before, as a newer kit's setting or a bad edit from
another Mac, is brought in on kit's base PATH (the system's folders and Homebrew's), as the fix
may have been pushed, then read again; still unreadable, the run fails as it would have, saying
why nothing came in if nothing could. A Mac with no name brings nothing in, nothing running
before its name. `config-sync` says how it went, once: the commits brought in after its summary
and counted (`brought_in`), a row of their own under kit-config's in `kit status`; or an item
(`config-sync:pull`) saying why none came in and what to do, quiet on its first day as drift
is, its first sighting in the drift record, then a problem, kept with the step even when its
own check fails.
What a program says, kit looks for on the line that carries its error (git's `fatal:`, or ssh's
own lines above it, some with no prefix of ssh's; mas's `Error:` or `[ERROR]`; brew's `Error:`;
osascript's `execution error:`; sudo's own), as the reason is often above the last line, and a
line that merely mentions it isn't the error; git runs in the C locale for it, as git translates
`fatal:`.

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
all: each of those steps that can be unmarked (a step by hand of its own, marked done) is, for the
next run to do it again. A run that's stopped (an interrupt, a SIGTERM, or its terminal closing)
fails the steps it hadn't started; a job begun on git (a commit, or a pull's rebase and putting
back what it set aside) finishes first, and what kit writes to a terminal that's gone is lost,
not kit. A step whose check failed, as one that didn't answer in time (30 seconds, tried twice),
is applied on that, which gives its apply nothing to act on; then, once its check answers with
things to act on, again, on those.

## Commands

Built:

```
kit                          At a terminal, kit's menu, at once: what to run next (else help)

This Mac:
kit status [step...]         How this Mac stands against the config: what needs attention
kit apply [step...] [--plan] Install what's declared and missing (--plan: say what, do nothing)   [--restore-from mac|time-machine|arq] [--at]
kit reconcile [<kind>...] [<id>...]  Settle drift: adopt, remove, install, undeclare, leave or snooze
kit nightly [job...]         Run the scheduled jobs due, then every check (the hourly launch)   [--plan]
kit list [kind]              What's declared for this Mac: file, note, installed or not
kit why <name>               Where it's declared, whether it's installed, what needs it
kit log                      What the last run did: every check, every command, with timings
kit machine [<name>]         This Mac's name: shown, or set (one of kit.toml's Macs)
kit machine add <name>       Add a Mac to kit-config, a copy of another's or empty   [--from] [--description]
kit terminal [<name>]        The terminal a new Mac's boot hands over to: shown, or set in kit.toml   [--none]
kit nightly at [<time>]      When the nightly run falls due: shown, or set in kit.toml (nightly_at)
kit setup                    Set up a Mac in use: kit-config made or joined, the Mac declared as it is, then kit apply   [--create|--join] [--mac] [--from] [--restore|--no-restore] [--prefs|--no-prefs] [--link|--no-link] [--adopt|--no-adopt] [--apply|--no-apply]

What kit manages, a command each:
kit <kind> add <name>...     Install, and declare for this Mac   [--shared] [--temp] [--note]
kit <kind> remove <name>...  Uninstall, and undeclare            [--shared] [--zap] [--keep] (apps)
    (brew, cask, mas, npm, composer, gh, claude-mcp, claude-skill, login-item,
     defaults, power, git-config, backup-exclusion, spotlight-exclusion)
kit <kind> <verb> <line>...  A line copied for the kind's program, run as given, then what it installed declared   [--shared] [--note]
    (brew install|uninstall, npm install|uninstall -g, mas install|uninstall,
     gh extension install|remove, composer global require|remove)
kit claude-mcp on|off <name>...  Declare Claude's MCP servers on or off, and install or remove them
kit feature on|off <name>... Switch features on or off for this Mac   [--shared]
kit backup add|remove <tool> The backup tools this Mac uses, each checked   [--after] [--before] [--shared]
kit file add|remove <path>...    Files linked into the home folder from kit-config
kit path add|remove <dir>...     Directories on the PATH
kit restore add <path>; kit restore remove <path>...  The person's data a new Mac carries across   [--skip] [--note] [--shared]
kit secret add <name>        Keep a secret in its store, declare it, put it in place   [--stdin] [--from] [--ref] [--in] [--item] [--field] [--mode] [--shared]
kit secret remove <name>...  Take it off the Mac, undeclare it     [--keep-value] [--delete-value] [--shared]
kit secret sync              Read every secret from its store, and put each in place
kit secret places            The blocks secrets are declared in: name, store, item, file, count
kit prefs capture|restore|history|pending|start-fresh   Apps' settings, saved to a private repository and restored
kit check|hourly add <name> -- <command>, kit nightly add <name> -- <command>   Your own checks and jobs
kit manual add <name> "<what to do>" [-- <command>]; kit manual done <name>...  Steps by hand   [--group]
kit step add <name> "<what it does>"; kit step remove <name>...  Steps of your own   [--after] [--before] [--secret] [--sudo] [--shared]
```

The command shape (the maintainer, 6 Oct): a command about one kind of thing names it first, then what to
do, as in `kit brew add`, `kit secret sync`, `kit manual done`; one about the whole Mac is a verb
alone (`status`, `apply`, `reconcile`, `nightly`, `list`, `why`, `log`, `machine`, `terminal`), a kind named
after it a filter. Each thing's help shows only its own flags and verbs. The help lists the
commands in groups: This Mac, Packages, Settings, Files and secrets, Your own. `kit nightly`
runs jobs by name, so no nightly job is named `add`, `remove` or `at`. Kinds are named as words read
after `kit`: `git-config` (not `git`, read as git's own add), `spotlight-exclusion` and
`backup-exclusion` (not `spotlight`, read as adding to it), `login-item`, `defaults` (the lines
are `defaults write`'s words), `mas` (casks are apps too; package kinds are named after their
tool).

`kit machine add <name>` adds a Mac to kit-config, for a new Mac to pick as it's set up: its
`[macs.<name>]` table in `kit.toml`, after the last Mac's, the file's text otherwise as it was,
and its folder, with `--from <mac>` a copy of that Mac's (every file as it is, steps' scripts
still executable), and `from = "<mac>"` in its table, else empty but for its `declarations`
file; committed and pushed as `kit <kind> add` is. It leaves this Mac's name, the primary and
the settings store as they are, and refuses a name kit.toml knows, one that can't be a Mac's,
and `add`. A `from` naming no Mac counts as none. The boot's "This Mac" offers the same: "new"
asks the name, then where it starts from, fresh (only what every Mac shares) or one of the Macs,
whose copy is made as `--from` makes it, left uncommitted with the new Mac's table, as a new
Mac's git can't commit yet. `kit apply` commits and pushes those alone, as `kit machine add`
would, in its `new-mac` step (the Git row): after git's settings and the steps of the user's
own, which can bring the key git signs with, once git has a name and email, waited for till
the rest of the run is done. Without them, or with a commit git can't sign yet, it's left for
the next apply, saying why in git's words. A push that fails is the next apply's to push. Once
committed and pushed, the boot's record forgets the Mac, and later runs have no such step.

The two settings of kit.toml's own a person changes have commands, so nobody edits kit.toml by
hand (the maintainer, 18 Nov 2026): `kit terminal [<name>]`, shaped as `kit machine` is, the
terminal a new Mac's boot hands over to, one `apps.Terminals` knows, or with `--none` none, the
boot running on in Terminal; and `kit nightly at [<time>]`, `nightly_at`, a time of day as in
04:30, written as HH:MM, shown with 03:00 when kit.toml sets none, said to be the default. Without
a value each shows its setting, `--json` as a document. With one, it edits kit.toml in place: the
setting's line replaced where it stands, its indent, comment and line ending kept, else one added after the last setting
before the first table, the rest as it was; read back with the config's own loader, written
whole, then kit.toml alone committed and pushed as `kit machine add` is, the message the command
run. A value kit.toml says already changes nothing and commits nothing, its step saying so; set
when it was the default, `nightly_at` is written, so the time holds should the default change.

`kit setup` is the front door for a Mac in use, as the boot is for a blank one (the maintainer,
18 Nov 2026): one command, all the way, its steps through the engine, each found done by its
check looking at what's there, the clone and git's settings in it, GitHub and the Mac, its
record in kit's state (`setup.json`) keeping only the choices made, so a run stopped anywhere,
a question cancelled, one its flag doesn't answer, or a clone, a setting or a push cut short,
carries on where it stopped and ends as a run never stopped does. It refuses while the config
folder holds anything but Finder's `.DS_Store`, which it deletes so git clones there, unless its
own run part-way left it there: the folder empty or a clone
of the repository it chose, read as its origin's URL is set, before an insteadOf rewrites it,
and kit's name for the Mac none or the one it chose; else it's a Mac kit looks after. Run
again with a flag other than the run recorded: till the commit's edits are begun, recorded as
they are since the clone can't tell them from the Mac's first files, a question's flag replaces
its answer, and a `--create` (owner/name on the signed-in account) or `--join` naming another
repository deletes setup's own clone and the name it gave the Mac and starts afresh; once
they're begun, either is refused, naming the recorded answer, as files have been moved in and
linked back. A `--mac` or `--from` other than recorded is refused, the Mac's name being written
in kit-config and kit's state. GitHub: GitHub's CLI's sign-in first, else kit's
kept one, else the boot's (its device flow, the phone or the browser), then GitHub's CLI
installed through Homebrew and handed it, as the boot does, the same code. The config:
`--create <name>`, a new repository, private, created through GitHub's CLI as owner/name on
the signed-in account, or taken when it's there, private and empty, as a run cut short after
creating it leaves it; or `--join owner/name`, refused unless GitHub says it's private. At a
terminal with neither, the person's `kit-config` is offered when they have one; else a new one,
one of their private repositories (one call, never looking inside any) or a name typed. Cloned
as the boot clones, single-branch, git signing in through GitHub's CLI, then looked at before
anything's written in it, as git has it: no branch, and GitHub none (`ls-remote --heads`), it's
empty, and kit-config starts on this Mac, as one created does, its clone set on main and
tracking main alone, the remote having no branch till the first push; no branch while GitHub
has some, the clone was cut short, killed before its fetch, and is cloned again, once; holding
a kit.toml, it's joined; else, or holding anything when it was to be created, the clone's
deleted and the choice forgotten. The row says a repository joined empty is private and
empty, one created new and private. This Mac: `--mac`, suggested from its model, as macOS names
it (`MacBook Pro`, `macbook-pro`), named as `kit machine add` names one, recorded before it's
written anywhere; a config starting on this Mac has first files kit.toml (`format`,
`minimum_kit` this kit when it's a release, `primary` this Mac, `terminal` Ghostty only when
setup runs in it, the Mac's table, its model the `description`), and the Mac's folder, its
declarations file empty, no shared folder (made once something's declared for every Mac; kit
puts Homebrew's folders on its PATH itself, so they need no line); joining, one of kit.toml's Macs or a new one, added as `kit machine
add` adds it, `--from` a copy of another. Then the snapshot (`snapshot.Find`, on kit-config's
PATH: a kind's program that isn't there is looked for along the PATH setup was run with, then
the boot's folders (`/usr/local/bin`, Homebrew's and the system's being on kit's PATH already), and the first folder holding it, never kit's own, a
temporary or a relative one, nor one named for a version, as 22.11.0 (refused, saying so, when
it alone holds the program), is declared last in the Mac's `[paths]` as `kit path add` declares
it, its own part; the snapshot reads with kit's PATH as its later runs will have it, those folders
after the Mac's own and before the shared file's, Homebrew's and the system's), a row its counts by part, and its questions, each with its answer
suggested, its flag, and its answer kept in the record: the folders a new Mac brings back
(`--restore`, as `kit restore add` declares them), apps' settings saved nightly (`--prefs`:
the `prefs` feature on, started afresh from this Mac's settings as `kit prefs start-fresh`
does; asked unless this Mac's own record says capture's on, whatever kit-config declares, as a
Mac joining as a copy of one saving them has saved nothing), the shell's and tools' files kept (`--link`, as `kit file
add` moves and links them), the apps installed by hand Homebrew takes over (`--adopt <cask>`,
To Homebrew: a row a cask of those that install them, as `brew.Homebrew` matches them, its apps
named and the cask only when its name says more, all ticked, a cask the Mac declares already
never offered; the ticked declared with the snapshot, so the `kit apply` after setup takes their
apps over, the rest left by hand for good, by their apps' bundle ids, `kit reconcile` never asking of them),
and, joining, what kit-config declares that this Mac hasn't:
installed with the rest (`--apply`) or setup stopping to look first (`--no-apply`); a kind
whose check couldn't tell is asked about too, saying so and why, never taken for nothing to
install; kit-config started on this Mac has nothing to install, its step saying so. Then,
once every URL git pushes the clone to, after any `pushurl` or `pushInsteadOf`, is proven
private (`gitrepo.ProvePrivate`, as the prefs store proves its), one edit and one commit, `snapshot.Write`
and the answers, kit.toml and the Mac's folder, pushed; setup's done only once the push lands.
A commit or push that didn't go says a run again carries on, and never to delete kit-config's
folder part-way, as files are moved into it and linked back: the run again declares only what
isn't, links only what isn't, commits nothing new and pushes what's waiting, to an empty
repository as one, its branch made by the push; what's waiting that can't be told is never
taken for nothing. Till kit-config's own branch is on the remote, the prefs store pushes
nothing, as GitHub makes a new repository's first branch pushed the one a new Mac's boot
clones. It says what's refused and what couldn't be checked, each with why, the apps left installed by hand
(none a cask takes over, never Apple's, the App Store's, a cask's or those Chrome makes for websites, `com.google.Chrome.app.`;
the person's to install through Homebrew or the App Store should kit set the Mac up again),
and that secrets go in a store with `kit secret add`; then `kit apply` runs in the same
process, with its own screen at a terminal. Without a terminal it asks nothing: each question
its flag doesn't answer fails its step, naming the flag and what it chooses from, the others
asked on, and kit exits 2. `--json` is one document: setup's steps, as kit status's document
has a run's, and, when it ran, apply's under `apply`. At a terminal, its keys read there too, it
has a screen of its own,
the boot's counterpart in kit's look (kit-look's round 13), drawn in place and left in the
scrollback (`render.SetupFace`): the wordmark, with setup, the Mac (its model's name till it's
named) and when; SETUP, a row a step, `n of 3` while one's asking; once that's done, DECLARED,
a row a part of the snapshot as `snapshot.Find` tells of it (`Mac.Looks`, emitted as
`event.Finding`: waiting, being read, its count, the features and backup tools by name), the
folders' sizing in Your data's row, then a row a question, To install's only once it asks or
has something to say; each question asked in its row (a choice, a visible typed field, a
checklist of boxes for the folders and the files, scrolling round its cursor), what more it says
on a muted line under it; the commit in kit-config's row, `pushed` kept however narrow, and DECLARED's
note its total; then the apps installed by hand, a row each, where secrets go, and `kit apply
carries on now` before apply's own screen. Signing in, the way chosen takes the whole window, the
QR code or the browser's page, and the code, as the boot's does, the head, then the blank lines,
then the QR code giving way in a short window so the code and its expiry stay whole; esc gives
it back, the code under GitHub's row. Esc, or q but in a typed field, stops it: a step the
engine says the run was stopped before or in, or one skipped for it, says it stopped, rows done
stay as they were, kit-config's never pushed, and nothing's said of the apps installed by hand
or secrets. A checklist scrolls in the room the row and the keys leave, and the keys wrap only
between keys. Bare `kit` at a terminal, its keys too, offers it while setup hasn't finished: a
setup stopped part-way, enter carries it on; the config folder absent or empty, as setup starts
on, enter runs `kit setup`; any other key leaves. Every command that needs a config and finds
none names `kit setup` first.

Every Mac's SSH host key is recorded in its table, `host_key`, its type then the key (a host key
is public), and its local hostname, `local_hostname`, as macOS's LocalHostName has it (`scutil
--get LocalHostName`, without `.local`), for a Mac restored from it to reach it by and know it
by (Data restore). The `host-key` step (the Git row), on every Mac, is ok and quiet while its
`remote-login` feature is off: there's nothing to record. With it on, after Remote Login's step,
it checks this Mac's own key, the first of `/etc/ssh`'s `ssh_host_ed25519_key.pub`, then the
rest, anyone may read, and its local hostname, against kit.toml's: ok when they match; not
recorded, or another (a Mac set up again under its name has a new key, a Mac renamed a new name,
which is right), it needs attention, and `kit apply` records both, committing and pushing
kit.toml, placed as `new-mac` is, after it. macOS makes the keys at Remote Login's first
connection (`ssh.plist` is inetd-style: `sshd-keygen-wrapper` runs for each one), so with none
yet apply connects to port 22 on this Mac once, reading the banner, logging into nothing, then
records the key. It waits for the next apply, saying why, while kit.toml has edits not
committed, as recording commits kit.toml whole: the person's, for `kit reconcile`; this Mac's
table as the boot left it, for `new-mac`, which commits it first in a run. A commit git can't
sign yet puts kit.toml back as it was, for the next apply.

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
`--sudo` uses plain `sudo`; `sudo -n` never asks, so goes without. Only the programs whose sudo
asks are given the way to ask: Homebrew installing and uninstalling, its installer, and a step
declared `--sudo` applying; never everything a run starts, as an npm package's install script.
A step's script waits for its sudo: one left running in the background (`sudo … &`) is cut off
from the script when it exits, and refused. kit answers only its helper, run by the real sudo
(running as root), beneath one of those commands while it runs, a program named as kit is: that
stops a program asking for itself, or running sudo with kit's helper from outside one of those
commands. It can't stop a program that rewrites what kit or Homebrew later run, as the helper's
link in the user's own folder: nothing the user owns is guarded from the user's own programs.
A command stays noted till its output closes, a moment after it exits, a window left as too
unlikely to guard. The person's asked one thing at a time: a program's sudo waits for kit's own
question, the run's time limits waiting with it; the run's end gives up a question still
waiting.

`kit <kind> add` installs each name, unless it's installed, and declares it in this Mac's file, in
its sorted place, or for every Mac with `--shared` (taking it out of each Mac's own file); it
asks nothing about where. `--temp` installs without declaring: quiet for 7 days, then reconcile
asks.
`kit <kind> remove` uninstalls and takes each name out of this Mac's file; one declared for every Mac
needs `--shared`. An app (a cask, an App Store app) leaves its files and settings behind, in case
it comes back, and kit prefs goes on saving its settings: at a terminal, before anything's
uninstalled, kit asks once for each app that leaves any and that removing would uninstall (not
one refused, as declared for every Mac without `--shared`), by its name ("<app>'s settings and
files": `Keep them`, in case you come back, or `Remove them`, its files from this Mac and its saved
settings from kit prefs' store), listing under it, wrapped, exactly what removing takes. A cask's files
are those its zap list (brew info's) names that are on the Mac, a shared vendor folder or another
app's settings included when its globs say so, which is why they're listed; removing them
uninstalls with Homebrew's zap, then `defaults delete`s each preferences domain it removed
(`~/Library/Preferences/<domain>.plist`) that no installed app uses (kit prefs' owner lookup), as
cfprefsd keeps a domain whose file has gone: the next capture drops it from kit prefs' store, whose
history keeps it. An App Store app has no zap list: it's asked about only when kit prefs holds a
domain that's its alone (never one it shares with another app, as a vendor's or its group's), and
removing them deletes those domains, while the app's still there, then uninstalls it; should the
uninstall fail, they're restored (`kit prefs restore <domains>`), held till the app's quit, or
opened once, as a restore of domains named holds them, the error saying so. Settings
that can't be taken off the pending list leave it undeclared all the same, the summary saying
so. `--zap` and `--keep` answer without asking, and never together; without a terminal, they're
kept, the output saying so and that `--zap` removes them. An app leaving nothing is never asked
about; `--zap` on one not installed says it had nothing to remove. The kind knows its app's leftovers (a `Leaver`); kit's
core never knows Homebrew's zap. Adding and removing both handle each name on its own (one
failing leaves the others), then commit the config's changed files, with a message saying what, for which Mac and why, and push to
main, pulling with rebase first. A push that fails leaves the commit, and says so.

**A copied line** (the maintainer, 23 Nov 2026: "copy and paste the command from someone's
GitHub … and then I could just append a kit to the front and it will just work"). `kit brew
install --cask owner/tap/tool` runs `brew install --cask owner/tap/tool` as given, through the
runner, in the program's turn to change things, watched for going quiet, its output live, then
declares each name it installed as `kit <kind> add` would, and commits and pushes once; its
uninstall runs the program's, then undeclares what's gone. A kind takes a line by implementing
`kind.Liner` (`Line` runs a `kind.CopiedLine`: the verb as typed, the arguments, whether it
installs or is a dry run, and the names not yet installed by kind; `Refuses` judges an install
line before anything runs) and describing it in its descriptor's `Help.Line`, a `kind.LineHelp`:
the program, its install and uninstall `kind.LineVerb`s (words, each a command of kit's holding
the next, as gh's `extension install`; the program's aliases for each word; its dry-run flags;
what else the help says), how to find a line's names (`Names`) and which kinds they can be
(`Kinds`: brew's names are formulae or casks, formulae first, as brew takes a name both have).
`internal/cli` builds the commands from it (`addLineCommands`) and runs every line one way
(`runLine`): brew, npm, mas, gh and Composer take one today. The rules, and why:
- The line's flags are the program's and this install's alone; kit-config records names, as
  kit's own add does: npm's version and Composer's constraint as typed, a tap's thing with its
  tap, a cask declared as one when brew installed it so. A file's path or an address can't be
  declared, so it's refused.
- kit's own flags in the line are `--shared` and `--note` (none of these programs has either);
  every other word goes to the program, kit's names included, as brew's `--verbose`, so kit's
  global flags go before the kind's name (`lineAfter`). cobra parses none of the line
  (`DisableFlagParsing`).
- Every name is judged before anything runs, as `kit <kind> add` judges it, the
  program's own refusals with them (`Refuses`: a cask whose app there isn't the cask's; gh's
  extension without its owner), and the administrator's password asked for first: one refusal
  refuses the whole line ("…; nothing was installed"), as a line half-declared would leave
  kit-config differing from what the person meant. A name installed already is declared;
  one that didn't install isn't; a dry run, per verb, declares nothing.
- A plain cask line (`plainFlags` alone: `--cask`, `--casks` and those changing only brew's
  output) gets `--adopt` added, and a replace when Homebrew won't adopt, as every kit cask
  install has (the maintainer's yes, 23 Nov 2026); any other flag may change where or how it
  installs, so the line runs exactly as given.
- An uninstall finds each name by whichever form it's declared (`declaredAs`: brew's short or
  full name, npm's and Composer's with or without the version, an App Store app by id, gh's by
  its command), undeclares only what its own kind no longer has, and needs `--shared` for one
  declared for every Mac, refusing the line whole without it. An app's kept settings go as
  `kit cask remove` handles them: `--zap` in a brew line removes them, a mas line keeps them.
- Known limits: npm's `add`, `remove` and `rm`, and brew's `remove` and `rm`, are kit's own
  commands' names, so such a line reaches kit's add or remove and fails on the program's flags
  (`-g`, `--cask`); `kit mas uninstall` finds each id in the App Store
  first (`Finder`), so an app it no longer has needs `kit mas remove`.
`kit <kind> add` and `remove` stay as they are: the line is for a command copied, add for kit's
own way. No command's name clashes with kit's (`TestNoCommandsNamesClash`).

**Moving a line.** Every command declaring for every Mac takes the name out of each Mac's own
file in the same edit, the line moving as written, all judged before anything's stored,
installed or asked for:
- Each Mac's block must be the same: the line's words (re-quoted, so quoting and spacing don't
  count), its note, the comments above it by their words (one set apart from the line by a
  blank line is the line's, as kit's sorting has it), a step by hand's steps, and its section's
  item, `--as` name and hold (`[homebrew formulae --after cask:x]`). Otherwise it's refused,
  naming each line and what it has, as written: make them the same (for notes, or give one
  with `--note`), or take those lines out first.
- A command taking a line's value (`kit check|hourly|nightly|manual|step add`, backups, a
  restore path) takes the name alone with `--shared` when Macs declare it, moving their line
  as written: `kit manual add hammerspoon --shared` moves its options and steps too.
- A value the command gives, its item and its section's hold must have the same words as the
  block's, otherwise it's refused, naming both lines: give only the name, to move it as
  written; for a value kit reads from this Mac (macOS, power and git settings, MCP servers),
  set it the same here, or edit the line; for a secret, give the same. `-integer 60` and
  `-int 60` differ, as do options in another order.
- Otherwise the block moves as written: its comments, line, steps and section's hold and
  `--as` name. A note or `--as` the command gives wins; a note kit makes up, as a login item's
  app name, is the note only when nothing else gives one. A block name the shared file's block
  for the item doesn't go by (another, or none) is refused, naming both, as the name would be
  lost; one another file gives to a different item is refused with the whole edit, as the
  config wouldn't read with it.
- A step of your own's folder moves with its line: the Macs' folders, all the same, go to the
  shared one, or are left for one there already that's the same.

A name in the shared file already is refused as before. `kit <kind> add` judges each name before anything's installed
or asked for: one refused fails alone, in its row, and the rest carry on.

`kit reconcile` settles drift. At a terminal it goes through each item that needs attention
(`--all`: quiet ones too; name kinds, as in `kit reconcile brew`, for theirs alone), asking
what to do with it: declare it (for this Mac, or every Mac),
uninstall it, install it, undeclare it, snooze it for 7 days, leave an app installed by hand
so for good, skip it, or stop. Every question
comes first; then it does it all, as `kit <kind> add` and
`kit <kind> remove` would, and commits and pushes once. Without a terminal, or with `--json`, it lists
the items with their ids and choices, and `kit reconcile <id>... --adopt` (or `--remove`,
`--install`, `--undeclare`, `--snooze`, `--leave`; `--shared`, `--note`) settles those, every
id checked before anything's done, in one run and one commit: how an agent carries out a
person's decisions.

`kit status` runs every step's check (named steps run with what they need): Homebrew, each
kind, and the config repository being proven private (`config-private`): every URL kit pushes it
to (its one remote's push URLs, as `git push` has them, git's `insteadOf` applied; git failing
to name them fails the check) and origin's, which it's fetched from, each URL once, each that
isn't proven private named. Proof is positive, never detection: a URL is proven private only as
a repository on github.com, written exactly as `https://github.com/<owner>/<name>`,
`git@github.com:<owner>/<name>` or `ssh://git@github.com/<owner>/<name>`, with `.git` or
without, that `gh` says is private, asked by its URL, whatever host `GH_HOST` names (`gh` not
signed in, as it says by exiting 4, fails the check: "gh isn't signed in to GitHub: run gh auth
login"). Anything else is unproven, named without who it signs in as, saying the three forms
kit takes: another host, scheme, port, user or form, a path on this Mac, a helper's URL. With no
remote it's pushed to or fetched from, it isn't public.

Every command takes `--plain` (plain lines, as without a terminal) and `--verbose` (commands'
output kept whole in the run's log), and every one but `kit agent`, whose text is what agents
read, `--json` (one document).

Exit statuses: 0 when kit did its job, even when what it found needs attention (drift, a
to-do, a step by hand, a plan with things to do, a lookup that found nothing), 1 when
something it tried failed (a step's check or apply, an install, a push) or a question was
cancelled, 2 when kit couldn't do its job at all (no config repository, a config error, an
unknown command, a Mac with no name yet). What needs attention is in the output and
`--json`, never the status.

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
state (`nightly.json`). A whole run first asks GitHub for kit's newest release, at most once a
day, at the URL install.sh asks (`releases/latest`; install.sh follows its redirect, kit reads the
tag it redirects to without following it, no token, a 10-second limit), and keeps the answer in
`release.json`; an ask that fails keeps the last one, fails nothing, and is asked again next run,
and a record that won't read counts as none, the next answer replacing it. Nothing upgrades kit by itself (the maintainer, 23 Nov
2026): `kit status` says a release is out, and the person upgrades. It installs and removes nothing. `--plan` says what's due; naming jobs
runs those, now. Its runs notify no one (the maintainer, 12 Nov 2026: "record it so that you
have access to the knowledge, but I don't want to be told"): what they find is in `kit status`,
bare `kit` and the log. The notifications kit
posts are a run's at a terminal: a card waiting, and macOS asking.

**The hourly launch** (slice 16) is a built-in step on every Mac, `hourly-launch`, nothing
declared: a launch agent, `com.leeovery.kit.nightly` in `~/Library/LaunchAgents`, has launchd
run `open -W -g -n` on kit.app every hour on the hour, with `nightly` and the kit on kit's PATH,
named as it's found there (Homebrew's link, not its versioned folder); kit.app runs `kit
nightly --scheduled` as its child, waits, and quits (`open -W` exits 0 whatever the run did, so
nothing reads how it ended: kit's run log has it). macOS holds kit.app responsible for its
child, so Full Disk Access is kit.app's, kept across kit's updates as kit.app's identity is
(**kit.app**, below; the dotfiles' Nightly.app did the same). The agent names that kit
whichever kit applies, so a lasting build of kit's own, as install.sh's `KIT_FROM` leaves in
`~/.local/bin`, names the kit on PATH, not itself; with no kit on PATH, the kit applying. It
comes after Homebrew's formulae, however they went, and finds the kit then, so a first apply
from such a build names Homebrew's kit, installed earlier in the run. A build that won't last
(`go run`'s, a worktree's, a build in a temporary folder) names none, so the hourly run never
runs a kit that may be gone: it leaves the agent as it stands, its check looking at the kit the
agent names, and `kit apply` saying why it left it. Its check:
the agent says that, for the kit and kit.app, the kit it names is there, kit.app is the one this
kit makes, and launchd has it loaded (`launchctl print gui/<uid>/…`); `kit apply` makes
kit.app, writes the agent, loads it (`launchctl bootstrap`, after `bootout` when it was loaded
and changed, asked once more a second later should launchd refuse, as it can while the old job
is torn down), and records when, the first run being up to an hour off. Only the launch's runs
record the hourly run: `kit nightly` by hand records the nightly run, when it's due, never the
hourly one, so it never hides a launch that's stopped.

**kit.app** is built once, at release, on the maintainer's Mac, and shipped in each release's
archive beside kit. press, the maintainer's release tool, reading `.press.toml`, has the tree's
`cmd/kitapp` lay its bundle out (`macapp.Lay`: the `Info.plist` kit embeds, carrying the
release's version, numbers and dots alone; the icon; the fingerprint of the files it's built
from; and the Developer ID team it's signed by), compiles `kit.swift` for arm64 and x86_64
(macOS 12 on, Go's own oldest) and joins them, one app for both archives, so it's notarised
once; signs it with the Developer ID in the
Mac's signing keychain and the hardened runtime, kit itself signed beside it
(`com.leeovery.kit.cli`); sends both to Apple's notary service (the keychain profile
`notarytool`, in the same keychain, which press unlocks itself, so a release asks nothing, at
the Mac or over SSH); staples the ticket to kit.app; and checks it as a Mac will
(`codesign --verify --strict`, `stapler validate`, `spctl` taking a quarantined copy as
notarised), in each archive too. Homebrew's formula puts it in the keg's `libexec`,
install.sh in a `libexec` beside the `bin` it puts kit in; kit finds it there from its own
executable, links followed, an app naming its team (`macapp.Shipped`), never a fixed prefix. A
run that needs kit.app copies that one into kit's data folder, the path the launch opens, aside
with `ditto`, which keeps what codesign sealed, and puts the copy in place once codesign passes
it whole and signed as kit.app by the Developer ID of the team it names (`--verify --strict -R`,
`anchor apple generic` and the leaf's `subject.OU`), so the team kit records is the signature's,
never the file's alone. It copies again only when the copy there names another team or other
files built from than the app shipped, or has no executable: a later release's build of the same
files, its version all that's new, leaves the copy alone, as macOS grants both alike and the
hourly run may be inside it, so `hourly-launch` stays current across such an update. A kit with
none beside it (`go build`'s, the tests', an older archive's) builds kit.app as it always has:
from the files it embeds, with `swiftc` once the command line tools are there, signed ad hoc,
again only when those files change, so a release's build of the same files is kept. What macOS
grants Full Disk Access to is kit.app's identity (`macapp.Identity`): a Developer ID app's grant
follows its designated requirement, bundle identifier and team, so a release's build is known by
its team, every release alike; one built on the Mac is another app to macOS once it's built from
other files, so it's known by their fingerprint.

After it, `kit-app-access` gives kit.app Full Disk Access, a step by hand: a card in `kit apply`
at a terminal, System Settings opened at the list and Finder at kit.app, for the drag (as the
boot's for the terminal), done as the person says. Each scheduled run records whether kit.app
could read what only Full Disk Access shows (nothing, with none of it on the Mac to read), with
kit.app's identity; the step is done while the last word, the run's or the person's, is
granted for the identity of the kit.app there, so one of another (one built on the Mac from
other files, or the first release's build in place of one built on the Mac: another app to
macOS, its grant gone) or found without it needs the card again, and `kit status` says so. `full-disk-access`, with the `prefs` feature or backup exclusions declared, is
the grant of the terminal or SSH session alone, for a capture or restore run there and the
exclusions, placed as `[features]` places prefs only while no exclusions wait for it; the
launch's runs leave it out, their exclusions held while the
run, kit.app's child, finds kit.app without it (nothing there to read holding nothing), a refusal
by tmutil recorded as kit.app not granted. A capture holds a lock
beside the store's clone for its whole run, so two (the launch's and one by hand) never write
the store at once: the second waits, up to 30 minutes.

Built since this list was made: `bootstrap`, `secret` (for `secrets`) and `prefs`. Not built,
and in no plan: `share` (`--shared` does its job), `edit`, `update`, `takeover`, `retire`,
`decisions`, `decide`, `fleet`.

Every question has a flag that answers it, so everything runs without prompts. Without a
terminal kit never prompts: a question it can't ask is an error naming the flag. `--json`
output is one versioned document whose items carry stable ids (`brew:jq`).

## Files

- **The config repository:** `$KIT_CONFIG`, else `$XDG_CONFIG_HOME/kit`, else
  `~/.config/kit`. Never under a folder a data restore fills on a new Mac. The boot clones its
  own branch alone; apps' settings, on its `prefs` branch, have a clone of their own in kit's
  data folder, `~/.local/share/kit/prefs`.
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
Mac's, then the shared file's), `homebrew formulae`, `homebrew casks`, `app store apps`,
`npm packages`, `composer packages`, `github extensions`, `macos login items`,
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
nightly_at = "03:00"   # when the nightly run falls due: 03:00 unless set; kit nightly at sets it
terminal = "ghostty"   # what a new Mac's boot hands over to; kit terminal sets it

[macs.laptop]
description = "MacBook Pro"
host_key = "ssh-ed25519 AAAA…"   # recorded by the host-key step
local_hostname = "Laptop"         # likewise: where a Mac restored from it reaches it

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
  the repository's, a link to one the same, a link into kit-config to a file it no longer
  has, such as one left when the file moved to another scope's home folder, or a file under
  `~/.ssh` or `~/.gnupg` others can read); applying puts it right. A link to nothing outside
  the repository stays diverged.
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

At start-up kit builds its own PATH from the config's `paths`, Homebrew's folders and the
system's directories, ignoring what it inherited, so it behaves the same from a terminal,
launchd or an agent, and finds tools installed partway through a run. The order is the Mac's
own `paths`, then the shared file's, then Homebrew's (`config.HomebrewPaths`,
`/opt/homebrew/bin` and `/opt/homebrew/sbin`, which kit supplies as it does the system's:
Homebrew's installer puts only its bin on the PATH, after `/usr/bin`, and only a terminal's),
then the system's, each folder once, in the place it first appears (`config.PathLists`): a
Homebrew folder a file declares keeps its declared place, so a config declaring them reads as
it did before kit supplied them, and nothing migrates; a folder only one Mac needs, declared in
that Mac's file, wins over Homebrew's, as a version manager's shims must. The one exception to a
thing declared in the shared file or a Mac's, never both: a Mac may declare a folder shared
does, raising it on that Mac (`raises`, on the `paths` section alone); `kit path remove`
without `--shared` then takes it out of the Mac's file alone, lowering it again.

The shell uses the same list, Homebrew's folders included (the system's come from the PATH
the shell inherits), the code review's "one PATH list": the `path` step, on every Mac, writes it
(`~` expanded, colon-separated) to `path` in kit's state directory, and the shell's startup
puts the file's contents ahead of the PATH it inherits, reading it without running anything,
so a config that doesn't read never leaves a shell without its PATH. Relative entries
(`node_modules/.bin`) are kept for the shell and left out of kit's own PATH. A `$`, a colon,
or a `~` that doesn't start the home folder is refused. `kit path add <dir>` puts a
directory last of the Mac's own (`--shared`: last of every Mac's, taking it out of the Macs'
files), `kit path remove <dir>` takes it off; both write the file and commit.

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
  look's round 10): the wordmark, the bar (each thing to do one, as known as the run starts: a
  kind's step as many as its lines declared on, which finding what's missing would take each
  kind's check to know, any other step one; a step counting as many as its counts have done, a
  count of more or fewer things, or back nearer its start, the next after the last; a finished
  one whole, whatever it found to do, so the bar never goes back; a run's start without weights,
  as an older log's, each step one, a running one as far as it says it has got) and what the
  run is doing or waiting for you to do; on the left a row for
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
  click; esc for all), and after a row's a line for each of its steps still to run, live, the
  time's column blank: the waiting mark, its title, what it waits for as the row says. A step
  that's started is never one: checking, it's under way at the foot; applying, its lines are
  in the log; its card up or waiting its turn, the card says it. It's scrolled back and
  followed again, with a step by hand's card above it
  while it's up. In a terminal whose driver says it shows progress on its tab (Ghostty's),
  the run's progress is there too. At the run's end its to-dos take the screen (round 12): the state line counts
  what's left and what's done, then, once they're over, what kit's still finishing, or that it's
  carrying on; the left pane lists
  them, To do and how many, each group's name muted, done ones ticked, the one up a pill, `q`
  stopping there at its foot; on the right the one up is a panel ruled off over the log, as a
  card is: its title and where it is in the list, why, its steps, then how it ends (kit watching,
  and what for; `space`; a choice's keys). In a smaller window, in place under the heading, a light a
  row and `→ make the window at least 100×30 to follow the run`; macOS asking, and a card up with
  `→ a bigger window shows the keys to answer it`; the screen takes over once the window's big
  enough. Once applied, its record takes the screen's place: the wordmark with the bar and the
  tally, then what's left to do (the to-dos at the end first, in their order, each its steps as
  how), what failed, what needs you, and the rows done, under each what of its steps still
  waits, but for what's known to come by itself (a project's MCP server whose folder isn't
  there yet, as its kind marks it: `kit status` says it). Commands aimed at one thing (`add`, `remove`, a step named) are a timeline, a row
  a step as its turn comes, no wordmark. A summary ends a whole-Mac command: what failed,
  needs you or wasn't checked, what's fine, and how long it took. `kit log` shows a run as a
  timeline; kit's help and its errors take the look too (an error is a row, what to run about
  it under it: `→ did you mean kit brew add jq`);
- **plain** without one, or with `--plain`: the same words, one item a line, no colour, no
  animation, never a prompt;
- **json** with `--json`: one document at the end, `"schema": 1`;
- **the log**, always: every event as a JSON line. A command's output is capped at 64 KB a
  stream, its full size noted; `--verbose` keeps it whole.

`kit status` exits 0 when it checked, even when something needs attention, 1 when a check
failed, and 2 when it couldn't do its job, as every command does.

## Homebrew

Formulae and casks are compared by full name: `owner/tap/name` for a tap's, the plain name for
Homebrew's own, so a tap's `php` and Homebrew's own `php` are never taken for each other. A
declared name that matches nothing is looked up (`brew info`, all at once; one at a time when
one is unknown, as brew then answers nothing) to resolve aliases and renames before it's
called missing, and a name Homebrew doesn't know is missing and "unknown". Five states:

- **ok:** declared and installed (installed as another formula's dependency counts);
- **missing:** declared, not installed;
- **extra:** installed on request, needed by nothing installed, not declared;
- **unused dependency:** a formula installed as a dependency that nothing installed needs now
  (what `brew autoremove` removes);
- **by hand:** an app installed by hand that a cask of Homebrew's own installs, the cask not
  declared for the Mac (below).

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

**Apps installed by hand.** Homebrew 7 refuses `--eval-all`, so kit reads the catalogue in
Homebrew's cache (`api/internal/packages.<arch>_<macos>.jws.json.payload`, Homebrew's internal
format): each cask of Homebrew's own, its apps' places in /Applications (or the app a package
cask deletes, or the one it alone quits) and the bundle ids it states (what its uninstall quits,
signals or unloads, and the `~/Library` paths it zaps). An app installed by hand has a cask
only when it's at that cask's place and the cask states its bundle id: a fork under the app's
name, or a cask stating no id, has none, erring towards no offer; a disabled cask is never
one. An app's version is its
`CFBundleShortVersionString`, or its `CFBundleVersion` when the short one doesn't start with a
number (Build 4215 is 4215; `apps.Version`); one that still can't be read gets no offer, but
from a `latest` cask that updates itself with no numbered or LTS line at its place. A version
without a dot (a build's, as Sublime Text's 4215) has no major (`major`), and is never told
against a dotted version ("can't tell"): beside another build number, at or above the cask's
it's taken (`atOrAbove`); below, no offer, and the install refuses it ("…, and can't tell
whether this one is"), so an older build fails `kit apply` of its declared cask till the app
updates past it, a known limit. Of several stating it, a prerelease app's, with a channel at
its place, is the channel nearest its version at its major (`prereleaseOf`: `1password@beta`,
`firefox@beta`, `thaw@beta`), never the cask of releases; with none at its major, or two as
near, none. A version is a prerelease's when a channel's word (`marks`: alpha, beta, rc, tip,
snapshot, eap, daily and the rest, with pre and insider) stands in it as a word of its own, or
as in 3.0b2. A channel whose version has no major (`ghostty@tip`, `sublime-text@dev`) is at no
app's major, so a prerelease beside it gets no offer, a known limit.
Else, when the one without `@` updates itself (`lineOf`): that one for an app of its major version or
later (such an app runs ahead of the catalogue, as Dropbox of dropbox); for an earlier major,
the `@` line of that major (`bbedit@14`, `firefox@esr`), never a channel's (`channels`: beta,
nightly and the like), or none, as the plain cask would declare a major version the person may
have no licence for; a `latest` one only with no numbered or LTS `@` line at its place (a
channel doesn't count). Else the one whose
version shares the most leading numbers with the app's (`blender@lts` for Blender's LTS), a tie to the one without `@`,
two without `@` none. The offer runs the install's own check (`match` calls `takesOver`), so
nothing is offered that kit's install would refuse, as a cask of `latest` that doesn't update
itself; Homebrew may yet refuse it. A cask that moves anything besides apps into place (a suite, a font, a Quick Look
plugin) is never matched, nor vetted when installed. Every vetted cask installs with `brew
install --cask --adopt`, Homebrew taking its app there over in place (an older copy kit
replaces, below): for a cask that
auto-updates, unchecked, so kit checks it first (`versioned`, `adoptable`), as the offer does,
in `kit apply` of a declared cask too: only an app of the cask's major version or later, a
newer copy included; an earlier major fails the cask, naming its line if one is ("BBEdit.app
is 14.6.2 and the cask's is 16.1: kit takes over only a copy of the cask's major version or
later, and bbedit@14 is this one's line", `notAdopting`); a prerelease beside a channel at the
cask's place goes only to the channel, any other failing ("Thaw.app is 3.0.0-beta.7 and the
cask's is 2.1.0: kit takes over a prerelease only by its channel's cask, and thaw@beta is this
one's", "none here is at its major version", or, tied, "lamp@beta and lamp@nightly are as near
it", `notChannel`); a line (`lineToken`: an `@` cask not a channel's, as `bbedit@14` or
`firefox@esr`) takes over only a copy of its own line, as `pick` would give it ("BBEdit.app is
16.1 and the cask's is 14.6.9: kit takes over by a line only a copy of that line, and bbedit is
this one's", or "no cask here is this one's", `notLine`); for any other, Homebrew's own check,
the versions must match. A cask that installs through a package is checked as a replace is
before its installer runs over the app, whether it updates itself or not. One Homebrew didn't adopt, as the app differs from
the download, is installed again alone with `--force`, which deletes the app and moves the
cask's in (`--adopt --force` raises before force is read), but only for the same version (of
another build) or an older copy of the same major version: both versions dotted numbers (the
cask's before any comma), the app's lower, its first number the same. Another major version may be kept on purpose (an older line,
an LTS), a newer copy or a prerelease is ahead of the cask, and `latest` or no version says
nothing, so each fails the cask saying why, `whyNot` adding what it can ("Charles.app is 4.6.8
and the cask's is 5.2.3: kit replaces only an older copy of the same major version"). Before either, an app at the cask's place whose bundle
id it doesn't state, or the App Store's copy, isn't touched: the cask fails, saying so
("Spaceman.app is already in /Applications and isn't the cask's (<id>)"). `kit apply` puts
`couldn't install <cask>:` before a refusal; `kit cask add` and `kit reconcile`, `couldn't
install:`. Taps' casks, casks
the catalogue lacks, and every cask when it can't be read install as before, and none is
offered. The casks' check reports each app installed by hand whose cask isn't declared for the
Mac as `cask:<token>`, `by-hand`, "installed by hand as <App>.app", installed casks' apps read
from their install receipts in the Caskroom, as `brew info` takes a second or more, on every
check.
`kit reconcile` adopts one (installs, then declares, as `kit cask add` does: a failed install
declares nothing), or leaves it by hand, a leave that `--all` never asks about and status counts
as quiet, lasting while the app is installed by hand, matched or not: it ends once the app's
gone from the app folders or a cask has it.

**Drift counts after a day.** An item a kind's check finds (missing, extra, an unused
dependency) is new for its first 24 hours: shown, quiet, without needing attention, so a
throwaway install that's soon removed never does. kit remembers when it first saw each item,
the snoozes (7 days) and the temporary installs (quiet for 7 days) in
`$XDG_STATE_HOME/kit/drift.json`, written whole under a lock; it forgets an item once a check
of its kind no longer finds it. What's installed by hand is kept beside it, in `by-hand.json`,
which a kit from before it, writing drift.json whole, never touches: when each such item was
first seen and its snooze, by kind and app, and the apps left by hand (quiet for good, this
Mac's alone), by bundle id, so a cask renamed in the catalogue keeps its app's leave, as does
an app that updates itself past its cask, matching it no more for a while. Those are
forgotten only on a look at what's installed by hand that went well, once the app is
installed by hand no more, gone or a cask's, the look seeing every app installed by hand,
matched or not; a leave too once answered otherwise; a look that fails keeps them, the rest of its
kind forgotten as ever, and status says, greyed, that kit can't tell which apps are installed
by hand, and why. What applying was given to act on and left still to act on, as
a formula whose install ended well but isn't there, is never quiet, whatever its age: its step
needs attention, and the next apply tries it again.

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
| `internal/kind/<kind>` | A kind each, with its descriptor: `brew` (Homebrew's formulae and casks), `appstore`, `devtool` (npm's and Composer's global packages and the GitHub CLI's extensions, one table each), `login`, `mcp`, `skill`, `secret`, `gitconfig`, `defaults`, `power`, `exclusion`, `spotlight` |
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
| 5 | Full Disk Access for the terminal kit runs in, checked before anything that needs it | `boot.TestFullDiskAccess`, `steps.TestFullDiskAccess` |
| 6 | Each secrets store signed in, with a session open, before its secrets | `cli.TestOrderPlacesTheSignInBeforeTheSecrets`, `secret.TestEachStoreUsedBringsItsSignIn` |
| 7 | GitHub reached through the boot's sign-in on the phone, over HTTPS; an SSH key is the config's own step | `boot.TestGitHubSignsInAndReads` |
| 8 | A secret that fails to fetch keeps its previous value; a partial fetch never shrinks the file | `secret.TestSyncHoldsBackWhatOneReadDoesnt` |
| 9 | Settings restore and capture wait for their store | `prefs.TestARestoreWaitsForACapture`; the store a git repository, not a synced folder |
| 10 | Notifications allowed as a step, with a check, while someone's at the screen | not kit's: a step done by hand where the config wants one |
| 11 | Data restore works on a different Mac, at the right moment | `cli.TestApplyRestoresFromTheMacItStartedFrom`, `restore.TestJudgeOnceTheRestoreHasSettled` |
| 12 | Steps declare their Macs: no step asks whether it applies | a folder per Mac, and `--shared` (milestone 9) |
| 13 | Heavy, independent steps run last or alongside, never blocking quick ones | `engine.TestIndependentChecksRunSideBySide` |
| 14 | Nothing needs Go installed: a new Mac downloads a release | `install.sh`, proven from a blank Mac in the bootstrap's runs |
| 15 | The network check is an HTTPS request, not a ping | `boot.TestSelfTest` |

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
any kind; App Store apps; npm and Composer packages; GitHub CLI extensions; login items
(#21–#26); Go tools and tmux plugins, cut in slice 18 as unused.

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
(#43); `--alerts`, drift's daily digest and the report (#44): the alerts and the digest taken
out in slice 16, as kit's runs notify no one.

**9. Steps — built.** A folder per Mac in the config repository; linked files; the
config repository's own edits settled by `kit reconcile`; the shell and PATH, git, macOS
settings and backup exclusions as kinds, secrets, what a person must do by hand; commands
adding lines to `[checks]`, `[hourly]` and `[nightly]`; commands naming the kind first.

**10. Apps' settings — built.** `kit prefs`, the dotfiles' prefsync in Go: its store on
kit-config's `prefs` branch (a repository of its own till slice 22), below.

**11. The bootstrap — built.** `install.sh` and `kit bootstrap`: GitHub's sign-in on the
phone, which Mac it is, Homebrew, the terminal the config names, kit-config, then kit itself
through Homebrew (`leeovery/tools/kit`) once kit-config's files are linked, as Homebrew writes
its trust of a tap's formula into `~/.homebrew/trust.json`, which kit-config may link; then
`kit apply`, from Homebrew's kit, on a screen of its own, with cards for what a person does by
hand; proven from a blank Mac in a virtual machine. Run in that terminal already, the boot
carries on there as Homebrew's kit, becoming it. install.sh runs its download from a temporary
folder, removed once the boot has handed over or ended, so nothing's left but Homebrew's kit; a
build of kit's own (`KIT_FROM`) goes in `~/.local/bin` and runs throughout, the boot installing
no kit. The config is `--config owner/name`, else the one a boot before chose, else the
person's `kit-config`; the install line gives no flags, so with none of those there (the
maintainer, 18 Nov 2026), at a terminal, GitHub's row asks, the same question and code as `kit
setup`'s (`boot.Account.ChooseRepo`): one of their private repositories, listed in one call and
never looked inside, one named as owner/name, or one created, private, on the signed-in account,
`kit-config` suggested. One not there, public, holding no kit.toml (its fetched copy removed),
or holding files when it was to be created is refused before anything's written, and the
question asked again, saying why. One empty (GitHub lists no branch), or to be created and not
there yet, starts kit-config on this Mac: the boot asks the terminal the Mac uses (one
`apps.Terminals` knows, or macOS's own; without a terminal to ask at, none, as no flag answers
it, kit-config's row then naming `kit terminal <name>`), plans the boot order from it, and names
the Mac without a list; the kit-config step creates the repository through GitHub's CLI unless it's there,
clones it, sets it on main as setup does (`boot.OnMain`), writes `config.Start`'s first files
(this Mac the primary, that terminal, the Mac's folder with an empty declarations file, no
shared folder), commits them, as the GitHub account (its name, its private address) when git has no author
yet, and pushes once every push URL is proven private (`boot.ProvenPrivate`, shared with
setup), its branch made by the push (`BranchMayBeMissing`). The record (`fresh`) keeps the
choice, so a boot run again asks nothing again; once pushed, it's a config as any other.
Without a terminal it stops, naming `--config`: a new config is made at a terminal, or with
`kit setup`.

**12. Data restore — built.** `[restore]`, copied from the Mac a new one starts from, over SSH
and pinned to its host key, else from its backups; the boot its one trigger.

**13. kit by itself — built.** An hourly launch through kit's own app; nothing alerts.

**14. The whole-codebase review — done.** Its bugs fixed, and what cost more than it gave cut.

**15. Agents — built.** `kit agent`'s guide, `--json` from every command an agent reads.

**Then:** the Mac mini onto kit; Claude Code's skills, through their installer, when they're
wanted.

## Apps' settings (`kit prefs`)

Every preferences domain that isn't Apple's or the system's (no dot in its name, or `com.apple.`,
`group.com.apple.`, `systemgroup.`, `.GlobalPreferences`, `org.cups.`), through `defaults export`
and `import` (cfprefsd's view; a plist copied behind its back half-sticks), and the settings files
`[prefs files]` names (folders taken whole, dot-files and all but Finder's `.DS_Store`; globs
matching each part as rsync's wildmatch does, as a data restore reads them: a wildcard matches a
hidden name too). Each Mac saves into its own folder of one git branch, kit-config's `prefs`, in
prefsync's format byte for byte: `<mac>/domains/<domain>.plist`, sorted XML as Python's plistlib
writes it, the keys that change by themselves stripped (window frames, list views, file dialogs,
Sparkle's check times); `<mac>/files/` mirroring each file's path under the home folder (`_root/`
outside it); `<mac>/owner`, the hardware UUID, model and name (`scutil --get ComputerName`) of the
Mac the folder belongs to.

The branch lives on kit-config's own remote, the URL kit-config's clone fetches from (its one
remote), so whatever reaches kit-config reaches the store, and the `prefs`
feature alone turns it on: nothing in `kit.toml` (a `prefs_repo` left there from when the store
was a repository of its own is refused, saying to take the line out). kit clones the branch alone
into its data folder (`~/.local/share/kit/prefs`), aside then moved into place under a lock, a
clone of one branch that fetches only it, given the URL kit-config's remote has set, before git's
`insteadOf` and `pushInsteadOf` rewrite it, so those rewrite both clones alike. It reaches the
remote as kit-config's clone does: its
clone's own `credential.*` settings (the boot's GitHub sign-in) and its remote's `pushurl`s go with
each git that reaches the remote as it clones (`ls-remote` asked for 20 seconds at most), and stay
in the store clone's settings, the push URLs as `origin`'s. The boot clones kit-config the same
way, its own branch alone, so the store's history never lands in a kit-config the boot cloned; a
clone that fetches every branch, made otherwise, fetches `prefs` too. A remote with no `prefs`
branch yet, before any Mac's first capture, gets a clone that's empty, on the branch and tracking
it, and that capture's push makes the branch. A clone with no commit (no branch, as git lists
them) is cloned again once the remote has the branch, by a capture, a restore or a look at the
store, as the settings to-do's or the settings step's before it starts afresh by itself, each
holding the lock on the store: the branch is cloned aside while the empty clone stays in place, so
a clone that fails leaves it. No commit is lost, though a file written there and never committed,
as by a capture whose commit failed, is, and the next capture writes it again from the Mac. Git
gives a branch with no commit no upstream, so a pull would never bring another Mac's first capture
in, and a new Mac booted before any Mac's first capture would never restore, or would start afresh
by itself while another Mac's save was on the branch. That costs one `ls-remote` a look, only while
the Mac is paused with an empty clone. Offline, or with the branch still to be made, the empty
clone stays, with no saves and no history, and a capture carries on in it, its commit waiting to
be pushed. A clone in the store's place whose fetch URL isn't kit-config's, as git takes both,
whose push URLs aren't kit-config's (each as `git push` has them, `pushInsteadOf` applied), or
that's on another branch, as one of the repository the store was in before, empty or not, is
refused, naming the clone, where it points and where it should; nothing's captured into it,
restored from it, read for history or an app's saved settings, or taken over, and kit moves
nothing: the person moves it aside, for kit to clone the branch there, and the new clone fetches
and pushes as kit-config does. The store's branch must be set to track origin's prefs, as every
clone kit makes is: one set to track another, as `git branch -m main prefs` leaves a clone of
kit-config still set to track main, which a pull would rebase the store onto and a push send it
to, or set to track nothing, which a push would publish whole as prefs, is refused the same way:
move it aside, or set both `branch.prefs.remote origin` and `branch.prefs.merge
refs/heads/prefs`. Its settings are read, not git's upstream, which git shows as none when no
fetch refspec maps the branch set (a push would then publish kit-config's history as prefs) and
for a branch with no commit; the push reads them again. One off
its branch part way through a rebase, as one cut short, is refused as the rebase under way,
since it holds commits not yet pushed, never as a clone to move aside. Removing an app with a
refused clone uninstalls it all the same, deleting none of its saved settings and saying why.

- **`kit prefs capture`** (nightly, as the `prefs` feature's job): needs capture switched on (a
  full restore, or `kit prefs start-fresh`: a rebuilt Mac never captures defaults over a good
  store), Full Disk Access (without it sandboxed apps export as empty), and this Mac owning its
  folder (claimed by the first capture; another Mac, such as a replacement given the same name,
  is refused until it takes ownership). It pulls, writes only what changed, removes what's gone
  from the Mac (history keeps it), leaves domains waiting to be restored alone, keeps a domain
  that fails to export, commits once in prefsync's message format, and pushes, only when every
  URL the push goes to (its one remote's push URLs, as `git push` has them) is proven
  private, as `config-private` proves it; any other, or a clone with more than one remote,
  holds the push, saying why. A push that fails waits for the next.
- **`kit prefs restore [<domain>...]`** (`--from`, `--at`, `--as`, `--pending`, `--dry-run`): a
  domain's app found from the installed apps' bundle ids (sub-domains, shared domains, group and
  team prefixes, `[prefs apps]` for the rest); not installed or running, it waits; a sandboxed
  app (its entitlements, as codesign reads them, turning `com.apple.security.app-sandbox` on: an
  app can declare it off, and has no container) with no container yet waits for the person to
  open it, kit opening nothing. A windowless helper (an app inside another's bundle whose
  Info.plist sets `LSUIElement` or `LSBackgroundOnly`, as login items do: no window or Dock icon
  to quit it by) is the exception, every restore but a dry run readying it itself once no app of
  its own sharing the domain is running: quit if running (`quit app id`), and, sandboxed with no
  container, opened out of view (`open -g`) till its container comes, then quit, each wait
  bounded (5 s to quit, 15 s for the container); one kit quit that was running is started again
  out of view once its settings are back. One that won't quit, or whose container doesn't come
  in time, waits as an app that's running or not installed does, never as one to open, for the
  next pass to try again. Menu-bar apps of their own are not helpers: they're the person's to
  quit. Then the
  stored copy checked, the live
  settings saved, `defaults delete` then `import`, read back and compared. A full restore puts
  back the files that differ (each saved first), writes the pending list, records its commit, and
  switches capture on, failures or not. What it couldn't restore is held on the pending list,
  each with why, and capture leaves it alone: a domain whose app isn't installed, "kept till its
  app is installed" (never counted as waiting or as anything to do, no card: many such apps are
  never coming, the config not installing them; the summary says "12 kept for apps not
  installed"); one whose app is running; one whose sandboxed app has no settings folder, for the
  person to open once (a to-do, below); and a domain or file that failed otherwise, named with why
  in plain words. A restore of domains named holds those it couldn't restore as a full one does,
  those waiting for their apps or for the person to open one, while those that failed otherwise
  fail alone, unless held already; but not when what's held is from another Mac than the one
  restored from. The hourly pass
  (`--pending`, unattended) brings back a held domain once its app is installed, not running
  and, sandboxed, has its settings folder (one without stays for the person to open), and leaves
  what failed otherwise alone, and does nothing at all, not even a pull, while nothing held could
  come back (all of it kept for apps still not installed, or failed). `kit cask add` and `kit mas
  add`, straight after an install, when kit prefs is on, restore the domains kept for that app
  alone (the app that wasn't there before the install), before it's first opened, saying what
  became of them (restored; to come back once it's been opened and quit, or once it's quit); kit
  apply, its settings step applied after the kinds of apps the run has, whatever its check finds,
  runs the hourly pass's restore once a domain kept has its app; `kit prefs restore --pending`, by hand, tries everything waiting,
  failed or not. From another Mac's folder, or onto other hardware,
  `[prefs machine-bound]` stays behind (Tailscale's identity and the like). `--at` unpacks the
  folder as it was then from history.
- **`kit prefs history`**, **`pending [--clear]`** (what's held, each with why), **`start-fresh`**.
  A sandboxed app held for its settings folder is a to-do of its own, one at a time (`Open <app>
  and quit it`): kit opens it in view (`open -b`) as its card goes up, once a run; `space` restores its
  settings then and there once it's quit and has its folder, else the log says which it isn't and
  the card stays; `s` leaves it to the hourly pass. A windowless helper never gets a card: kit
  opens and quits it itself, as restore says. What failed
  otherwise is said by the check, apps' settings and settings files apart, as held for `kit prefs
  restore --pending`, after anything wrong with capture itself. A held
  file's saved copy stays in the store, on the Mac or not. `[prefs deny]` leaves
  domains out.

## Data restore (`[restore]`)

The person's data, which only backups hold, back on a new Mac. A Mac's `[restore]` lists it, a
path in the home folder a line, a folder or a file, with `--skip` naming what in it to leave out
(relative to it, a path or a pattern as rsync takes one); on when it has lines. Only what's
listed is carried, never the old Mac whole: kit rebuilds the rest. `kit restore add` and `kit
restore remove` change the list, committing and pushing as every list's commands do; add
refuses a line the restore would refuse, before writing anything, and copies nothing.

- **One owner per path.** Every path in the home folder has one owner, chosen by who writes it
  and what it is, asked in order: what the person writes is a linked file; a credential, kit's
  secrets; an app's settings, how it behaves, prefs; the person's data (code, history, ledgers,
  a tool's state), the restore list; anything else, nothing, rebuilt by kit or a cache. So no
  two mechanisms hold one file. In a folder that mixes them each path keeps its owner: the
  restore never carries, and never counts toward a path's holding anything, what kit links,
  what prefs saves (while it's on for the Mac), what a kind puts in place, kit's own folders and
  what the backups leave out; nor Finder's `.DS_Store` and `.localized`. kit works that out,
  never a `--skip`, which is for what in a listed folder is neither the person's data nor kit's
  (a tool's own credentials, made again by its own setup). A line whose path is one of those,
  or in one, is refused, as are two lines where one is or holds the other, case aside, however
  either is spelt. The list decides what's carried by itself, not
  what's safe: Time Machine and Arq hold the whole home folder, so a path missing from it costs
  a copy when noticed, never data, and there's no drift watch over data. An agent building a
  tool that keeps data adds its line then (`kit agent`'s guide asks the question).
- **When: the boot is the one trigger.** A restore starts only on a Mac the boot has just
  named, on a Mac kit had no name for (a new install), which records that it set the Mac up,
  in one of two cases: a new Mac started from another (`from` in kit.toml, written by the
  boot's "start from a Mac" or `kit machine add --from`), whose copied declarations carry the
  list, copied from that Mac, else its backups; or a Mac clean-installed under its own name
  (the boot naming a Mac kit-config already has, with no `from`), restored from its own
  backups, there being no old Mac: the card asks at once for its Time Machine disk or Arq,
  with no Mac looked for, or to restore nothing, the answer for a Mac in use brought onto kit
  by its boot under its own name, or one starting afresh, which the boot can't tell from a
  clean install (each path recorded not restored, never begun, nothing set aside, and the
  restore settles); the card names the listed paths whose files a backup would set aside. A
  new Mac with nothing to come from (a new name, no `from`) restores
  nothing, and a Mac in use, which never runs the boot, restores nothing, whatever its `from`
  says. A Mac rebuilt under its own name while its `from` is still in kit-config restores from
  that Mac. A Mac in set-up whose `[restore]` lists nothing still has the step, fine with
  nothing listed, which settles at its first `kit apply`: set-up's window closes whatever the
  list holds, so a line added later waits for `--restore-from`, never setting aside what's
  there. Once the restore settles it leaves `kit apply` and `kit status`, no
  row, as the reinstall does, coming back only for a path part-way or `--restore-from`. That
  flag is an override, on any Mac: a particular backup or date, a change of source part-way, a
  line added later or one restored nothing (`time-machine` or `arq`; `mac`, the Mac in `from`,
  on a Mac that has one), and then only a path absent or holding nothing but kit's: one
  holding data is here, never touched, though one recorded here is restored once it's absent
  again; `--at` picks the backup, the latest at or before a date or time.
- **Sources, best first.** The old Mac, over SSH on the local network, while it answers (reached
  at the local hostname kit-config records for it, `local_hostname`, then `.local`, confirmed by
  the host key kit-config records, `host_key`, ssh trusting that key alone, never one on first
  sight: a Mac presenting another isn't it, whatever it says its name is; and by its kit name;
  logged into with the key kit's secrets write); its Time Machine disk, the backup's snapshot
  mounted read-only; Arq, restored by the person in Arq's app into a holding folder kit names,
  then put in place by kit and compared as from Time Machine, the holding folder deleted after.
  When the old Mac doesn't answer, or kit-config records no host key or local hostname for it,
  or one that isn't one (it isn't reached: `kit apply` on it records them), a card offers its
  Time Machine disk, Arq, or trying again, which first fetches kit-config and takes the old
  Mac's host key and local hostname from the remote's kit.toml, merging nothing, as `kit apply`
  on it may have recorded them, or new ones (a Mac renamed), since the boot cloned kit-config (a
  fetch that fails keeps the record kit has, saying so in the log, while that's whole); the
  card's words say what puts it right: `kit apply` on it, or, where macOS may be asking whether
  the terminal can find devices on local networks, Allow; then trying again; without a terminal,
  the failure says to run `kit apply` on it, then here. A Mac rebuilt under its own name has no
  old Mac: its card offers its own Time Machine disk, Arq, or restoring nothing, looking for the
  disks again while there's none to choose or one's locked, and Arq's card names this Mac's
  backup; without a terminal it fails, naming `kit apply --restore-from time-machine|arq`. A
  Time Machine disk is the person's while its latest backup holds a home folder of the same
  username, whichever Mac took it. What comes after the restore waits. A path stopped part-way
  carries on from the source it began from, each from its own; but the source the person names,
  by `--restore-from` (and `--at`) or on the card, stands in for every path part-way in that
  run, whatever it began from, each carrying on from it as its own copy begins (`--update` never
  copies over newer work, and the second pass passes over a newer file kept), the record and the
  log saying so: one it hasn't fails, keeping the source it began from. A path a source hasn't
  is recorded not there only if its copy never began; part-way, it fails, naming the ways on:
  another source, by `--restore-from`, or `kit restore remove` dropping its line, as a source
  that won't open names them for every path it was to restore (a wait, or kit stopped, names
  none). Arq's restore comes last in a run, taking in the paths sent to it on the card, so its
  card goes up once. A holding folder of Arq's still empty once the person says Arq's done is
  never a path the backup hasn't: the card comes back naming it, to look again, or for the
  person to say the backup hasn't it, those still empty as they say so looked at again; one
  holding another path stays on the card till it's moved into its own, or emptied, and one kept
  keeps its number. Without a terminal, those paths fail, saying what to restore where, and the
  rest are copied.
- **The copy.** Homebrew's rsync, rsync 3 on both Macs (macOS's own keeps no extended
  attributes), keeping attributes, permissions and dates; never `--delete`, and `--update`, so a
  file edited on the new Mac is never replaced by the old copy. Sockets, pipes and devices are
  never copied: they're a running program's, never data, and a live old Mac's state folders hold
  them (switchboard's and Valet's sockets). From the old Mac it leaves out
  what the backups leave out (folders marked excluded, `[backup exclusions]`), but a marked
  folder holding git checkouts, agents' worktrees, is copied whole. Needs: the source
  reachable, free space for the copy, the same username (tools key their data by full path).
  Then a second pass comparing checksums: from a backup it must find nothing; from a live Mac
  what changed during the copy is copied again, and counted. Files the copy kept as newer here
  (rsync names each), as a shell's history every tab writes, or a tool's state, between a
  path's setting aside and the copy reaching them, are set aside and copied again, those
  alone, once, before the second pass: only in set-up's window, before its copy first
  started, never one resumed; any other is kept, as newer work always is, the log saying so.
  rsync's own errors fail the step.
- **The judgement, per path, before its copy.** Recorded done: never restored again; its copy
  begun: carried on, never judged again, what's in it the copy's. Absent, or holding only
  what's kit's: restored. Holding anything else in set-up's window, whatever its age (a
  shell's history from a Terminal opened before the boot, a download): all of it is set aside
  into kit's state, its path kept, a folder for each setting aside, never deleted, and the
  path is restored. Out of the window: here, never touched, never merged into. The window is
  the Mac's state, never its files' times: open from the boot naming a Mac that has a `from`,
  or that kit-config has, till the restore first settles, every path done or here, and only to
  the paths the restore's first run listed, before their copies start; a Mac in use has none,
  and a line added later never moves a Mac's data. A path its source hasn't waits, judged
  afresh each run.
- **The record**, in kit's state: the source a copy last started from, so a run again resumes
  from it (recorded only as a copy starts, so a source chosen that copied nothing is never
  kept), and each path's own, changed only as its own copy begins; the first run's paths; each
  path's state as it goes (copying, checking, done, here, waiting for its source, its holding
  folder of Arq's), with its source, snapshot or time, counts, when, and the second pass's
  result; and when the restore first settled.
- **Order.** The restore waits for Homebrew's rsync and, copying from the old Mac, the secrets;
  what writes into listed paths (Claude Code's MCP servers and skills), steps declared
  `--after restore` and the reinstall come after it. Linked files and prefs needn't wait, as
  it never carries what's theirs. Everything else runs alongside, so the long copy blocks
  nothing quick.
- **The reinstall** (the `reinstall` step), after the restore, the kinds that install projects'
  tools, and the secrets: each project kit restored has its dependencies installed again from
  its lockfile (Composer, npm, pnpm, Yarn, Bun, uv), in main checkouts and the person's own
  worktrees beside them, never agents' (in `.claude/worktrees`, copied whole) or submodules. A
  project whose tool isn't installed is listed, not failed; one that fails is named, and tried
  again only when the run names the step (`kit apply reinstall`). Yarn Berry projects are the
  person's to install; Terraform, Swift, Go and Cargo are left alone.

## Open-source hygiene

- No personal data in the repo or its history, ever (see House rules in `CLAUDE.md`);
  `scripts/personal-data-scan` checks the files and every commit, in `scripts/gates`.
- Public, under the MIT licence. Released by the maintainer with mint, whose hooks build the
  binaries and kit.app, signed by his Developer ID and notarised (**kit.app**, above), on the
  Mac before the tag, then upload them to the release and update the Homebrew tap, through
  press, the maintainer's release tool (`.press.toml`; `press prepare`, `press upload`).
