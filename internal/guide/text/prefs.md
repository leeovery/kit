# Apps' settings (kit prefs)

kit prefs saves apps' settings and restores them on a new Mac: every
preferences domain that isn't Apple's or the system's, through defaults
export and import, and the settings files kit-config's `[prefs files]`
names. It's the feature prefs (`kit feature on prefs`), and its store is
kit-config's prefs branch, a folder for each Mac, on the same remote as
kit-config: nothing to set up, and the first capture makes the branch.
kit clones that branch alone into its data folder. The boot clones
kit-config's own branch alone, so a kit-config it cloned never fetches
prefs; a clone of kit-config made otherwise may fetch every branch.

## What it keeps

- Each Mac's settings are captured every night (`kit prefs capture` does it
  now), each capture that changed anything a commit, so a restore can go
  back to a date (`kit prefs history`).
- `[prefs files]` lists settings files beside the domains: a file its
  program replaces when it saves, which a link would lose.
- `[prefs keys]` keeps chosen keys of one domain, those alone, even a
  domain prefs skips, as Apple's are (below).
- `[prefs deny]` leaves domains out; `[prefs apps]` says whose a domain is
  when its name doesn't; `[prefs machine-bound]` are never copied to another
  Mac.

Those five sections have no add command: edit them by hand, and commit.
Capture needs Full Disk Access for kit, which `kit status` checks.

Every app's settings are captured, with nothing declared for it. Don't
declare an app's own settings as defaults lines too.

## Keys of an Apple domain

The Dock's apps and folders are two keys of com.apple.dock. This section
keeps them:

    [prefs keys]
    com.apple.dock persistent-apps persistent-others --restart Dock

- A line is the domain, the keys kept, then --restart and a process to
  restart once they're restored, if one needs it. It may name any domain;
  an app's needs its app named, as a running app writes over the keys.
- Capture saves those keys alone, in `<mac>/keys/<domain>.plist` on the
  prefs branch, and never the domain's other keys: declare those as
  `kit defaults` lines, such as the Dock's size.
- Restore merges the keys into the domain as it stands, and restarts the
  process only if that changed anything. An item pointing at a file not on
  this Mac, such as the Dock tile of an app not installed, is left out.
- A domain with items left out stays held (`kit prefs pending`), waiting
  for apps to put its items back. Once an item's file is on the Mac,
  `kit apply` or the hourly run puts that item back where it was, and
  nothing else. Once the person changes those keys, as by moving a Dock
  tile, kit drops the hold, and the nightly capture saves them as they are.
- On a new Mac this happens with the rest of the restore, once the run's
  apps are installed.

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
app is installed, near the end of the run, before most have been opened.
So, on a new Mac:

- An app opened before then, by a step, a login item or a person, leaves
  its restore waiting till it quits.
- A step that opens, drives or configures an app comes after prefs:
  `--after prefs`. It then finds the app installed and its settings
  restored, but likely never opened: its first launch is slow, and may
  show onboarding or macOS's question about apps downloaded from the
  internet. `--after prefs` fails the step on a Mac without kit prefs on,
  so a step declared for every Mac with it needs prefs on in every Mac.
- A step that changes an app's settings itself, rather than leaving them
  to prefs, may have them overwritten by the restore unless it comes
  after prefs.

`kit agent order <name>` says where the prefs step falls for a step, or a
thing as in cask:firefox.
