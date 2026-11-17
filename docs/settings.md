# macOS settings: set once, kept on every Mac

Change a setting in System Settings as you always do, then tell kit to keep it. kit reads the
value this Mac has and declares it in kit-config, and each Mac it's declared for gets it.
kit also watches the Dock, Finder, the trackpad and more, so a change you forgot to keep still
turns up. Power settings and git settings work the same way.

## Try it

```bash
# Hide the Dock in System Settings, then keep it
kit defaults add com.apple.dock:autohide
```

kit reads the value and writes the line in the form `defaults write` takes:

```text
[macos settings]
com.apple.dock autohide -bool true
```

```bash
# Keep the charger's sleep setting, for every Mac
kit power add charger:sleep --shared
# Keep how git pulls
kit git-config add pull.rebase
```

Each lands in its own section, in sorted order, then kit commits and pushes kit-config:

```text
[power settings]
-c sleep 0

[git config]
pull.rebase true
```

## The settings kit keeps

| Command | Section | Named as |
|---|---|---|
| `kit defaults` | `[macos settings]` | `com.apple.dock:autohide` |
| `kit power` | `[power settings]` | `charger:sleep` |
| `kit git-config` | `[git config]` | `pull.rebase` |

A setting name is the domain and the key, joined by a colon. A power setting starts with its
source: `charger`, `battery`, `all` or `ups`.

## What happens

- **`kit apply` sets what's missing.** On a new Mac, or for a line you wrote by hand, kit writes
  the declared value.
- **You see the change at once.** kit restarts the Dock, Finder or Control Center after it
  writes one of their settings. It reloads keyboard shortcuts too.
- **A change by hand is never undone behind your back.** If a declared setting changes on the
  Mac, `kit apply` leaves it. It's drift, and you choose to keep the new value or put the
  declared one back.
- **Removing differs by kind.** `kit defaults remove` and `kit git-config remove` unset the
  setting. `kit power remove` only takes the line out, and the value stays.

## Changes in Apple's settings show up

kit watches these domains, and every domain you declare a setting in:

- the Dock, Finder, Spaces and Stage Manager;
- the trackpad, the keyboard and keyboard shortcuts;
- screenshots, the menu bar clock and Control Center;
- whether Finder leaves `.DS_Store` files on other drives;
- the global domain, which holds settings for every app.

The first time kit looks at a domain, it takes the values as they are. After that, a setting
you change there and haven't declared is drift. After a day, `kit status` lists it, and `kit
reconcile` asks whether to declare it or put back what it was.

A setting removed from the Mac shows up the same way:

```console
  ▲ com.apple.dock:autohide  removed, not declared · 2 days · → kit reconcile
```

`kit reconcile defaults:com.apple.dock:autohide --adopt` accepts it as removed, and kit stops
showing it. `--revert` puts the old value back. See
[Drift: keep or undo what changed by hand](drift.md).

## What macOS changes by itself is ignored

macOS writes some values on its own, all day. kit ignores them, so they never show up as
drift:

- window sizes and positions, and the columns of a table;
- recent files, folders and places;
- timestamps and dates;
- the apps in the Dock, and Finder's views of the Trash and the network.

git is different: every global setting that kit-config doesn't declare shows up.

## Good to know

- **A setting for this Mac only starts `currentHost:`.** For example,
  `currentHost:NSGlobalDomain:com.apple.mouse.tapBehavior` writes a line that starts
  `-currentHost`.
- **Some settings need your password.** A setting in a domain under `/Library`, and every
  power setting, is written through sudo. `kit apply` asks for the password once, at a
  terminal.
- **An app keeps its own settings elsewhere.** A setting in the domain of an app is saved with
  the rest of that app: see [Apps' settings](apps-settings.md).
- **Remote Login, File Sharing and Touch ID for sudo** are features, switched on with
  `kit feature on`.

## Reference

| Command or flag | What it does |
|---|---|
| `kit <kind> add <name>...` | Declares it as this Mac has it |
| `add --shared` | Declares it for every Mac |
| `add --note "<why>"` | Keeps why, after the line |
| `kit <kind> remove <name>...` | Takes the line out, as above |
| `remove --shared` | Needed to remove a shared line |
| `kit reconcile` | Settles what changed by hand |

Each of these takes `--json`, and `--help` says what it needs and what it changes.

## Related

- [Drift: keep or undo what changed by hand](drift.md)
- [Apps' settings](apps-settings.md)
- [Packages: install once, and your next Mac has it](packages.md)
