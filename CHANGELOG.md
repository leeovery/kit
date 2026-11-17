# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
