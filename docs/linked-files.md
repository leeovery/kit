# Linked files: your config files, on every Mac

`kit file add` moves a file you write into [kit-config](glossary.md) and links it back in its
place. A Mac that replaces this one gets the same file at the same path, and `--shared` puts
it on every Mac.
You'd want it for any config file you edit by hand, such as `~/.zshrc`.

## Try it

```bash
# Move a config file into kit-config, linked back
kit file add ~/.config/tool/conf
```

kit moves the file, links it back, then commits and pushes kit-config:

```console
  ● ~/.config/tool/conf  in kit-config, linked: laptop/home/.config/tool/conf
  ● kit-config  committed and pushed laptop/home/.config/tool/conf
```

```bash
# Link it on every Mac instead
kit file add ~/.zshrc --shared
# Say where a linked file comes from
kit why ~/.zshrc
```

```text
~/.zshrc (file)
  linked from shared/home/.zshrc
  linked here
```

## kit-config mirrors your home folder

Each Mac has a `home` folder in kit-config, and so does `shared`. kit links each file in them
to the same path under `~`:

| In kit-config | Linked at |
|---|---|
| `shared/home/.zshrc` | `~/.zshrc`, on every Mac |
| `laptop/home/.config/tool/conf` | `~/.config/tool/conf`, on `laptop` |

kit links one file at a time, never a folder. The other files an app keeps beside a linked one
stay the app's.

## What happens

- **An edit through the link is an edit in kit-config.** kit doesn't commit it for you.
  `kit status` lists it under CONFIG, and `kit reconcile` commits and pushes it, or undoes it.
- **A folder adds file by file.** `kit file add ~/.config/tool` moves and links each file in
  it. A file git ignores stays on this Mac only, and kit names it.
- **Private files stay private.** A file under `~/.ssh` or `~/.gnupg` is kept readable by you
  only, and so is its folder. A public key, ending `.pub`, is left as it is.
- **A new Mac has them from the start.** The boot links kit-config's files when it clones
  kit-config, so the terminal it opens is set up already.
- **`kit file remove` undoes it.** It puts a copy back in place of the link, and takes the file
  out of kit-config. A file every Mac links needs `--shared`.

## Link only files you write

Some programs replace a file when they save it. They write a new copy where the link was, so
the link is gone after the first save. Many apps do this with their settings, and so do some
tools. kit can't tell which files a program replaces, so `kit file add` links them anyway.

Don't link those files. Leave them to [apps' settings](apps-settings.md), which saves them
every night and restores them on a new Mac. It saves only once `kit feature on prefs` is on.
A settings file outside an app's preferences is saved only if `[prefs files]` names it.

A file that holds a secret doesn't belong in kit-config either. Declare it as a secret, and
kit writes it on each Mac: see [Secrets from 1Password](secrets.md).

## A replaced link is drift

A program may save over a link, or you may put another file in its place. After a day,
`kit status` shows that as [drift](drift.md). kit never overwrites a file that differs from
the one in kit-config: `kit apply` leaves it alone.

```bash
# Put kit-config's file back, the Mac's to the Bin
kit reconcile file:~/.zshrc --revert
```

```console
  ● ~/.zshrc  linked to kit-config's · the Mac's copy is in the Bin (~/.Trash/.zshrc)
```

If the copy on this Mac is the one to keep, `--adopt` takes it into kit-config and links it.
Then kit commits and pushes kit-config.

## Good to know

- **One place a file.** A file in both `shared/home` and a Mac's `home` is refused. Move it to
  one of them.
- **Some paths are refused.** `kit file add` refuses a link, or a path outside your home
  folder. It also refuses a path git ignores, and a folder that holds a git repository.
- **A copy that matches is relinked.** If the file in place is the same as kit-config's,
  `kit apply` links it again without asking.
- **Declare what a file needs.** If your `.zshrc` loads a plugin, declare the plugin as a
  package or a [step of your own](steps.md). Otherwise the shell on a new Mac can break.

## Reference

| Command | What it does |
|---|---|
| `kit file add <path>...` | Move into this Mac's `home`, link back |
| `add --shared` | Into `shared/home`, for every Mac |
| `kit file remove <path>...` | A copy back in place, out of kit-config |
| `remove --shared` | Needed for a file every Mac links |
| `kit reconcile file:<path>` | Settle drift: `--revert`, `--adopt` |
| `kit why <path>` | Where it's linked from, and whether it is |

Each command takes `--json`, and `--help` says what it changes.

## Related

- [Drift: keep or undo what changed by hand](drift.md)
- [Steps of your own](steps.md)
- [Apps' settings](apps-settings.md)
