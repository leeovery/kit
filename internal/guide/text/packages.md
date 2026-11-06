# Packages

A package is a line in a list section of a declarations file, of a kind kit
installs: `kit --help` lists the kinds under Packages.

| Kind | Section | Named as |
| --- | --- | --- |
| brew | `[homebrew formulae]` | a formula, or owner/tap/formula |
| cask | `[homebrew casks]` | a cask, or owner/tap/cask |
| mas | `[app store apps]` | an App Store app, by name or id |
| npm | `[npm packages]` | a global npm package |
| composer | `[composer packages]` | a global Composer package |
| go | `[go tools]` | a module path |
| gh | `[github extensions]` | owner/repo |
| login-item | `[macos login items]` | an app |
| claude-mcp | `[claude mcp]` | a server Claude Code has |
| claude-skill | `[claude skills]` | owner/repo and the skill in it |

tmux's plugins are declared in tmux's own config, which kit reads.

## Adding and removing

`kit brew add jq` installs and declares in one go, the line in its sorted
place in this Mac's declarations file (every Mac's with `--shared`), and
commits and pushes the change. `--note` keeps why after the name; comments
right above a line are its notes too. `--temp` installs without declaring,
for a throwaway: it's quiet for 7 days, then `kit reconcile` asks.

`kit brew remove jq` uninstalls and undeclares. When something installed
still needs it, nothing changes, and kit says why. Removing an app (a cask
or an App Store app) asks whether to keep its files and settings, at a
terminal; `--keep` keeps them and `--zap` removes them without asking.
Ask the person before you remove anything.

What's installed but not declared, or declared but not installed, is drift:
`kit status` lists it, quiet for its first day, and `kit reconcile` settles
it, as in `kit reconcile brew:jq --adopt`. Installing with brew, npm or the
App Store by hand leaves drift, and a new Mac without it: use kit's add.

`kit why <name>` says where a package is declared, whether it's installed,
and what needs it; `kit list` lists everything declared for this Mac.

## Private downloads

A Homebrew package from a private repository needs a token to download.
`kit brew add` and `kit cask add` check, and put its line in the section
held for that secret, as in `[homebrew casks --secret TOKEN]`, which every
Mac it's declared for must declare.

## Held sections

A list section's header can hold its lines back: `--after` a step or a
step's thing, `--secret` a secret they need, as in
`[homebrew formulae --after <step>]`. Its lines install once those are in
place, the rest of the list meanwhile. Move a line into one by hand, and
commit it, when a package needs something kit installs first.

## On a new Mac

- Homebrew is installed by the boot; its formulae and casks are much of a
  new Mac's run.
- npm, Composer and Go tools wait till their program is on kit's PATH,
  whatever installs it.
- App Store apps need someone signed in to the App Store.
- Login items come after the casks and App Store apps.
- An app kit installs is never opened by the install: its first launch is
  still to come. Its settings are restored after every cask and App Store
  app is in (see `kit agent prefs`).
- A package's own set-up beyond installing it (a sign-in, a licence, a
  plugin it fetches) isn't the install's: it's a step of your own or a step
  done by hand, ordered after the package (`--after cask:<name>`).
