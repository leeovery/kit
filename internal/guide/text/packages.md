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
| gh | `[github extensions]` | owner/repo |
| login-item | `[macos login items]` | an app |
| claude-mcp | `[claude mcp]` | a server Claude Code has |
| claude-skill | `[claude skills]` | owner/repo and the skill in it |

## Adding and removing

`kit brew add jq` installs and declares in one go, the line in its sorted
place in this Mac's declarations file (every Mac's with `--shared`), and
commits and pushes the change. `--note` keeps why after the name; comments
right above a line are its notes too. `--temp` installs without declaring,
for a throwaway: it's quiet for 7 days, then `kit reconcile` asks.

`kit brew remove jq` uninstalls and undeclares. When something installed
still needs it, nothing changes, and kit says why. Ask the person before
you remove anything.

Removing an app, as `kit cask remove` does, asks at a terminal whether to
keep its files and settings; without one, they're kept. `--keep` keeps them
and `--zap` removes them, without asking.

What's installed but not declared, or declared but not installed, is drift:
`kit status` lists it, quiet for its first day, and `kit reconcile` settles
it, as in `kit reconcile brew:jq --adopt`. Installing with brew, npm or the
App Store by hand leaves drift, and a new Mac without it: use kit's add.

`kit why <name>` says where a package is declared, whether it's installed,
and what needs it; `kit list` lists everything declared for this Mac.

## Apps installed by hand

Every cask install runs `brew install --cask --adopt`, in `kit cask add`,
`kit apply` and `kit reconcile` alike: an app already in /Applications
that's the cask's is taken over in place. If Homebrew says it differs from
the cask's download, kit installs the cask again, alone, with
`brew install --cask --force`, which replaces the app, but only an older
copy of the same major version: the app's `CFBundleShortVersionString` and
the cask's version, before any comma, both dotted numbers, the app's lower.
An app of any other version (another major version, a newer copy, a
prerelease, or beside a cask of `latest`), an app there whose bundle id the
cask doesn't name (a fork of the same name), or the App Store's copy, is
left alone, and the cask fails, saying so: ask the person which to keep. To
keep the app, undeclare the cask (`kit reconcile cask:<name> --undeclare`).

An app installed by hand that one of Homebrew's own casks installs, the
cask naming the app's bundle id, is drift while the cask isn't declared
for this Mac: state `by-hand`, its `detail` saying what it's installed as,
`installed by hand as Google Chrome.app`. Its choices:

- `kit reconcile cask:<name> --adopt [--shared]`: declare the cask, for
  this Mac or every Mac, and install it, taking the app over. `kit cask add`
  does the same.
- `--leave`: leave it installed by hand, quiet for good, on this Mac only
  (`--shared` is refused). Any other answer, or the app going, ends it.
- `--snooze`: quiet for 7 days.

There's no `--remove`: kit deletes no app it didn't install. Adopting may
replace the app, so ask the person before you adopt. Of the casks
installing an app, the one without `@` is offered if it updates itself,
whatever the app's version; else the one whose version is nearest the
app's, by the numbers it starts with (`blender@lts` for Blender's LTS), a
tie going to the one without `@`; and never one whose install would leave
the app and fail, as a cask of `latest` that doesn't update itself would.
Casks from taps, and apps whose cask can't be told, as Homebrew's catalogue
can't be read, are never offered.

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
- npm and Composer wait till their program is on kit's PATH, whatever
  installs it.
- App Store apps need someone signed in to the App Store. That's the
  person's alone, in the App Store app: ask them, as you can't.
- Login items come after the casks and App Store apps.
- kit doesn't open an app it installs, though a few installers do, and the
  password manager's sign-in card opens that app: an app's first launch is
  likely still to come. Its settings are restored after every cask and App
  Store app is in (see `kit agent prefs`).
- A package's own set-up beyond installing it (a sign-in, a licence, a
  plugin it fetches) isn't the install's: it's a step of your own or a step
  done by hand, ordered after the package (`--after cask:<name>`).
