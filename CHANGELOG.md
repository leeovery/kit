# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0] - 2026-11-19

Changes from other Macs reach each Mac within the hour, because runs now bring in kit-config's remote commits first.

✨ Added
- `kit apply`, `kit status`, `kit reconcile` and the hourly nightly run first bring in kit-config's remote commits — what another Mac pushed is applied within the hour, and uncommitted edits are left as they are.
- A run that can't read the config because another Mac pushed a fix now brings in the remote's commits and reads it again, instead of failing.
- `kit status` reports how many commits were brought in, and says why none could be, quiet on its first day.

🔧 Changed
- Two kits at once no longer interleave commits, pulls and pushes in the config repository — one waits its turn for the other.
- Pushing and pulling now refuse when the remote's history was rewritten and dropped a commit this Mac has — bring it in by hand.
- Pulling now refuses when the remote's commits would overwrite a file that git ignores on this Mac — move it aside first.
- A remote that doesn't answer in time now reports a timeout rather than a generic failure.

## [0.2.0] - 2026-11-19

Set up a Mac you already use with one command, `kit setup`, which makes or joins your kit-config and declares everything on the Mac as it is.

💥 Breaking
- The `prefs_repo` setting in kit.toml is no longer accepted, and kit stops reading kit.toml while it's there — take the line out, as apps' settings are now saved on kit-config's own `prefs` branch.
- A Mac can no longer have `shared` or `add` as its name when you add or boot it, as those are reserved.

✨ Added
- `kit setup` brings a Mac in use onto kit in one run — signs in to GitHub, creates or joins kit-config, names the Mac, declares its apps, settings, git config, features and backups, then commits, pushes and runs `kit apply`.
- `kit setup` asks which folders a new Mac brings back, whether to save apps' settings nightly, which shell files kit-config keeps and whether to install what the config declares, each with its own flag (`--restore`, `--prefs`, `--link`, `--apply` and their `--no-` forms) for use without a terminal.
- The new-Mac boot lets you create a kit-config or join one of your private repositories when you have none named kit-config, and starts an empty one with your Mac and chosen terminal.
- `kit terminal` shows or sets the terminal a new Mac's boot hands over to, and `--none` takes it out.
- `kit nightly at` shows or sets when the nightly run falls due.
- Bare `kit` on a Mac that isn't set up, or whose setup stopped part-way, offers to run or carry on `kit setup`.
- `kit agent in-use` guide tells agents how to run `kit setup` for a person.
- A new kit-config declares Homebrew's folders in the shared `[paths]`, so kit's runs find brew.

🔧 Changed
- Apps' settings are saved on a `prefs` branch of kit-config itself rather than a separate repository, and the boot clones only kit-config's own branch.
- Uninstalling an app now says its saved settings weren't looked at when the settings store can't be read, instead of silently skipping them.
- `kit status` marks the config repository as needing you while part of it does, and says plainly what to do when it isn't proven private.
- Declaring Homebrew formulae and casks from tapped packages checks whether a private download needs a GitHub token without tapping or updating, and checks GitHub's package registry anonymously as Homebrew does.
- The save-panel expanded state and the menu-bar clock's date setting are now watched as your own choices rather than ignored as noise.

🐛 Fixed
- Pushing kit-config no longer reports "nothing changed" when it couldn't tell what's waiting to be pushed; it asks for attention instead.
- Long or multi-line errors no longer leave stray lines behind on in-place screens.
- Tall question cards in `kit apply` no longer break the log pane.
- Signing in with a QR code fits short terminal windows, keeping the code and its expiry visible.
- Cancelling a key read at the terminal no longer swallows the next question's keys.

## [0.1.2] - 2026-11-17

🔧 Changed
- Bootstrap now installs kit itself through Homebrew last, after kit-config is cloned and its files linked — a Homebrew trust file that kit-config links is no longer blocked by one Homebrew writes first.

🐛 Fixed
- Private apps from a tap already added are now found when the tap is newer than Homebrew's last fetch — the tap is updated once per run before its contents are checked.

## [0.1.1] - 2026-11-17

Install kit on a new Mac through Homebrew, so it stays up to date.

🔧 Changed
- The install script runs kit from a temporary download that is removed afterwards, instead of leaving it in `~/.local/bin` — kit itself comes from Homebrew.
- Bootstrap now installs kit through Homebrew and carries on from Homebrew's kit, unless you run a build of your own with `KIT_FROM`.
- The hourly launch now looks for kit after Homebrew's formulae are applied, so it picks up a kit installed earlier in the same run.
- Bootstrap errors and help now tell you to paste the install line into Terminal again, instead of running `kit bootstrap` again.

✨ Added
- A "kit" step in the boot installs or upgrades kit through Homebrew, and upgrades it when Homebrew's copy is older than the running kit.
- Install kit with `brew install leeovery/tools/kit`.

🐛 Fixed
- The hourly launch now works when kit is applied by a kit other than the one on kit's PATH, instead of being skipped.

## [0.1.0] - 2026-11-17

Initial release.
