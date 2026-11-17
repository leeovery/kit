# Glossary: kit's words, defined once

Each term here has one meaning, and the docs use it in that sense only.

## kit-config and what's in it

**kit-config.** The private git repository that declares what each of your Macs should have.
kit keeps its clone in `~/.config/kit`, unless `KIT_CONFIG` names another folder. It holds
`kit.toml`, a folder for each Mac, and the `shared` folder.

**kit.toml.** The file at the top of kit-config that names your Macs. It also names the terminal
a new Mac installs, the repository for apps' settings, and when nightly jobs run.

**Declaration.** A line that says a Mac should have something: a package, a setting, a secret,
a step. A note may follow it, after `#`. kit's `add` commands write declarations, and you can
write one by hand.

**Declarations file.** The file of declarations in each folder of kit-config, such as
`laptop/declarations` for the Mac named `laptop`. It's made of sections, each a header in
brackets, such as `[homebrew casks]`, then a line for each thing. kit's commands keep each list
sorted.

**`shared`.** The folder of kit-config whose declarations file is for every Mac. A Mac has what
`shared/declarations` declares, and what its own file declares. `--shared` on an `add` command
puts the line in the shared file.

**Held section.** A section whose header holds its lines back until something is in place:
`--after` a step, or `--secret` a secret. For example, `[homebrew casks --secret TOKEN]` installs
its casks once `TOKEN` has synced.

**Kind.** A sort of thing kit lists and installs, such as Homebrew's casks. Each kind has its own
section, `add` and `remove` commands, and step. `kit --help` lists the kinds
under Packages and Settings.

**Secret.** A value kept in a password manager, such as 1Password, and put in place on each Mac:
an environment variable, or a file. kit-config holds where the value is kept, never the value.

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

**Drift.** A difference between this Mac and kit-config. It's a package installed but not
declared, or declared but not installed. It's also a setting changed by hand, or a linked file
that a program replaced with a copy. `kit status` shows drift after its first day.

**Reconcile.** Settling drift, with `kit reconcile`. At a terminal it asks about each item.
Without one, you name each item and what to do with it. The main answers:

| Answer | What kit does |
|---|---|
| `--adopt` | Declares what this Mac has |
| `--revert` | Puts back what's declared |
| `--remove` | Uninstalls it |

kit then commits and pushes kit-config. `--install`, `--undeclare` and `--snooze` settle the
rest.

**Feature.** A piece of kit with nothing to list, switched on with `kit feature on`. The features
are apps' settings (`prefs`), Remote Login, File Sharing and Touch ID for sudo. kit-config lists
those on in `[features]`.

**Apps' settings (kit prefs).** The feature that saves your apps' settings every night and
restores them on a new Mac. It saves every preferences domain that isn't Apple's or the
system's, and the settings files `[prefs files]` names. They go to a private repository of
their own, which `kit.toml` names.

**The hourly run.** On the hour, launchd opens kit.app, which runs `kit nightly`. It runs the jobs
that are due, then every check. It tells no one what it finds: `kit status` and `kit log` show
it. `kit apply` installs it.

## A new Mac

**The boot.** The one line you paste into Terminal on a new Mac. It runs `kit bootstrap`, which
signs you in to GitHub on your phone and asks which Mac this is. It installs Homebrew, then
kit-config and kit, and carries on with `kit apply` in the terminal `kit.toml` names.

**The restore list.** The paths of your own data that a new Mac copies across, such as code,
shell history and tools' state. They're declared in `[restore]`, with `kit restore add <path>`.
Adding a path copies nothing now.

**The data restore.** The copy of the restore list's paths, on a Mac the boot has just set up.
It copies from the Mac this one started from, or from a backup. kit rebuilds
everything else from kit-config.
