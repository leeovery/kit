# A Mac in use

`kit setup` brings a Mac that's in use onto kit, all the way, in one
command: GitHub, kit-config made or joined, the Mac's name, everything on
the Mac declared as it is, then `kit apply`. A blank Mac takes the install
line (`kit bootstrap`) instead. `kit setup` refuses a Mac whose config
folder holds a config already: kit looks after that one.

## Before you run it

GitHub's CLI signed in (`gh auth status`) is the sign-in kit setup takes.
Without it, kit setup signs in as the boot does: in plain output, a line
saying the code to enter at github.com/login/device, which the person
enters on their phone or in a browser. `--json` holds its document till
the end, so run that sign-in in plain output, or have the person run
`gh auth login` first.

## Its questions, and their flags

Every question has its flag; without a terminal kit setup asks nothing.

| Question | Flags |
|---|---|
| Create kit-config, or join one | `--create <name>`, `--join owner/name` |
| This Mac's name | `--mac <name>`, `--from <mac>` for a new Mac |
| Folders a new Mac brings back | `--restore <path>`..., `--no-restore` |
| Apps' settings saved nightly | `--prefs`, `--no-prefs` |
| Files kit-config keeps, linked | `--link <path>`..., `--no-link` |
| Apps Homebrew takes over | `--adopt <cask>`..., `--no-adopt` |
| Joining: install what it declares | `--apply`, `--no-apply` |

- `--create` makes a private repository on the person's account; suggest
  `kit-config`. `--join` refuses a repository that isn't private.
- Joining, `--mac` is one of kit.toml's Macs, or a new one's name; a new
  one starts empty, or as a copy of another with `--from`.
- `--restore` and `--link` take a path each time they're given, `--adopt`
  a cask.
- The apps Homebrew can take over are the apps installed by hand that one
  of Homebrew's own casks installs, the cask naming the app's bundle id;
  setup's failing step names each cask and its apps, all suggested. A
  cask kit-config declares already isn't offered, but counted: `kit apply`
  takes its app over too. Each given is declared in this Mac's file, and
  the `kit apply` after setup takes its app over, at the versions its cask
  takes over (see `kit agent packages`). The rest are left by hand for good:
  `kit reconcile` won't ask about them, and `kit cask add <cask>` takes one
  over later.
- `--no-apply` stops once setup has committed, for the person to look
  first; `kit apply` then installs the rest.

## Running it without a terminal

1. Run `kit setup --json` with the flags the person has answered. It goes
   as far as they take it. A question its flag doesn't answer stops it:
   that step fails, its reason naming the flags and what they choose from
   (the folders, with sizes and which are suggested; the files), and kit
   exits 2.
2. Put each question to the person in their words, with its suggested
   answer. Never answer for them.
3. Run `kit setup --json` again with the new flags. It carries on where it
   stopped: what's done stays done, and nothing is created twice.

Run again with a flag other than the one given before:

| When | Flag | What happens |
|---|---|---|
| Before the commit's edits | a question's | It replaces the answer |
| Before the commit's edits | `--create`, `--join` | Setup starts afresh |
| After the commit's edits | any of those | Refused, naming the answer |
| Any time | `--mac`, `--from` | Refused, naming the answer |

Refused, give the answer it names, or leave the flag out.

Setup is done once its commit is pushed. If the commit or the push fails,
the `commit` step says so, and running kit setup again pushes it. Never
delete `~/.config/kit` part-way: files are moved into it and linked back.

Once it's pushed, kit setup runs `kit apply`, and the document's `apply`
holds that run, as `kit apply --json` would.

## What to tell the person

Read the document's steps, then tell them:

- `snapshot` and `commit`: how much was declared, by part (`counts`), and
  that it's committed and pushed to kit-config. `paths` counts the
  folders added to this Mac's `[paths]`, as `kit path add` adds them:
  each holds a kind's program kit's PATH didn't, found on the PATH kit
  setup ran with, so kit's later runs find it too.
- `commit`'s items: lines refused, and lines declared as they are because
  kit couldn't check whether they need a secret, each with why.
- `adopt`: how many apps Homebrew takes over in the `kit apply` that
  follows, those of casks declared already counted apart.
- `by-hand`: the apps left installed by hand, those no cask installs and
  those the person kept. kit doesn't look after them: if kit ever sets the
  Mac up again, they're the person's to install, and Homebrew or the App
  Store is the way to. An app a cask installs has that cask's add as its
  `detail`, as in `kit cask add editor`: it takes the app over later.
- `secrets`: kit keeps secrets in a store, never in kit-config. Point them
  at `kit secret add`. Never search their files for secrets.
