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
App Store by hand leaves drift, and a new Mac without it: use kit's add, or
kit in front of the line (below).

`kit why <name>` says where a package is declared, whether it's installed,
and what needs it; `kit list` lists everything declared for this Mac.

## A copied line

An install command copied for the package's own program, as a README gives
it, runs with kit and the kind in front; its uninstall undoes it:

| Line | Its uninstall's verb | Names |
| --- | --- | --- |
| `kit brew install` | `uninstall` | formulae and casks |
| `kit npm install -g` | `uninstall -g` | packages |
| `kit mas install` | `uninstall` | App Store ids |
| `kit gh extension install` | `remove` | owner/gh-name |
| `kit composer global require` | `remove` | packages |

The program runs the line as given, its output shown; Homebrew's and mas's
sudo ask kit for the password, as kit's own installs do. Then kit declares
each name the line installed, as the kind's add would (`kit brew add`), for
this Mac or with `--shared` every Mac, and commits and pushes once:

- kit-config records names, never the line's flags: the force of
  `brew install --force jq` is this install's alone. A version or
  constraint is kept as typed, as add keeps it (`typescript@5`,
  `vendor/pkg:^2`); a tap's name with its tap. A name brew installs as a
  cask is declared as one, even from `brew install <name>`.
- kit's own flags in the line are `--shared` and, for an install,
  `--note "<why>"`; its others, as `--json`, go before the kind's name,
  never in the line, which passes them to the program.
- Before anything runs, each name is judged as add judges it, and one kit
  can't declare refuses the whole line: a file's path or an address, npm
  without `-g`, gh's extension without its owner, mas given anything but
  ids, a cask or an App Store app needing the password with no terminal to
  ask at.
- A name already installed is declared, "already installed"; one the line
  didn't install isn't. A dry run (brew's `-n` or `--dry-run`, npm's and
  Composer's `--dry-run`, mas uninstall's `--dry-run`) declares nothing.
- A plain line, whose flags are only brew's cask flag and those changing
  only brew's output, gets `--adopt` when every cask it installs is one kit
  vets, formulae in it or not, as kit's own cask installs do: an app there
  that's the cask's is taken over, or an older copy of its major version
  replaced. A line with any other flag, or a tap's cask, runs as given.
- An uninstall undeclares only what's no longer installed, by whichever
  form it's declared: brew's short or full name, npm's and Composer's with
  or without the version, mas by id, gh by the repository or the command.
  One declared for every Mac needs `--shared`, else the whole line is
  refused. Brew's zap flag in the line (`brew uninstall --zap <name>`)
  removes an app's saved settings too, as `kit cask remove --zap` does; a
  mas line keeps them.

Which to use: when the person gives you a line from a README, put kit in
front of it; when you're choosing the package yourself, use the kind's
add, as `kit brew add`.

Known limits: npm's `add`, `remove` and `rm`, and brew's `remove` and
`rm`, reach kit's own `kit npm add`, `kit npm remove` and
`kit brew remove`, which refuse the program's flags (`-g`, brew's cask
flag): use `install` and `uninstall`, or kit's own add and remove without
them. `kit mas uninstall` looks each id up in the App Store first, so an
app it no longer has needs `kit mas remove` by its declared name.

## Apps installed by hand

A cask of Homebrew's own that kit can vet, its apps alone, installs with
`brew install --cask --adopt`, in `kit cask add`, `kit apply` and
`kit reconcile` alike. An app's version is its `CFBundleShortVersionString`,
or its `CFBundleVersion` when the short one doesn't start with a number
(Build 4215 is 4215). What an app already in /Applications that's the
cask's becomes, by the cask:

- One that updates itself, with no package, takes the app over as it is at
  the cask's major version or later, a newer copy too. A build number
  (numbers without a dot, as Sublime Text's 4215) is never told against a
  dotted version ("..., and can't tell whether this one is"); against a
  build number, at or above the cask's it's taken, below it the cask fails
  till the app updates past it. An earlier major fails the cask, naming the
  `@` line that's the app's, if one is: `BBEdit.app is 14.6.2 and the cask's
  is 16.1: kit takes over only a copy of the cask's major version or later,
  and bbedit@14 is this one's line`. Declare that line in its place, or
  leave the app by hand.
- A line (`bbedit@14`, `firefox@esr`: an `@` cask that isn't a channel's)
  takes over only a copy of its own line, as the offer would pick it:
  `BBEdit.app is 16.1 and the cask's is 14.6.9: kit takes over by a line
  only a copy of that line, and bbedit is this one's`, or ending "no cask
  here is this one's".
- A prerelease goes only to a channel's cask when one is at its place, the
  channel nearest its version at its major. Another cask fails, naming the
  channel (`Thaw.app is 3.0.0-beta.7 and the cask's is 2.1.0: kit takes over
  a prerelease only by its channel's cask, and thaw@beta is this one's`), or
  ending "none here is at its major version", or, with two as near,
  "lamp@beta and lamp@nightly are as near it". A version is a prerelease's
  when a channel's word marks it as a word of its own (alpha, beta, rc, dev,
  nightly, canary, preview, tip, snapshot, eap, daily and the rest, pre,
  insider), or as 3.0b2.
- Any other, with no package, takes the app over as it is at the cask's
  version. If Homebrew says it differs, kit installs the cask again, alone,
  with `brew install --cask --force`, which replaces the app, but only the
  same version (another build) or an older copy of the same major version,
  both dotted numbers (the cask's before any comma). Any other version fails
  the cask, saying why: "this one is newer", "this one is a prerelease",
  "the cask's is a prerelease", "latest says no version", "can't tell
  whether this one is", or nothing more for an earlier major.
- One that installs through a package, whether it updates itself or not, is
  checked as a replace is, before its installer runs over the app.

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
at its major, or two as near, none. A channel whose version has no major
(`ghostty@tip`, `sublime-text@dev`) is at no app's major, so a prerelease
beside it gets no offer, a known limit. Else, when the one without `@`
updates itself: that one if the app's major version is the cask's or later
(Chrome, Dropbox); for an earlier major, the `@` line of that major
(`bbedit@14`, `firefox@esr`), never a channel's (beta, nightly and the
like), or none. A `latest` one that updates itself is offered, at any app
version, only with no numbered or LTS `@` line at its place (a channel
doesn't count). Otherwise the one whose version is nearest the app's, by
the numbers it starts with (`blender@lts` for Blender's LTS), a tie going
to the one without `@`. An app whose version can't be read gets no offer,
but from such a `latest` cask. The offer runs the install's own checks, so
nothing is offered that kit's install refuses, as a cask of `latest` that
doesn't update itself; Homebrew can still refuse it.
Casks from taps, disabled casks, and apps whose cask can't be told, as
Homebrew's catalogue can't be read, are never offered.

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
