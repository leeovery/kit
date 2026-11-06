# Settings

Settings are lines in list sections too, each a setting and its value. The
way to declare one: change it on this Mac first, the way you would by hand,
then add it, and kit declares it as this Mac has it.

| Kind | Section | Named as |
| --- | --- | --- |
| defaults | `[macos settings]` | domain:key, as com.apple.dock:autohide |
| power | `[power settings]` | source:setting, as charger:sleep |
| git-config | `[git config]` | a key, as pull.rebase |
| backup-exclusion | `[backup exclusions]` | a folder |
| spotlight-exclusion | `[spotlight exclusions]` | a folder |

`kit defaults add com.apple.dock:autohide` reads the value this Mac has and
declares it, with `--shared` for every Mac and `--note` saying what it is.
A setting of the current host only starts `currentHost:`. Lines are written
as `defaults write` and `pmset` take them; read the section when unsure.

Removing differs by kind: `kit defaults remove` unsets the setting, so
macOS's default comes back; `kit power remove` undeclares it and leaves the
value as it is. Read the command's `--help` first.

## macOS's settings, apps' settings

A macOS setting, in one of Apple's domains, is a defaults line. An app's
own settings, in its domain, are kit prefs': it captures every domain that
isn't Apple's or the system's, whole, and restores it on a new Mac (see
`kit agent prefs`). Declaring an app's setting as a defaults line as well
has two of kit's mechanisms writing it.

Some settings only take effect once something restarts (the Dock, Finder,
a logout). A defaults line sets the value, nothing more: what has to
restart for it is a step of your own, or a note saying so.

## Features

`kit feature on <feature>` switches on a piece of kit with nothing to list:
apps' settings (prefs), remote login, file sharing, Touch ID for sudo.
`kit feature --help` lists them. They're kit-config's `[features]`.

## On a new Mac

Settings are set as declared by `kit apply`, in no order with the rest, but
git's settings, which come after the secrets they can sign with. A setting
changed by hand afterwards is drift, which `kit reconcile` settles: adopt
the new value (`kit reconcile <id> --adopt`), or put back the declared one
(`kit reconcile <id> --revert`).

A setting that needs an app installed, or a file in place, isn't promised
either on a new Mac: what depends on another step is a step of your own,
with `--after`.
