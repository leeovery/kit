# Changing this Mac's set-up through kit

Read this before you change anything about how this Mac is set up:
installing or removing software, changing a setting or a dotfile, keeping a
secret, or writing a script that sets something up.

kit promises that a Mac that dies is replaced, and set up again from its
config, with one line pasted into Terminal, months later, and that it works.
Between new Macs, the config changes on a running Mac, where everything else
is already in place, so a change can work here and fail on a blank Mac. Your
job is to make each change work on both: think it through against how kit
applies on a new Mac (the last section) before it's committed.

## The rules

1. kit makes a Mac match its config, kit-config, a private git repository:
   lists of packages and settings, files linked into the home folder, apps'
   captured settings, secrets, steps done by hand, and steps of your own.
   `kit apply` installs what's declared and missing, `kit reconcile` settles
   what differs from the config, and `kit status` reports how the Mac stands.

2. Every change to the set-up goes through kit-config, so that a new Mac
   gets it. A `brew install`, a file edited outside kit-config or a setting
   changed by hand is lost when the Mac is replaced.

3. Change kit-config with kit's commands: `kit cask add`, `kit brew add` and
   the other kinds' add and remove, `kit step add`, `kit secret add`,
   `kit file add`, `kit manual add` and `kit reconcile`. Each commits its
   change and pushes it. An edit by hand, to a declarations file or a step's
   script, is yours to commit and push, or `kit reconcile`'s.

4. Read a command's `--help` before you use it the first time: it says what
   it needs and what it changes. Every command has one, as in
   `kit step add --help`.

5. A line is for this Mac unless `--shared` declares it for every Mac.
   Choose by where it's wanted: ask when you can't tell.

6. A secret never goes on a command line. `kit secret add --stdin` takes its
   value on standard input, and kit never prints a value.

7. Removing uninstalls as well as undeclaring. Ask the person before you
   remove anything.

8. Never change kit itself to fit one thing. kit is general: a step of your
   own covers what no kind does.

## Which mechanism

Take the first that fits, in this order:

1. A package of a kind kit has: Homebrew formulae and casks, App Store
   apps, npm, Composer, Go tools, GitHub CLI extensions, login items,
   Claude Code's MCP servers and skills (`kit --help` lists them all).
2. A setting: macOS's (`kit defaults add`), power (`kit power add`), git's
   (`kit git-config add`).
3. A file the person writes, a config or a script: a linked file.
   `kit file add` moves it into kit-config and links it back. Never a file
   its program replaces when it saves: the program would put a copy where
   the link was, and the link is lost. That's an app's settings, item 5.
4. A secret: `kit secret add`, into the shell's environment or a file.
5. An app's settings, how it behaves: kit prefs saves apps' preferences and
   the settings files kit-config's `[prefs files]` names, and restores them
   on a new Mac.
6. The person's data: code, history, ledgers, a tool's state. The restore
   list, `[restore]`: `kit restore add`.
7. A step done by hand: `kit manual add`, for what only a person can do.
8. A step of your own: `kit step add`, a script, the last resort.

A file or folder that's none of 3 to 6 is nothing of kit's: kit rebuilds
it, as apps, packages and tools, or it's a cache. Every path in the home
folder has one owner, and a folder that mixes them keeps each path's own.

What macOS makes a person approve, such as a privacy permission, a driver or
a sign-in, is a step done by hand: kit says what to do, and a command checks
that it's done.

A Homebrew package downloaded from a private repository needs a token.
`kit brew add` and `kit cask add` check the download, and put the line in
its section held for that secret, as in `[homebrew casks --secret TOKEN]`,
by themselves; a new Mac installs it once the secret has synced.

### The person's data

When you build a tool that keeps data, add its folder then, with
`kit restore add ~/.local/state/<tool>`: a new Mac started from this one
brings it across. Adding it copies nothing here.

The restore leaves out by itself what kit links, prefs saves or a kind puts
in place, kit's own folders and what the backups leave out, so none of
those is ever a `--skip`. A path that's one of those, or in one, is
refused, as is one another line holds or is in. `--skip` is for what in a
listed folder is neither the person's data nor kit's, such as a tool's own
credentials in its state folder, which its own setup makes again.

## Steps of your own

### The contract

A step is a line in kit-config's `[steps]` and a script, `steps/<name>/run`
in this Mac's folder (in `shared/` with `--shared`), its data beside it.

kit runs `run check` first. It exits 0 when the step is done, 1 when it
isn't, and 3 when it can't be done yet because something it needs isn't
there; its first line says how it stands. Any other exit is a failure.

When it isn't done, kit runs `run apply`, then checks again. Apply must be
safe to run again, and when it fails, its last line says why.

The check proves the outcome, not that files exist. A step that put an
extension's files in place passed its check while the app had never been
told of the extension. What can't be checked cheaply is checked by the
nearest proof, and the script says so.

A check runs on every `kit status`: it changes nothing, and it's quick. kit
gives it 30 seconds, tries it once more, then fails it. One that has to go
online says so in its script.

Declare a step with `--sudo` when its apply needs an administrator: kit asks
for the password once a run, at a terminal, and gives it to sudo when sudo
asks. Without a terminal, as in your shell, there's no password to give, so
such a step waits: the person runs it at a terminal. Never drop `--sudo`, or
use `sudo -n`, to make one pass.

### What a step can rely on

