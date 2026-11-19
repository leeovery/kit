# Configuration: what kit-config holds, file by file

[kit-config](glossary.md) is a private git repository. It says what each of your Macs should
have, and kit reads it on every run. This page describes each file in it: `kit.toml`, the
declarations files, the `home` folders and the step folders. kit writes most of it for you.
Read this page when you edit a file by hand.

## The layout

A kit-config for two Macs, `laptop` and `studio`, looks like this:

```text
kit.toml
shared/
  declarations
  home/.zshrc
laptop/
  declarations
  home/.config/tool/conf
  steps/fonts/run
studio/
  declarations
```

| Path | What it holds |
|---|---|
| `kit.toml` | Your Macs, and settings for every Mac |
| `shared/` | What every Mac has |
| `<mac>/` | What one Mac has, besides the shared folder |
| `declarations` | Sections of declarations, one file a folder |
| `home/` | Files linked into your home folder |
| `steps/<name>/` | A step of your own: its `run` script and data |

A Mac reads `shared/` and the folder named after it. The name comes from `kit machine`, and
must be one of the Macs `kit.toml` names. A folder with a declarations file for a Mac
`kit.toml` doesn't name is an error.

## kit.toml

```toml
format = 1
minimum_kit = "0.1.0"
primary = "laptop"
nightly_at = "03:00"
terminal = "ghostty"

[macs.laptop]
description = "MacBook Pro"

[macs.studio]
description = "Mac Studio"
from = "laptop"
```

kit refuses a setting it doesn't know, so a typo shows as an error. kit.toml needs at least
one `[macs.<name>]` table.

| Field | Meaning | Set by |
|---|---|---|
| `format` | Required. The config format, `1` today | You |
| `minimum_kit` | The oldest kit that reads it, such as `0.1.0` | You |
| `primary` | Required. One of the Macs below | You |
| `nightly_at` | When nightly jobs fall due, `03:00` unless set | `kit nightly at` |
| `terminal` | The terminal a new Mac installs, `ghostty` only | `kit terminal` |

A kit older than `minimum_kit` stops and tells you to update kit. A format newer than kit
reads does the same. Without `terminal`, the boot carries on in macOS's own Terminal.

`kit terminal` and `kit nightly at` show their setting, or set it given a value. Each sets
one line in place, keeps the rest of `kit.toml` as it is, then commits and pushes.
`kit terminal --none` takes the line out.

### A table for each Mac

Each Mac has a table, `[macs.<name>]`. A name is lower-case letters, digits and hyphens, such
as `laptop`. A Mac can't be called `shared`. `kit machine add` writes the table for you.

| Field | Meaning |
|---|---|
| `description` | What the Mac is, such as `"Mac Studio"` |
| `from` | The Mac this one started as a copy of |
| `host_key` | Its SSH host key: `kit apply` records it |
| `local_hostname` | Its name on the local network: `kit apply` records it |

`from` names the Mac that the [data restore](your-data.md) copies from. A `from` that names
no Mac in kit.toml counts for nothing. `kit apply` records `host_key` and `local_hostname` on
a Mac with the `remote-login` feature on. A Mac restored from it uses them to find it and
trust it.

## The declarations file

A declarations file is sections, each a header in brackets, then a line for each thing:

```text
# Packages for every Mac
[homebrew formulae]
# for scripts that read JSON
jq
ripgrep   # search

[macos settings]
com.apple.dock tilesize -int 60
```

- **A note** follows a line, after a space and `#`. kit keeps it with the line.
- **Comments directly above a line**, with no blank line between, belong to that line. kit
  moves them with it when it sorts.
- **Comments before the first section** are fine. Any other line there is an error.
- **A header appears once a file.** `[homebrew formulae]` and
  `[homebrew formulae --secret NAME]` are two headers, so a file may hold both.
- **A thing is declared once,** in the shared file or a Mac file, not both. A folder in
  `[paths]` is the one exception, explained after this list.

kit keeps the sections of packages and settings sorted, all but `[claude mcp]`. The others
keep the order you wrote. kit puts a new line in `[paths]` or `[restore]` last, and a new line
anywhere else in its place by name. A kind is looked after only while a file has its section,
even an empty one.

The PATH runs in this order: the Mac file's `[paths]`, then the shared file's, then the
system's folders. Each folder appears once, in the first place it's listed. If a Mac file
lists a folder the shared file has, that folder comes first on that Mac. kit's PATH and your
shell's both follow this order.

### The sections

These are the sections, in the order kit writes them. A command adds each line for you,
though the ordering options on a `[manual]` line are written by hand.

