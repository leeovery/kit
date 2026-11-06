# Steps of your own

A step of your own is set-up no kind covers, as a script in kit-config. Use
one last: a package, a setting, a linked file, captured settings, a secret
or a step done by hand comes first, when one fits.

## Its line and its folder

`kit step add <name> "<what it does>"` declares it in this Mac's
declarations file (every Mac's with `--shared`) and writes its folder,
`<mac>/steps/<name>/` (`shared/steps/<name>/`), with `run` in it, a template
to fill in. The line in `[steps]` reads as the command wrote it:

    name "what it does" --after cask:firefox --secret TOKEN --sudo

- `--after` a step it's applied after, by its name as `kit status` shows
  it, or one of a step's things, as in cask:firefox: it waits till that
  thing is installed, or its step has finished. A kind this Mac doesn't
  use is no wait at all.
- `--before` a step that's applied after it.
- `--secret` a secret it needs, by what it fills, declared in a secrets
  section: its apply waits till the secret has synced, and runs with it set
  when it's a variable.
- `--sudo` when its apply needs an administrator's password.
- `--note` why it's there, kept after the line.

Each of `--after`, `--before` and `--secret` is given as often as wanted.
Change the line by hand to add one later, and commit it; `kit status` says
when a line doesn't read, or names what isn't a step.

The folder's other files are its data. `run` must stay executable.

## How kit runs it

- `run check`, in its folder, with nothing on its input, for at most a
  minute: exit 0 when it's done, 1 when it isn't, 3 when it can't be done
  yet. Its first line says how it stands, as `kit status` shows it. Any
  other exit, or a check that doesn't answer, is a failure.
- When it isn't done, `run apply`, for at most 15 minutes, then `run check`
  again: the step is done only when that check says so. When apply fails,
  its last line says why.
- A check that exits 3 isn't applied in that run, and isn't failed: the
  next run tries again. Say what's missing in its first line.

A check runs on every `kit status` and in every nightly run: it changes
nothing, opens nothing and asks no one. One that must go online says so in
a comment.

## What it can rely on

Its environment is kit's, never your shell's: HOME, the PATH kit-config's
`[paths]` makes (then the system's), USER, LOGNAME, SHELL, TMPDIR, LANG,
LC_ALL, SSH_AUTH_SOCK, the secrets store's own variables, Homebrew told not
to update itself, git told not to ask for a password, and the secrets it
names that are variables. A token in your shell is not there: declare it
with `kit secret add`, and name it with `--secret`.

On a new Mac its check can run before its secrets have synced, without
them; kit holds its apply till they're in place. So a check that needs a
secret it hasn't got says not done (exit 1), never 3, or the step isn't
applied in that run.

What it names with `--after` is there when it runs; nothing else is
promised. A command it runs from a package is a thing to name, as in
brew:jq. An app it drives is a cask or App Store app to name, and prefs,
so that the app's settings are restored first (`--after prefs`, where kit
prefs is on).

`#!/bin/bash` is macOS's bash 3.2 on a new Mac: no mapfile, no associative
arrays, no `${var,,}`. For a newer one, name Homebrew's (`--after brew:bash`)
and start the script `#!/usr/bin/env bash`, which runs the first bash on
kit's PATH.

## Writing it well

- One job a step: two things that can stand apart are two steps.
- The check proves the outcome, not that files are there: an app told of
  an extension, not the extension's files copied. Where the outcome can't
  be checked cheaply, check the nearest proof, and say so in a comment.
- Apply is safe to run again, and leaves nothing half done that its check
  would take as done.
- Data comes from the folder or from kit-config, never from a path or a
  name only this Mac has.
- Wait for what's slow, an app or the network, for minutes, trying again,
  not once with a fixed sleep.
- `sudo -n` never asks; with `--sudo`, plain sudo gets the password kit
  asked for.
- Never print a secret: kit hides the values it set, but not what the
  script works out from them.

## Trying it

`kit apply <name>` applies it with the steps it needs (not those it comes
after), then `kit status <name>` shows its check. On this Mac what it comes
after is done already, so that proves less than it seems:
`kit agent order <name>` says what a new Mac's run has done by then.
`kit step remove <name>` undeclares it and removes its folder.
