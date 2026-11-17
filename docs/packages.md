# Packages: install once, and your next Mac has it

One kit command installs a package and declares it in [kit-config](glossary.md). A Mac that
replaces this one installs it too, and `--shared` puts it on every Mac. It covers nine kinds,
from Homebrew casks to Claude Code skills. You'd want it for every install you mean to keep.

## Try it

```bash
# Install jq, and declare it for this Mac
kit brew add jq
```

kit installs jq, writes its line, then commits and pushes kit-config:

```console
  ● jq  installed · declared in laptop
  ● kit-config  committed and pushed laptop/declarations
```

The line lands in this Mac's declarations file, in sorted order:

```text
[homebrew formulae]
jq
```

```bash
# Install it here, and declare it for every Mac
kit cask add firefox --shared --note "browser"
# Uninstall jq, and take its line out
kit brew remove jq
```

## The kinds kit installs

Each kind has a section in a declarations file, and an `add` and a `remove` command.

| Command | Section | Named as |
|---|---|---|
| `kit brew` | `[homebrew formulae]` | `jq`, `owner/tap/tool` |
| `kit cask` | `[homebrew casks]` | `firefox` |
| `kit mas` | `[app store apps]` | `xcode@497799835` |
| `kit npm` | `[npm packages]` | `typescript@5` |
| `kit composer` | `[composer packages]` | `vendor/pkg:^4.0` |
| `kit gh` | `[github extensions]` | `owner/gh-tool` |
| `kit login-item` | `[macos login items]` | a bundle id |
| `kit claude-mcp` | `[claude mcp]` | a server's name |
| `kit claude-skill` | `[claude skills]` | `owner/repo/skill` |

A few kinds take more than the name they keep:

- `kit mas add` takes an app by name or by id. The line keeps both, so the list reads by name.
- `kit login-item add` takes an app by name, path or bundle id. The line keeps the bundle id.
- An npm package, a Composer package or a skill may carry a version. kit keeps it for installing.
- `kit claude-mcp add` declares a server Claude Code has already, as Claude Code has it.
  `kit claude-mcp off` removes the server but keeps its line.

## What happens

- **This Mac only, unless you say.** Without `--shared`, the line goes in this Mac's file, such
  as `laptop/declarations`. With `--shared`, it goes in `shared/declarations` for every Mac,
  and comes out of each Mac's own file. Other Macs install it at their next `kit apply`.
- **Removing undoes the install.** A formula is uninstalled, a login item stops opening at
  login, and an MCP server leaves Claude Code. If something installed still needs the package,
  kit changes nothing and says why.
- **`kit apply` installs what's missing.** It installs what's declared and not installed, and
  never removes anything.
- **A new Mac installs it all,** once what each kind needs is in place, such as npm for npm
  packages. [A new Mac from one line](new-mac.md) says what waits for what.

## Installs by hand show up

If you install a package with brew or npm yourself, kit-config doesn't have it. That's drift.
After its first day, `kit status` lists it under CONFIG:

```console
  CONFIG
  ▲ ffmpeg  installed, not declared · 2 days · → kit reconcile
```

`kit reconcile brew:ffmpeg --adopt` declares it, and `--remove` uninstalls it.

## Good to know

- **Removing an app asks about its files.** `kit cask remove` and `kit mas remove` ask at a
  terminal whether to keep what the app leaves behind. `--keep` and `--zap` answer without
  asking. Without a terminal, kit keeps it.
- **Trying something out?** `--temp` installs without declaring. The package stays quiet for 7
  days, then `kit reconcile` asks about it.
- **Private taps need a token.** kit checks a tap's download as you add it. If it's private, the
  line waits on a secret: see [Secrets from 1Password](secrets.md).
- **Installing isn't setting up.** A sign-in or a licence a package needs is a
  [step of your own](steps.md) or a step by hand, after the package.

## Reference

| Command or flag | What it does |
|---|---|
| `kit <kind> add <name>...` | Install, and declare for this Mac |
| `add --shared` | Declare for every Mac instead |
| `add --note "<why>"` | Keep why, after the name |
| `add --temp` | Install without declaring |
| `kit <kind> remove <name>...` | Uninstall, and undeclare |
| `remove --shared` | Needed to remove a shared line |
| `kit list [<kind>]` | List what's declared for this Mac |
| `kit why <name>` | Where it's declared, and what needs it |

Each of these takes `--json`, and `--help` says what it needs and what it changes.

## Related

- [Drift: keep or undo what changed by hand](drift.md)
- [Secrets from 1Password](secrets.md)
- [A new Mac from one line](new-mac.md)
