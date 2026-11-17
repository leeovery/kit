# Drift: keep or undo what changed by hand

You install a package with brew, change a Dock setting, or an app replaces a linked file. kit
sees each one, and `kit reconcile` asks what you want. You keep it, and kit declares it in kit-config. Or
you undo it, and the Mac goes back to what kit-config says. You'd want it so that your next
Mac matches the one you use today.

## Try it

```bash
# See how this Mac stands, drift included
kit status
```

Drift shows under CONFIG, with how long it's been there:

```console
  CONFIG
  ▲ ffmpeg  installed, not declared · 2 days · → kit reconcile
```

```bash
# Settle it: kit asks, then does it all
kit reconcile
```

At a terminal, kit asks about each item first. Then it does what you chose, and commits and
pushes kit-config once:

```console
  ● ffmpeg  already installed · declared in laptop
  ● kit-config  committed and pushed laptop/declarations

  1 settled · 6.1s
```

## What happens

- **A day's grace.** Drift counts only after a day. A package you try and remove the same
  afternoon never needs you. Until then, `kit status` counts it as new.
- **Every hour, kit looks.** The hourly run runs every check, so drift turns up without you
  asking. It tells no one: `kit status` shows it.
- **Each item has its own answers.** A package can be declared or uninstalled. A setting can
  be kept as it is now, or put back.
- **One commit for the lot.** Whatever you settle in one run goes into kit-config as one
  commit, pushed.
- **Nothing is lost by stopping.** If you stop part way, kit does what you answered so far.
  Cancel, and nothing changes.

## Settling by id

Without a terminal, `kit reconcile` asks nothing. It lists each item with its id and the
answers it takes:

```text
kit reconcile · laptop
brew:ffmpeg extra since 31 Dec: --adopt, --remove or --snooze
brew:node@20 unused-dependency since 31 Dec: --remove, --adopt or --snooze
cask:firefox extra since 31 Dec: --adopt, --remove or --snooze
```

Name the ids, and one answer for them all:

```bash
# Declare both, for this Mac
kit reconcile brew:ffmpeg cask:firefox --adopt
# Quiet one for 7 days
kit reconcile cask:firefox --snooze
```

The answers, and what each one does:

| Answer | What kit does |
|---|---|
| `--adopt` | Declares what this Mac has |
| `--revert` | Puts back what's declared |
| `--remove` | Uninstalls it |
| `--install` | Installs it, as declared |
| `--undeclare` | Takes its line out |
| `--snooze` | Quiets it for 7 days |

`--shared` with `--adopt` or `--undeclare` uses the shared file, for every Mac. `--note "why"`
keeps a note after the line it adopts. `kit reconcile brew cask` goes through only those kinds.

## Trying something out

```bash
# Install it, but don't declare it yet
kit brew add hello --temp
```

```text
hello ok installed, for now: not declared, quiet for 7 days, then kit reconcile asks
```

The package stays quiet for 7 days. After that, it's drift like any other, and `kit
reconcile` asks whether to keep it.

## Good to know

- **kit watches only the kinds you declare.** A kind is checked only if kit-config has its
  section for this Mac, even an empty one. `--temp` adds an empty section if there's none.
- **A setting removed from the Mac can be accepted.** `--adopt` on a removed setting leaves
  it removed, and kit stops showing it. See [macOS settings](settings.md).
- **A replaced linked file goes to the Bin.** `--revert` links kit-config's copy back, and
  moves the Mac's copy to the Bin. `--adopt` moves the Mac's copy into kit-config instead.
- **Edits by hand in kit-config come up too.** `kit reconcile` commits a file you edited
  there, or undoes the edit.

## Reference

| Command or flag | What it does |
|---|---|
| `kit status` | Every check, drift under CONFIG |
| `kit reconcile` | Asks about each item, at a terminal |
| `reconcile --all` | Asks about quiet items too |
| `kit reconcile <id>... --<answer>` | Settles those items |
| `kit reconcile --json` | Lists the items as one document |
| `kit <kind> add --temp` | Installs without declaring |

## Related

- [Packages: install once, and your next Mac has it](packages.md)
- [macOS settings](settings.md)
- [Built for agents](agents.md)
