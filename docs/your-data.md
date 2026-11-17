# Your data comes across to a new Mac

kit rebuilds your apps and settings from kit-config, but your own data isn't there: code,
shell history, the state your tools keep. The restore list names those paths. A new Mac copies
them from the Mac it started from, or from a backup. You'd want it before you move to a new Mac,
or reinstall this one.

## Try it

```bash
# Carry ~/Code across, without its logs
kit restore add ~/Code --skip logs
# Carry shell history across, for every Mac
kit restore add ~/.zsh_history --shared
```

kit declares the path, then commits and pushes kit-config. It copies nothing now:

```text
~/Code ok declared in laptop; nothing copied
```

The line lands under `[restore]`, in `laptop/declarations`:

```text
[restore]
~/Code --skip logs
```

`--skip` names what in the path to leave out, such as credentials a tool makes again when you
set it up. It's relative to the path: a path, or a pattern as rsync takes one, as often as you want.

## Where the data comes from

The data restore runs on the first `kit apply` after the boot. Where it copies from depends on
which Mac this is:

| This Mac | Copies from |
|---|---|
| Started from another Mac | That Mac, over SSH |
| Reinstalled under its own name | Its own backups |
| Started fresh, or in use | Nothing, unless you ask |

On a Mac reinstalled under its own name, a card asks for a Time Machine disk or Arq. It also
offers nothing, for a Mac starting afresh.

## Copying from the old Mac

The new Mac reaches the old one on your local network. `kit.toml` records the local hostname
and the SSH host key of the old Mac. The new Mac copies only from the Mac that presents that
key, at that name. The old Mac needs:

- Remote Login on, with `kit feature on remote-login`. Its next `kit apply` records the key and
  the hostname.
- Remote Login's "Allow full disk access for remote users" on, in System Settings.
- A `~/.ssh/authorized_keys` that holds the key the new Mac uses.
- Homebrew's rsync, on both Macs.
- The same user name and home folder as the new Mac.

If the old Mac doesn't answer, a card offers its Time Machine disk, a restore through Arq by
hand, or trying the Mac again.

## What happens

- **It waits only for what it needs:** rsync 3 on kit's PATH, and, copying from the old Mac,
  your secrets.
- **The rest of the Mac doesn't wait.** Only Claude Code's MCP servers and skills, and steps
  declared `--after restore`, wait for the copy.
- **Nothing here is lost.** Whatever a listed path holds before its copy is set aside in kit's
  state first, never deleted.
- **Projects get their dependencies.** Each restored project installs them again from its
  lockfile: Composer, npm, pnpm, Yarn, Bun or uv.
- **A stopped restore carries on** from where it was, at the next `kit apply`.
- **Once done, it's gone** from `kit apply` and `kit status`.

## What it leaves out by itself

kit brings some things back itself, and others don't belong. The restore leaves out:

- what kit links, what prefs saves, and what a kind puts in place, such as Claude skills;
- kit's own folders;
- what the backups leave out, with `[backup exclusions]`;
- from the old Mac, folders marked excluded from backups, unless one holds a git checkout;
- the `.DS_Store` and `.localized` files Finder makes.

`kit restore add` refuses a path that's one of these, or inside one.

## Good to know

- **A Mac in use restores nothing by itself.** `kit apply --restore-from` brings a path later,
  from the old Mac or a backup, and only while the path is absent or empty. A path holding data is never touched.
- **Without a terminal, there's no card.** `--restore-from` answers it: `mac`, `time-machine` or
  `arq`.
- **One path, one line.** Lines can't overlap: a path inside another listed path is refused.

## Reference

| Command or flag | What it does |
|---|---|
| `kit restore add <path>` | Declares a path for this Mac |
| `add --skip <what>` | Leaves out what's in the path |
| `add --shared` | Declares it for every Mac |
| `kit restore remove <path>...` | Takes paths off the list |
| `kit apply --restore-from <src>` | Restores from `mac`, `time-machine` or `arq` |
| `apply --at <date>` | The latest backup by that date |
| `kit apply reinstall` | Retries the reinstalls that failed |

## Related

- [A new Mac from one line](new-mac.md)
- [Backups that kit checks](backups.md)
- [Secrets from 1Password](secrets.md)
