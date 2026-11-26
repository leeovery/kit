# Glossary: kit's words, defined once

Each term here has one meaning, and the docs use it in that sense only.

## kit-config and what's in it

**kit-config.** The private git repository that declares what each of your Macs should have.
kit keeps its clone in `~/.config/kit`, or `$XDG_CONFIG_HOME/kit`, unless `KIT_CONFIG` names
another folder. It holds
`kit.toml` and a folder for each Mac. Once you declare something for every Mac, it holds the
`shared` folder too.

**kit.toml.** The file at the top of kit-config that names your Macs. It also names the terminal
a new Mac installs and when nightly jobs run.

**Declaration.** A line that says a Mac should have something: a package, a setting, a secret,
a step. A note may follow it, after `#`. kit's `add` commands write declarations, and you can
write one by hand.

**Declarations file.** The file of declarations in each folder of kit-config, such as
`laptop/declarations` for the Mac named `laptop`. It's made of sections, each a header in
brackets, such as `[homebrew casks]`, then a line for each thing. kit keeps the lists of
packages, settings and secrets sorted, all but `[claude mcp]`. The rest, such as `[paths]` and
`[steps]`, keep the order you wrote.

**`shared`.** The folder of kit-config whose declarations file is for every Mac. A Mac has what
`shared/declarations` declares, and what its own file declares. `--shared` on an `add` command
puts the line in the shared file. kit makes the folder the first time you declare something
with `--shared`.

**Held section.** A section whose header holds its lines back until something is in place:
`--after` a step, or `--secret` a secret. For example, `[homebrew casks --secret TOKEN]` installs
its casks once `TOKEN` has synced.

**Kind.** A sort of thing kit lists and puts in place, such as Homebrew casks or macOS settings.
Each kind has its own section, `add` and `remove` commands, and step. `kit --help` lists the
kinds under Packages and Settings, and secrets under Files, secrets and features.

**Copied line.** An install command copied from a README, such as `brew install jq`, run with
kit in front: `kit brew install jq`. The program runs it as given, then kit declares each name
it installed. A copied uninstall line, such as `kit brew uninstall jq`, undeclares instead.

**Secret.** A value kept in a password manager, such as 1Password, and put in place on each Mac:
an environment variable, or a file. kit-config holds where the value is kept, never the value.

**Synced.** A secret is synced once kit has read its value from the store and put it in place on
this Mac.

**Secrets block.** A section of secrets in a declarations file. A block for one item in the
store, such as `[secrets op://vault/item]`, names a field of that item on each line. The plain
`[secrets]` block gives each reference in full. `kit secret places` lists the blocks.

**Linked file.** A file in kit-config, under a Mac's `home` folder or `shared/home`, that kit
links into your home folder at the same path. `shared/home/.zshrc` is linked at `~/.zshrc`. An
edit through the link is an edit in kit-config. `kit file add` moves a file in and links it back.

## Keeping a Mac in step

**Step.** One thing kit checks and puts right, such as `homebrew` or `cask`. Each step has a check
that changes nothing. `kit status` runs every check, and `kit apply` applies the steps that
aren't done.

**Step of your own.** A script in kit-config, for set-up no kind covers. `kit step add` writes
it as `steps/<name>/run` and declares it in `[steps]`. kit runs `run check`, then `run apply` if
the check says it isn't done.

**Step by hand.** Something only you can do, such as signing in to the App Store, declared in
`[manual]` with `kit manual add`. It says what to do. A command may say whether it's done, and
otherwise `kit manual done <name>` marks it.

**Backup tool.** Time Machine or Arq, declared in `[backups]` with `kit backup add`. kit checks
that its backups run, and keeps `[backup exclusions]` in Time Machine's list. Arq leaves them
out once its plan follows that list.

**Card.** What the boot, or a whole `kit apply` at a terminal, shows when it needs you. It
says what to do, why, and the steps to do it. A step by hand that isn't done comes up as a card.

**Check of your own.** A command declared in `[checks]`, with `kit check add`, that exits 0 when
all's well. `kit status` and the hourly run run it.

**Job.** A command kit runs on a schedule, declared in `[hourly]` or `[nightly]`. The hourly run
runs the hourly jobs every time, and the nightly jobs once a day.

**Drift.** A difference between this Mac and kit-config. It's one of these:

- a package installed but not declared, or declared but not installed;
- a dependency that nothing installed needs any more;
- a setting changed by hand;
- a linked file that a program replaced with a copy;
- an app installed by hand that kit matches to a cask, the cask not declared.

`kit status` lists drift under CONFIG once a day has passed.

**Reconcile.** Settling drift, with `kit reconcile`. At a terminal it asks about each item.
Without one, you name each item and what to do with it. The main answers:

| Answer | What kit does |
|---|---|
| `--adopt` | Declares what this Mac has |
| `--revert` | Puts back what was there, or what's declared |
| `--remove` | Uninstalls it |
| `--leave` | Leaves it installed by hand, for good |

kit then commits and pushes kit-config. Adopting an app installed by hand also installs its
cask, and Homebrew takes the app over or replaces an older copy. `--install`, `--undeclare` and `--snooze` settle the
rest.

**Feature.** A piece of kit with nothing to list, switched on with `kit feature on`. The features
are apps' settings (`prefs`), Remote Login, File Sharing and Touch ID for sudo. kit-config lists
those on in `[features]`.

**Apps' settings (kit prefs).** The feature that saves your apps' settings every night and
restores them on a new Mac. It saves every preferences domain that isn't Apple's or the
system's. It also saves the keys `[prefs keys]` names and the settings files `[prefs files]`
names. They go to the `prefs` branch of kit-config, which the first save makes.

**The hourly run.** On the hour, launchd opens kit.app, which runs `kit nightly`. It runs the jobs
that are due, then every check. It tells no one what it finds: `kit status` and `kit log` show
it. `kit apply` installs it.

## A Mac you already use

**Snapshot.** What `kit setup` declares of a Mac in use, in that Mac's own declarations file. It
holds the Mac as it stands:

- everything installed and not yet declared, of each kind whose program is there;
- the macOS settings set now, and git's settings;
- the features on, and the backup tools set up.

**Installed by hand.** An app that no kind installed: not from Homebrew or the App Store, not
Apple's, and not one Chrome made for a website. If kit can match it to a Homebrew cask, kit
offers to adopt it, and Homebrew takes the app over. A fork with the same name gets no match. A package you install yourself through Homebrew isn't one: it comes up as drift.

## A new Mac

**The boot.** The one line you paste into Terminal on a new Mac. It runs `kit bootstrap`, which
signs you in to GitHub, on your phone or in a browser, and asks which Mac this is. It installs Homebrew and the
terminal `kit.toml` names, then kit-config and kit, and carries on with `kit apply` in that
terminal.

**The restore list.** The paths of your own data that a new Mac copies across, such as code,
shell history and tools' state. They're declared in `[restore]`, with `kit restore add <path>`.
Adding a path copies nothing now.

**The data restore.** The copy of the restore list's paths, on a Mac the boot has just set up.
It copies from the Mac this one started from, or from a backup. kit rebuilds
everything else from kit-config.
