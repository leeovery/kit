# Commands: every kit command, and what it's for

This page lists every command, in the groups `kit --help` uses. Each line links to the page
that covers it. To see the flags a command takes, run `kit <command> --help`.

At a terminal, `kit` on its own opens a menu of what to run next.

## This Mac

| Command | What it's for | Page |
|---|---|---|
| `kit status` | Every check: what needs attention | [Drift](drift.md) |
| `kit apply` | Install what's declared and missing | [Packages](packages.md) |
| `kit reconcile` | Settle drift: adopt, remove or undo | [Drift](drift.md) |
| `kit nightly` | Run the jobs that are due, then every check | [Running by itself](running-by-itself.md) |
| `kit list` | List what's declared for this Mac | [Packages](packages.md) |
| `kit why` | Where a package is declared, and what needs it | [Packages](packages.md) |
| `kit log` | Past runs, with every check and command | [Running by itself](running-by-itself.md) |
| `kit machine` | Show this Mac's name, or set it | [A new Mac](new-mac.md) |
| `kit bootstrap` | Set up a new Mac: the install line runs it | [A new Mac](new-mac.md) |
| `kit agent` | Instructions for an agent working through kit | [Agents](agents.md) |

`kit machine add` adds a Mac to kit-config, empty or as a copy of another (`--from`).

## Packages

Each of these has `add` and `remove`. `add` installs and declares, and `remove` uninstalls and
undeclares.

| Command | What it installs | Page |
|---|---|---|
| `kit brew` | Homebrew formulae | [Packages](packages.md) |
| `kit cask` | Homebrew casks | [Packages](packages.md) |
| `kit mas` | App Store apps | [Packages](packages.md) |
| `kit npm` | Global npm packages | [Packages](packages.md) |
| `kit composer` | Global Composer packages | [Packages](packages.md) |
| `kit gh` | GitHub CLI extensions | [Packages](packages.md) |
| `kit login-item` | macOS login items | [Packages](packages.md) |
| `kit claude-mcp` | Claude Code's MCP servers | [Packages](packages.md) |
| `kit claude-skill` | Claude skills, through agntc | [Packages](packages.md) |

`kit claude-mcp` also has `on` and `off`. `off` removes a server from Claude Code, and keeps its
line.

## Settings

Each of these has `add` and `remove`. `add` declares a setting with the value this Mac has.

| Command | What it declares | Page |
|---|---|---|
| `kit git-config` | git's global settings | [Settings](settings.md) |
| `kit defaults` | macOS settings, in `defaults write` form | [Settings](settings.md) |
| `kit power` | Power settings, in `pmset` form | [Settings](settings.md) |
| `kit backup-exclusion` | Folders the backups leave out | [Backups](backups.md) |
| `kit spotlight-exclusion` | Folders Spotlight leaves out | [Settings](settings.md) |

## Files, secrets and features

| Command | What it's for | Page |
|---|---|---|
| `kit file` | Files linked into your home folder | [Linked files](linked-files.md) |
| `kit path` | Directories on the PATH | [Linked files](linked-files.md) |
| `kit secret` | Secrets from 1Password | [Secrets](secrets.md) |
| `kit prefs` | Apps' settings, saved and restored | [Apps' settings](apps-settings.md) (coming) |
| `kit feature` | Switch a feature on or off | [Settings](settings.md) |
| `kit backup` | Backup tools this Mac uses | [Backups](backups.md) |
| `kit restore` | Data a new Mac carries across | [Your data](your-data.md) |

Most of these have `add` and `remove`. Three differ:

- `kit secret` has `add`, `remove`, `sync` and `places`.
- `kit prefs` has `capture`, `restore`, `history`, `pending` and `start-fresh`.
- `kit feature` has `on` and `off`.

## Your own

| Command | What it's for | Page |
|---|---|---|
| `kit check` | Checks of your own, each a command | [Running by itself](running-by-itself.md) |
| `kit hourly` | Jobs of your own, run every hour | [Running by itself](running-by-itself.md) |
| `kit manual` | Steps by hand, each saying what to do | [Steps](steps.md) |
| `kit step` | Steps of your own, each a script | [Steps](steps.md) |

Each has `add` and `remove`. `kit manual done` marks a step by hand done on this Mac.
`kit nightly add` and `kit nightly remove` declare jobs for the nightly run.

## Other commands

| Command | What it's for |
|---|---|
| `kit splash` | Play the boot's splashes again, for fun |
| `kit help` | Help for any command |

## Flags every command takes

| Flag | What it does |
|---|---|
| `--json` | Print one JSON document, for scripts and agents |
| `--plain` | Plain lines, with no colour or animation |
| `--verbose` | Keep commands' output whole in the run's log |
| `-h`, `--help` | Help for the command |

`kit --version` prints the version.

## Related

- [Configuration](configuration.md)
- [Troubleshooting](troubleshooting.md)
- [Glossary](glossary.md)
