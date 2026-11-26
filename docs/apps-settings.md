# Apps' settings: saved every night, back on a new Mac

kit saves the settings your apps keep, every night, with nothing to declare for each app. A
new Mac gets them back near the end of its first `kit apply`, before you open most apps. You'd
want it if you've tuned your apps and don't want to do it again on the next Mac.

## Try it

```bash
# Save apps' settings on this Mac
kit feature on prefs
# Start from the settings this Mac has now
kit prefs start-fresh
# Save them now, rather than tonight
kit prefs capture
```

On a Mac you already use, `kit setup` asks whether to save them; a yes runs the first two
commands for you. See [Your first kit-config](first-config.md).

## What happens

- **Every app is covered,** except Apple's apps and macOS itself. kit reads each preferences
  domain through `defaults export`. It also saves the files `[prefs files]` names, and the
  keys `[prefs keys]` names, such as the apps in your Dock.
- **The hourly run saves them once a night.** Each save that changed anything is a commit.
- **A new Mac restores them** once its casks and App Store apps are installed, if prefs is on
  for it. It is if the new Mac takes the old one's name, or starts as a copy of it. Otherwise,
  run `kit feature on prefs` there, or `kit feature on prefs --shared` for every Mac.
- **A running app waits.** kit never quits an app with a window: the settings come back
  once you quit it.
- **An app not installed yet is held.** The settings come back when kit installs it.

`kit prefs pending` lists what's held, and why.

## Your Dock's apps, on the next Mac

Add this section to a declarations file, and the next save keeps the apps in your Dock:

```
[prefs keys]
com.apple.dock persistent-apps --restart Dock
```

A line names a domain, then the keys to keep, then the process to restart once they're back.
Add `persistent-others` to keep the folders on the right of the Dock too. kit saves only those
keys, so the Dock's size stays with `kit defaults`. A restore merges them in and restarts the
Dock. It leaves out any app that isn't installed, so the Dock shows no question marks.

## Where they're kept

Your settings go on a branch of kit-config named `prefs`, a folder for each Mac. The first save
makes the branch, so there's nothing to set up. kit keeps its own clone of the branch in
`~/.local/share/kit/prefs`.

kit pushes the branch only if it can prove kit-config is private on GitHub. On a new
kit-config, it also waits for the first push of kit-config itself. Until then, the saves stay on
this Mac.

## Restoring

On a new Mac, `kit apply` brings a to-do, "Restore application settings". Press `r` to restore
the last save, or `f` to start afresh. With no save in the store, kit starts afresh by itself.
A Mac with no save of its own restores the save of the Mac it started as a copy of.

```bash
# See what a restore would change
kit prefs restore --dry-run
# Restore the settings from three days ago
kit prefs restore --at "3 days ago"
# Restore another Mac's settings onto this one
kit prefs restore --from laptop
```

A restore onto other hardware leaves out the domains `[prefs machine-bound]` lists. That holds
with `--from`, and on a new Mac that took the old one's name. `kit prefs history` lists the
saves that changed anything, newest first.

To start afresh instead, run `kit prefs start-fresh`. It switches saving on without restoring
anything, and the next save keeps the settings this Mac has now. `kit prefs pending --clear`
drops what's held, so the next save keeps what's on the Mac.

## Good to know

- **kit needs Full Disk Access to save or restore.** Without it, kit refuses, because macOS
  would hand over a sandboxed app's settings as empty. kit.app needs it. So does your
  terminal, for `kit prefs` run there. `kit status` checks it. See
  [Running by itself](running-by-itself.md).
- **Leave apps out of `kit defaults`.** kit saves their settings already.
- **Five sections tune it,** listed under Reference. They have no `add` command: edit them
  by hand in a declarations file, then commit.
- **A step that sets up an app** belongs after the restore: declare it `--after prefs`. See
  [Steps of your own, and steps by hand](steps.md).

## Reference

| Command | What it does |
|---|---|
| `kit feature on prefs` | Save apps' settings on this Mac |
| `kit prefs capture` | Save now, as the nightly run does |
| `kit prefs restore` | Put settings back from the store |
| `kit prefs history` | The saves that changed anything |
| `kit prefs pending` | What's held to restore, and why |
| `kit prefs start-fresh` | Save from now, restoring nothing |

| Section | What it holds |
|---|---|
| `[prefs files]` | Settings files saved beside the domains |
| `[prefs keys]` | Keys of a domain saved, those only |
| `[prefs deny]` | Domains left out |
| `[prefs apps]` | Which app a domain belongs to |
| `[prefs machine-bound]` | Domains kept to the hardware they came from |

## Related

- [Linked files: your config files, on every Mac](linked-files.md)
- [macOS settings: set once, kept on every Mac](settings.md)
- [A new Mac from one line](new-mac.md)