kit's environment, not your shell's: HOME, the PATH kit-config's `[paths]`
makes, USER, LOGNAME, SHELL, TMPDIR, LANG, LC_ALL and SSH_AUTH_SOCK, the
secrets store's own variables, and the secrets it names. No token, alias or
function from a shell is there. It runs in its folder, with nothing on its
input.

What it names: `--after` for each thing kit installs that it uses (a step,
or a step's thing, as in cask:firefox), and `--secret` for each secret. A
step it comes after has finished by then, whether or not it succeeded; a
thing it comes after is installed, or the step isn't applied. Nothing else
is promised to be there when it runs.

Naming what it needs is how a step gets it on a new Mac. Exit 3 is only the
safety net: a step whose check exits 3 isn't applied for the rest of that
run, so a new Mac needs a second run for it. Use it for what can't be named.

Its secrets are waited for inside apply, after the check: on a new Mac the
check can run before a `--secret` value has synced. A check that needs one
it hasn't got says not done (exit 1), never 3, or the step isn't applied in
that run.

A script that starts `#!/bin/bash` runs macOS's own bash, 3.2, on a new
Mac: no mapfile, no associative arrays, no `${var,,}`.

### Writing one

`kit step add` writes the script as a template that follows the contract:
fill in its check and its apply. Then run `kit apply <name>`, and
`kit status <name>` to see its check say done.

A step added with `kit step add --sudo`, or one after a cask that installs
through a package, waits without a terminal: ask the person to run
`kit apply <name>` at one.

Then think it through on a new Mac, as the next section says, with
`kit agent order <name>`.

## A new Mac

### How its run goes

The boot comes first: GitHub's sign-in on the phone, which Mac it is, the
administrator's password, Homebrew, the terminal kit-config names, then
kit-config cloned and its files linked. Then `kit apply` runs everything
kit-config declares for that Mac, in that terminal.

kit apply orders what's declared by `--after` and `--before`, and by kit's
own order (Homebrew before its formulae, apps' settings after the apps).
Everything else runs at the same time, in no order you can rely on.

Sign-ins come partway through. GitHub's is there from the boot, for gh.
The password manager's is a card the person answers, and secrets sync after
it. Others, such as the App Store's, are steps done by hand.

What's held for a secret waits for it to sync: a list section held for a
secret, and a step declared with `--secret`.

Apps' settings are restored once every cask and App Store app is installed,
near the end of the run.

On a Mac the boot has just set up, the data restore brings back the paths
`[restore]` lists, once Homebrew's rsync is in: on a Mac started from
another, from that Mac, known by the SSH host key kit-config records for
it, once the secrets have synced, else a backup of it; on a Mac
clean-installed under its own name, from its own backups. Claude Code's MCP
servers and skills, the reinstall of restored projects' dependencies, and a
step declared `--after restore` wait for it; nothing else does. A step of
your own that writes into a listed folder must say `--after restore`.
Linked files and apps' settings don't wait: the copy leaves them out.

Nothing waits on a step done by hand unless its line names what waits, with
`--before`: then it comes up as a card partway through, once what it comes
after is done. The rest come up at the end. The person may not be at the
Mac when one comes up.

### What's different from this Mac

Nothing else is there yet: no app used, no setting restored, no secret
synced, no repository cloned, no tool signed in, but what a step's
`--after` and `--secret` bring.

An app just installed has likely never been opened: kit doesn't open it,
though a few installers do, as does the password manager's sign-in card. Its
first launch is slow, may show onboarding or macOS's "downloaded from the
internet" question, and it may not answer a URL, AppleScript or its own
command line for a while. Drive an app only after its settings are restored
(`--after prefs`), and try again for a while, not once. `--after prefs`
fails the step on a Mac without kit prefs on, so a step declared for every
Mac with it needs prefs on in every Mac.

An app whose first launch needs a person (onboarding, a sign-in) can't be
retried into working. Add a step done by hand for it with
`kit manual add`, which can't order it: then, in the declarations file,
order its line after the app and before your step, by hand, and commit
that edit (rule 3):

    [manual]
    app-set-up "Finish the app's set-up" --after cask:app --before uses-app

An app opened before its settings are restored leaves its restore waiting
till it quits.

Homebrew's download cache, a shell's tokens, a file made by hand: none of
them is on a new Mac. What `[restore]` lists, your projects among them, is
there only once the data restore has brought it. A try that passed because
of one proves nothing.

A new Mac is busy, with dozens of installs at once: give what waits on the
network or an app time, minutes not seconds.

### Before you finish

Answer each of these for the change:

- What does it need that a blank Mac won't have at that point?
- Is each named (`--after`, `--secret`, `--sudo`)? Exit 3 is the safety
  net for what can't be, and leaves the step for the next run.
- Does it open or drive an app? Then it comes after prefs, and retries.
- Does it lean on a shell, a cache or a file only this Mac has?
- Does its check prove the outcome?
- Is its apply safe to run again?
- Is it this Mac's, or every Mac's?

Run `kit apply <name>` and `kit status <name>` here: its check says done.
What needs an administrator's password waits without a terminal: the person
runs that at one.
`kit agent order <name>` says what a new Mac's run has done by the time it
applies, and what comes after it.

What isn't obvious from the script, say in its note or the commit message:
what it waits for, and why.

What can't be proven on this Mac, tell the person: the next rehearsal of a
new Mac proves it.
