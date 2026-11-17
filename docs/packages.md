# Packages: everything you install, declared

kit keeps a list of everything you install, in [kit-config](glossary.md). One command installs a
package and adds it to the list, so your next Mac gets it too. It covers nine kinds, from
Homebrew casks to Claude Code skills. You'd want it for every install you mean to keep.

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
# Install an app on every Mac, saying why
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
| `kit composer` | `[composer packages]` | `laravel/valet` |
| `kit gh` | `[github extensions]` | `owner/gh-tool` |
| `kit login-item` | `[macos login items]` | an app's bundle id |
| `kit claude-mcp` | `[claude mcp]` | a server's name |
| `kit claude-skill` | `[claude skills]` | `owner/repo/skill` |

`kit mas add` takes an app by name or by id. The line keeps both, so the list reads by name. npm
and Composer packages may carry a version, which kit keeps for installing.

## What happens

- **This Mac only, unless you say.** Without `--shared`, the line goes in this Mac's file, such
  as `laptop/declarations`. With `--shared`, it goes in `shared/declarations` for every Mac,
  and comes out of each Mac's own file.
- **Removing uninstalls too.** If something installed still needs the package, kit changes
  nothing and says why.
- **`kit apply` installs what's missing.** It installs what's declared and not installed, and
  never removes anything.

## Installs by hand show up

If you install a package with brew or npm yourself, kit-config doesn't have it. That's drift.
After its first day, `kit status` lists it under Config, beside anything declared and missing:

```console
  CONFIG
  ▲ jq  declared, not installed · → kit apply
  ▲ ffmpeg  installed, not declared · 2 days · → kit reconcile
```

`kit reconcile brew:ffmpeg --adopt` declares it, and `--remove` uninstalls it.

## Private taps wait for their token

A cask or formula from a private GitHub repository needs a token to download. `kit brew add`
and `kit cask add` check the download first. If it's private, kit puts the line in a section
held for the token Homebrew downloads with:

```text
[homebrew casks --secret HOMEBREW_GITHUB_API_TOKEN]
owner/tap/app
```

Declare the token as a secret before you add the package, or kit refuses the add. A shared line
needs the token declared for every Mac. kit sets the token for those installs only.

## On a new Mac

`kit apply` installs everything declared for that Mac. Some kinds wait for something first:

| Kind | Waits for |
|---|---|
| Homebrew | nothing: the boot installs Homebrew |
| npm, Composer | their program on kit's PATH, such as from a formula |
| A private tap's line | the token, once you sign in to your password manager |
| App Store apps | you, signed in to the App Store |
| Login items | the casks and App Store apps |
| MCP servers, skills | the data restore |

## Good to know

- **Removing an app asks about its files.** `kit cask remove` asks at a terminal whether to keep
  the files and settings the app leaves behind. `--keep` and `--zap` answer without asking.
  Without a terminal, kit keeps them.
- **Trying something out?** `--temp` installs without declaring. The package stays quiet for 7
  days, then `kit reconcile` asks about it.
- **MCP servers are declared as Claude Code has them.** Add the server in Claude Code first, then
  `kit claude-mcp add <name>`. `kit claude-mcp off` removes a server but keeps its line.
- **Installing isn't setting up.** A sign-in or a licence a package needs is a
  [step of your own](steps.md) or a step by hand, after the package.

## Reference

| Command | What it does |
|---|---|
| `kit <kind> add <name>...` | Install, and declare for this Mac |
| `--shared` | Declare for every Mac instead |
| `--note "<why>"` | Keep why, after the name |
| `--temp` | Install without declaring |
| `kit <kind> remove <name>...` | Uninstall, and undeclare |
| `kit list [<kind>]` | List what's declared for this Mac |
| `kit why <name>` | Where it's declared, and what needs it |

Each of these takes `--json`, and `--help` says what it needs and what it changes.

## Related

- [Drift: keep or undo what changed by hand](drift.md)
- [Secrets from 1Password](secrets.md)
- [A new Mac from one line](new-mac.md)