| Section | A line holds | Command |
|---|---|---|
| `[features]` | A feature's name | `kit feature` |
| `[backups]` | `time-machine` or `arq` | `kit backup` |
| `[paths]` | A directory on the PATH | `kit path` |
| `[homebrew formulae]` | A formula | `kit brew` |
| `[homebrew casks]` | A cask | `kit cask` |
| `[app store apps]` | An app, as `name@id` | `kit mas` |
| `[npm packages]` | A package | `kit npm` |
| `[composer packages]` | A package | `kit composer` |
| `[github extensions]` | `owner/gh-tool` | `kit gh` |
| `[macos login items]` | A bundle id | `kit login-item` |
| `[claude mcp]` | A server, then its options | `kit claude-mcp` |
| `[claude skills]` | `owner/repo/skill` | `kit claude-skill` |
| `[secrets]` | What it fills, then where it's kept | `kit secret` |
| `[git config]` | A key, then its value | `kit git-config` |
| `[macos settings]` | A setting in `defaults write` form | `kit defaults` |
| `[power settings]` | A setting in `pmset` form | `kit power` |
| `[backup exclusions]` | A folder | `kit backup-exclusion` |
| `[spotlight exclusions]` | A folder | `kit spotlight-exclusion` |
| `[restore]` | A path, then `--skip` options | `kit restore` |
| `[steps]` | A name, what it does, options | `kit step` |
| `[manual]` | A name, what to do, options | `kit manual` |
| `[checks]` | A name, then a command | `kit check` |
| `[hourly]` | A name, then a command | `kit hourly` |
| `[nightly]` | A name, then a command | `kit nightly` |

The four `[prefs …]` sections belong to apps' settings, which get a
[page of their own](apps-settings.md).

A few sections take more after the header:

- **`[claude mcp ~/Code/site]`** holds servers for one project folder.
- **`[secrets op://vault/item]`** holds secrets kept in one item. Each line still starts with
  what it fills, but gives only the field, such as `API_TOKEN GitHub/token`. `--as tokens`
  after the item names the block, for `kit secret add --in tokens`.

### Held sections

A section of packages can hold its lines back until something is in place. The header says
what for:

```text
[homebrew formulae --secret HOMEBREW_GITHUB_API_TOKEN]
owner/tap/tool

[homebrew casks --after cask:firefox]
firefox-helper
```

| Option | The lines wait for |
|---|---|
| `--secret NAME` | The secret that fills `NAME` to sync |
| `--after step` | A step, or a thing such as `cask:firefox` |

The package sections take both, each as often as you like, but `[claude mcp]` takes neither.
`[features]` takes `--after` only. No other section takes either. A secret is named by what it fills: an environment
variable, or a file path from `~` or `/`. [Secrets from 1Password](secrets.md) says when a
private tap needs a section like this.

### Options on a line

A line in `[steps]`, `[manual]` or `[backups]` can say where it falls in a run:

```text
[steps]
fonts "Install the fonts" --after cask:font-tool
tool "Set the tool up" --secret TOOL_TOKEN --sudo

[manual]
app-store "Sign in to the App Store" --before mas
```

| Option | Meaning |
|---|---|
| `--after` | Wait for a step, or a thing such as `cask:firefox` |
| `--before` | Come before a step |
| `--secret` | Wait for a secret; a variable is set for the script (`[steps]`) |
| `--sudo` | Needs an administrator's password (`[steps]`) |
| `--group` | The group a step by hand is in (`[manual]`) |

`--after`, `--before` and `--secret` may each appear as often as you like. `kit manual add`
writes no `--after` or `--before`: add those to a `[manual]` line by hand.

A `[manual]` line may also end with `--test` and a command that says whether it's done.
Indented lines under it are what to do, in order. A step of your own keeps its `run` script in
`<mac>/steps/<name>/`. [Steps](steps.md) covers both kinds of step.

## The home folders

Every file in `shared/home` or `<mac>/home` is linked into your home folder at the same path.
`shared/home/.zshrc` is linked at `~/.zshrc`. The folder is the declaration: there is no list
beside it.

- kit links a file at a time, never a folder whole.
- A file git ignores in kit-config is never linked.
- A file in both home folders is an error.

`kit file add` moves a file in and links it back. [Linked files](linked-files.md) covers the
rest.

## Where kit-config lives

kit looks for kit-config in the first of these that's set:

| If | kit-config is at |
|---|---|
| `KIT_CONFIG` is set | That folder, an absolute path |
| `XDG_CONFIG_HOME` is an absolute path | `$XDG_CONFIG_HOME/kit` |
| Neither | `~/.config/kit` |

A relative `KIT_CONFIG` is an error. On a new Mac, the boot clones kit-config into place for
you. If kit finds no `kit.toml` there, it stops: see [Troubleshooting](troubleshooting.md).

## kit-config stays private

kit-config names your Macs, your apps and where your secrets are kept, so it must never be
public. The boot reads it from GitHub, and refuses one that's public. `kit status` checks it on
every run, and shows `kit-config  private` under CONFIG once GitHub says so.

| If kit-config is | `kit status` |
|---|---|
| Public on GitHub | Says so, with the `gh repo edit` command that fixes it |
| Not at a github.com URL kit reads | Can't prove it private, and lists the URL forms it reads |

## Related

- [Packages: install once, and your next Mac has it](packages.md)
- [Commands](commands.md)
- [Glossary](glossary.md)
