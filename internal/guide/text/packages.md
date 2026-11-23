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

A cask of Homebrew's own that kit can vet, its apps alone, installs with
`brew install --cask --adopt`, in `kit cask add`, `kit apply` and
`kit reconcile` alike. An app's version is its `CFBundleShortVersionString`,
or its `CFBundleVersion` when the short one doesn't start with a number
(Build 4215 is 4215). What an app already in /Applications that's the
cask's becomes, by the cask:

- One that updates itself, with no package, takes the app over as it is
  at the cask's major version or later, a newer copy too. A version of
  numbers without a dot (a build's, as Sublime Text's 4215) has no major:
  at or above the cask's, it's taken; below, the cask fails ("..., and
  can't tell whether this one is") till the app updates past it. A
  prerelease goes only to a channel's cask (`@beta` and the like) when one
  is at its place: another fails, naming the channel (`Thaw.app is
  3.0.0-beta.7 and the cask's is 2.1.0: kit takes over a prerelease only by
  its channel's cask, and thaw@beta is this one's`), or ending "none here
  is at its major version". An earlier major fails the cask,
  naming the `@` line that's the app's, if one is: `BBEdit.app is 14.6.2
  and the cask's is 16.1: kit takes over only a copy of the cask's major
  version or later, and bbedit@14 is this one's line`. Declare that line
  in its place, or leave the app by hand.
- Any other, with no package, takes the app over as it is at the cask's
  version. If
  Homebrew says it differs, kit installs the cask again, alone, with
  `brew install --cask --force`, which replaces the app, but only the same
  version (another build) or an older copy of the same major version, both
  dotted numbers (the cask's before any comma). Any other version (an
  earlier major, a newer copy, a prerelease, beside a cask of `latest`)
  fails the cask, saying why.
- One that installs through a package, whether it updates itself or not,
  is checked as a replace is, before its installer runs over the app.

An app there whose bundle id the cask doesn't name (a fork of the same
name), or the App Store's copy, even when the cask names its id, is left
alone too, and the cask fails, saying so. Ask the person which to keep.

Other casks (a tap's, one the catalogue lacks, any that puts more than
apps in place, every cask when the catalogue can't be read) install plain,
and fail as before beside an app already there, with Homebrew's own `It
seems there is already an App at`.

To keep the app, undeclare the cask
(`kit reconcile cask:<name> --undeclare`). With `--shared` that takes it
off every Mac: no answer keeps a shared cask off one Mac only, so ask the
person first. An app declared in `[app store apps]` comes back if it's
only moved to the Bin: `kit mas remove` it instead.

An app installed by hand that one of Homebrew's own casks installs, the
cask naming the app's bundle id, is drift while the cask isn't declared
for this Mac: state `by-hand`, its `detail` saying what it's installed as,
`installed by hand as Google Chrome.app`. Its choices:

- `kit reconcile cask:<name> --adopt [--shared]`: declare the cask, for
  this Mac or every Mac, and install it, Homebrew taking the app over.
  `kit cask add` does the same. `--shared` declares the cask for every
  Mac: each Mac with the app has Homebrew take it over, even one where it
  was left by hand, and every Mac without it installs it.
- `--leave`: leave it installed by hand, quiet for good, on this Mac only
  (`--shared` is refused), kept by the app's bundle id in `by-hand.json`
  in kit's state. Status counts it, `left`. Any other answer by its id,
  the app leaving the app folders, or a cask having it ends it;
  `kit reconcile --all` never asks about it.
- `--snooze`: quiet for 7 days.

There's no `--remove`: kit deletes no app it didn't install. Adopting may
replace the app, so ask the person before you adopt. Of the casks
installing an app: a prerelease app's, with a channel at its place, is the
channel nearest its version at its major (`1password@beta`,
`firefox@beta`, `thaw@beta`), never the cask of releases; with no channel
at its major, none. Else, when the one without `@` updates itself: that
one if the app's major version is the cask's or later (Chrome, Dropbox);
for an earlier major, the `@` line of that major (`bbedit@14`,
`firefox@esr`), never a channel's (beta, nightly and the like), or none. A
`latest` one that updates itself is offered, at any app version, only with
no numbered or LTS `@` line at its place (a channel doesn't count).
Otherwise the one whose version is nearest the app's, by the numbers it
starts with (`blender@lts` for Blender's LTS), a tie going to the one
without `@`. An app whose version can't be read gets no offer, but from
such a `latest` cask. The offer runs the install's own checks, so nothing
is offered that kit's install refuses, as a cask of `latest` that doesn't
update itself; Homebrew can still refuse it.
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
