# A new Mac from one line

Paste one line into Terminal on a blank Mac, and kit sets it up from kit-config. You sign in to
GitHub on your phone, pick which Mac this is, and type your password once. kit installs the
rest, then carries on in your own terminal. You'd use it on every Mac you set up.

## Try it

```bash
# On the new Mac, in Terminal
/bin/bash -c "$(curl -fsSL \
https://raw.githubusercontent.com/leeovery/kit/main/install.sh)"
```

The line downloads the newest release of kit, checks it against its checksum, and runs
`kit bootstrap`. A splash plays while kit tests the Mac, then enter starts the boot. Each row
is a step, in order:

```text
  SIGN IN
  [ OK ] GitHub  someone · kit-config has laptop and studio

  BOOT ORDER
  [ OK ] This Mac  laptop
  [ OK ] Password  Touch ID on for sudo
  [ OK ] Homebrew  7.0.9 · Xcode tools 27.0 · 6m02s
  [ OK ] kit-config  cloned · 61 files linked
  [ OK ] kit  0.1.1 · through Homebrew · 21s
  [ OK ] Full Disk Access  granted to Ghostty
  [*   ] Ghostty  opening
         → macOS asks before Ghostty first opens: choose Open
```

## The boot, step by step

1. **GitHub.** Press enter for a QR code to scan with your phone, or `s` for the browser here.
   kit reads kit-config, which must be private.
2. **This Mac.** Pick one of the Macs kit-config names, or `new`. A new Mac gets a name, then
   starts fresh or as a copy of one of your Macs.
3. **Password.** Typed once, for everything the boot installs. If kit-config turns on Touch ID
   for sudo, macOS asks to allow it.
4. **Homebrew,** with Apple's command line tools.
5. **Apps.** The terminal `kit.toml` names, such as Ghostty.
6. **kit-config.** Cloned to `~/.config/kit`, its files linked into your home folder.
7. **kit,** installed through Homebrew, so it stays up to date.
8. **Full Disk Access.** kit opens System Settings and Finder. You drag the terminal into the
   list, then press enter.
9. **The terminal.** It opens, and kit carries on there.

## kit apply takes over

In the terminal, kit runs the whole of `kit apply` on a screen of its own. Rows on the left
show each part of the Mac, such as Homebrew casks or Secrets, with what each waits for.
Installs run side by side, a few at a time.

If something waits for a step done by hand, such as "Sign in to 1Password", the step comes up
as a card on the right. The card lists what to do, and kit carries on by itself once it's
done. Steps by hand that nothing waits for come at the end, as a to-do list, with set-up only
you can do:

```text
  TO DO  2 · kit apply brings them back
  △ 1  Add a Time Machine disk
       → Plug in your backup disk
  △ 2  Set up Arq's backup plan
       → Open Arq
       → Sign in to your Arq account
       → Adopt this Mac's plan, and let it run
```

Once `kit apply` ends, the window becomes a fresh shell.

## Which Mac it starts from

| You pick | Its declarations | Your data |
|---|---|---|
| `new`, from one of your Macs | A copy of that Mac's | Copied from that Mac |
| A Mac kit-config names | That Mac's own | From its own backups |
| `new`, fresh | Only `shared` | Nothing restored |

You can make the copy beforehand: `kit machine add studio --from laptop` adds `studio`,
started from `laptop`. Pick `studio` at the boot, and it copies from `laptop`. [Your data on a new Mac](your-data.md) covers the data restore.

## What waits for what

kit installs nothing before what it needs is in place:

- Each kind waits for its program on kit's PATH: npm from a formula, `gh`, `mas`,
  `claude` and `agntc`.
- A line from a private tap waits for its token to sync.
- App Store apps need you signed in to the App Store. Declare that sign-in as a step by hand,
  before them, or their install fails.
- Login items wait for the casks and App Store apps.
- Claude Code's MCP servers and skills wait for the data restore. So does a step declared
  `--after restore`.

## Good to know

- **A failure is no dead end.** Paste the install line into Terminal again. Each step that's
  done is found so by its check, and the boot picks up where it stopped.
- **After the boot, run `kit apply` again.** It installs what failed or waited, and never
  removes anything.
- **Your config, by name.** Without `--config`, the boot reads the repository named
  `kit-config` in your GitHub account. If you don't have one yet, see
  [Your first kit-config](first-config.md).

## Reference

| Command or flag | What it does |
|---|---|
| `kit bootstrap` | The boot: the install line runs it |
| `bootstrap --mac <name>` | Names this Mac, rather than asking |
| `bootstrap --config owner/name` | Reads another config repository |
| `kit machine add <name> --from <mac>` | Adds a Mac, a copy of another |
| `kit apply` | Installs what's declared and missing |

## Related

- [Your data on a new Mac](your-data.md)
- [Steps of your own, and steps by hand](steps.md)
- [Packages: install once, and your next Mac has it](packages.md)
