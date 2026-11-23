# Commands: every kit command, and what it's for

This page lists every command, grouped much as `kit --help` groups them. Each line links to the page
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
| `kit terminal` | The terminal a new Mac's boot hands over to | [A new Mac](new-mac.md) |
| `kit bootstrap` | Set up a new Mac: the install line runs it | [A new Mac](new-mac.md) |
| `kit setup` | Set up a Mac you already use | [Your first kit-config](first-config.md) |
| `kit agent` | Instructions for an agent working through kit | [Agents](agents.md) |

`kit setup` creates kit-config (`--create`) or joins yours (`--join`), declares the Mac as it is, then runs `kit apply`.
`kit machine add` adds a Mac to kit-config, empty or as a copy of another (`--from`).
`kit terminal ghostty` sets the terminal, and `kit terminal --none` takes it out.
`kit nightly at` shows when the nightly run falls due, and `kit nightly at 04:30` moves it.
`kit agent <topic>` prints one topic of the guide, and `kit agent --help` lists the topics.

## Packages

Each of these has `add` and `remove`. `add` installs and declares, and `remove` uninstalls and
undeclares. `kit claude-mcp add` is the exception: it installs nothing, and declares servers
Claude Code has already.

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

`kit claude-mcp` also has `on` and `off`. `on` installs a declared server. `off` removes a
server from Claude Code, and keeps its line.

Five of these also run a [copied line](glossary.md): an install command from a README, with kit
in front. The program runs the line as given, then kit declares what it installed. An uninstall
line undeclares what it took off.

| Copied install | Copied uninstall |
|---|---|
| `kit brew install` | `kit brew uninstall` |
| `kit npm install -g` | `kit npm uninstall -g` |
| `kit mas install` | `kit mas uninstall` |
| `kit gh extension install` | `kit gh extension remove` |
| `kit composer global require` | `kit composer global remove` |

In the line, only `--shared` and `--note` are kit's flags. kit's other flags go before the kind,
as in `kit --plain brew install jq`. Each line takes the short words its program takes, such as
`kit npm i -g`. [Packages](packages.md) covers when to use which.

## Settings

Each of these has `add` and `remove`. For the first three, `add` declares a setting with the
value this Mac has. The two exclusion commands change the Mac: `add` leaves the folder out,
then declares it.

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
| `kit prefs` | Apps' settings, saved and restored | [Apps' settings](apps-settings.md) |
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
Because `kit nightly <job>` runs a job by name, no job can be named `add`, `remove` or `at`.

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

`kit --version`, or `kit -v`, prints the version.

## Related

- [Configuration](configuration.md)
- [Troubleshooting](troubleshooting.md)
- [Glossary](glossary.md)
