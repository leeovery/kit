# Apps' settings (kit prefs)

kit prefs saves apps' settings and restores them on a new Mac: every
preferences domain that isn't Apple's or the system's, through defaults
export and import, and the settings files kit-config's `[prefs files]`
names. It's the feature prefs (`kit feature on prefs`), and its store is
the private repository kit.toml's prefs_repo names, a folder for each Mac.

## What it keeps

- Each Mac's settings are captured every night (`kit prefs capture` does it
  now), each capture that changed anything a commit, so a restore can go
  back to a date (`kit prefs history`).
- `[prefs files]` lists settings files beside the domains: a file its
  program replaces when it saves, which a link would lose.
- `[prefs deny]` leaves domains out; `[prefs apps]` says whose a domain is
  when its name doesn't; `[prefs machine-bound]` are never copied to another
  Mac.

Those four sections have no add command: edit them by hand, and commit.
Capture needs Full Disk Access for kit, which `kit status` checks.

Every app's settings are captured, with nothing declared for it. Don't
declare an app's own settings as defaults lines too.

## Restoring

`kit prefs restore` puts settings back from the store: every saved domain
whose app is installed and not running, and the settings files that
differ. It never quits an app that has a window. What it can't restore yet
is held, and `kit prefs pending` lists it:

- an app that isn't installed: restored once it is;
- an app that's running: restored once it quits;
- a sandboxed app with no settings folder yet: kit opens it once, hidden,
  to make one, or asks the person to open it once.

## On a new Mac

The prefs step restores every app's settings once every cask and App Store
app is installed, near the end of the run, before anyone has opened them.
So, on a new Mac:

- An app opened before then, by a step, a login item or a person, leaves
  its restore waiting till it quits.
- A step that opens, drives or configures an app comes after prefs:
  `--after prefs`. It then finds the app installed and its settings
  restored, but still never opened: its first launch is slow, and may
  show onboarding or macOS's question about apps downloaded from the
  internet.
- A step that changes an app's settings itself, rather than leaving them
  to prefs, may have them overwritten by the restore unless it comes
  after prefs.

`kit agent order <name>` says where the prefs step falls for a step, or a
thing as in cask:firefox.
