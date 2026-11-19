# Your first kit-config, from the Mac you use

`kit setup` brings the Mac you use today onto kit in one command. It makes a private
kit-config on GitHub and declares everything the Mac has, as it stands. Then it commits, pushes
and runs `kit apply`. You'd run it on a Mac that's already set up, before kit looks after it.

## Try it

```bash
# On a Mac with Homebrew, install kit
brew install leeovery/tools/kit
# Bring this Mac onto kit
kit setup
```

`kit setup` signs you in to GitHub and asks a few questions. It ends on what it declared:

<img src="../art/first-config-done.webp" alt="kit setup's end on a Mac in use: GitHub, kit-config and this Mac done, 370 things declared by part, three apps installed by hand, a note on secrets, and kit apply carrying on" width="573">

On a blank Mac, use the one-line install instead: see [A new Mac from one line](new-mac.md). A
Mac with a kit-config already is refused, because kit looks after it.

## What it asks

Each question comes with a suggested answer, so enter takes it. Each has a flag too:

| Question | Suggested | Flags |
|---|---|---|
| kit-config | Join yours, else make `kit-config` | `--create`, `--join` |
| This Mac | A name from its model | `--mac` |
| Your data | Your own folders | `--restore`, `--no-restore` |
| Apps' settings | Save every night | `--prefs`, `--no-prefs` |
| Linked files | Every one found | `--link`, `--no-link` |
| What to install (joining) | Install with the rest | `--apply`, `--no-apply` |

- **Your data** lists the folders in your home folder, with their sizes. A new Mac copies the
  ones you keep from your backups. Downloads, Movies, Music and Public start unticked.
- **Apps' settings** switches on a save every night, which a new Mac restores from. See
  [Apps' settings](apps-settings.md).
- **Linked files** lists files such as `~/.zshrc` and `~/.ssh/config`. Each one you keep moves
  into kit-config and is linked back.

## What happens

kit takes a snapshot of the Mac as it stands, and declares it in this Mac's own declarations
file:

- **Everything installed and not yet declared,** of each kind whose program is here, such as
  Homebrew casks, login items and MCP servers.
- **The folders those programs need on kit's PATH,** such as `~/.local/bin` for Claude Code.
  kit adds each to this Mac's `[paths]`, as `kit path add` does, if your shell finds the program
  there and kit's PATH doesn't.
- **The macOS settings set now,** leaving out the state macOS keeps for itself, and git's global
  settings.
- **The features on and the backup tools set up,** such as Touch ID for sudo and Time Machine.
- **Your answers go beside it.** kit commits all of it as one, and pushes it.
- **Apps installed by hand are listed, never managed.** If kit sets this Mac up again, you
  install them yourself.
- **Secrets aren't looked for.** kit keeps a secret in a store, never in kit-config: declare each
  one with `kit secret add`. See [Secrets from 1Password](secrets.md).

## A second Mac joins

On your next Mac, `kit setup` finds kit-config on your account and offers to join it. Then
pick which Mac this is, or a new one. A new Mac starts from what every Mac shares, or as a copy
of another with `--from`. Either way, the snapshot then declares what this Mac has. kit-config
must be private: `--join` refuses a repository that isn't.

```bash
# Join kit-config as a new Mac named studio
kit setup --join owner/kit-config --mac studio
```

kit then asks about what kit-config declares that this Mac hasn't. Install it with the rest,
or stop to look first with `kit status`.

## Good to know

- **Run it again if it stops.** `kit setup` carries on where it stopped, and nothing is made
  twice. Bare `kit` at a terminal offers the same. Once the commit is pushed, setup is done.
- **A package with a private download is left out.** Setup refuses its line without the token,
  and won't come back to it. Declare the token with `kit secret add`, then add the package with
  `kit brew add` or `kit cask add`.
- **Never delete `~/.config/kit` part-way.** Setup moves files into it and links them back.

## Reference

| Flag | What it answers |
|---|---|
| `--create <name>` | Make kit-config, private, on your account |
| `--join owner/name` | Join a kit-config you have |
| `--mac <name>` | This Mac's name |
| `--from <mac>` | The Mac a new one copies |
| `--restore <path>` | A folder a new Mac brings back |
| `--prefs` | Save apps' settings every night |
| `--link <path>` | A file kit-config keeps, linked back |
| `--no-apply` | Stop after the commit, to look first |

`--restore` and `--link` take one path each time. `--restore`, `--prefs` and `--link` each
have a `--no-` flag that answers no. Without a terminal, kit asks nothing: a question its flag
doesn't answer stops setup, naming the flag.

## Related

- [A new Mac from one line](new-mac.md)
- [Packages: install once, and your next Mac has it](packages.md)
- [Glossary](glossary.md)
