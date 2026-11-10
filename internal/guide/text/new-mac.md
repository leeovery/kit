# A new Mac

What a change has to survive: a blank Mac, set up from kit-config months
from now, by a person who may not be watching.

## The boot

One line pasted into Terminal installs kit and runs `kit bootstrap`: it
signs in to GitHub on the person's phone, asks which Mac this is and for
the administrator's password once, then installs Homebrew, the terminal
kit.toml names and kit-config, and links its files. It carries on in that
terminal with the whole of `kit apply`.

So when `kit apply` starts: Homebrew and the terminal are there, gh is
signed in to GitHub, kit-config is cloned and its files linked, and nothing
else of the person's is.

## The run

`kit apply` builds its steps from kit-config for that Mac, and orders them:

- by what kit itself needs: Homebrew before its formulae and casks, git's
  settings after the secrets, login items and apps' settings after the
  casks and App Store apps;
- by what kit-config says: `--after` and `--before` on steps, on steps
  done by hand, on features' and list sections' headers, and `--secret`;
- and nothing else. The rest runs side by side, a few at a time, in no
  order you can rely on, whatever order it ran in last time.

A step waiting for one of another step's things (`--after cask:firefox`)
starts once that thing is in, or once its step has finished, failed or
not: a wait never hangs the run. A thing that didn't come leaves the step
unapplied.

Partway through:

- The password manager's sign-in is a card the person answers; secrets
  sync after it, and what's held for a secret waits for it.
- Steps done by hand that something waits on come up as cards when their
  turn comes; the rest come up at the end. The person may be away.
- Installs that need an administrator's password use the one asked for at
  the start.
- Apps' settings are restored once every cask and App Store app is in.

Name what a step needs (`--after`, `--secret`) so it's there in the first
run. A step whose check exits 3 isn't applied for the rest of that run: the
person runs `kit apply` again later, and it's applied then. Exit 3 is only
the safety net. A check that needs one of its own secrets says not
done (exit 1) when it hasn't synced yet, never 3: kit waits for it inside
apply.

## The person's data

A Mac started from another (the boot's "start from a Mac", or
`kit machine add --from`) carries its `[restore]` list in its copied
declarations, and its data restore brings those paths across: code,
history, ledgers, tools' state. Nothing else of the old Mac's comes: kit
rebuilds the rest from kit-config.

- From where: the Mac it started from, over the local network, while it
  answers, known by the SSH host key kit-config records for it (`host_key`
  in kit.toml, which `kit apply` on that Mac records with its remote-login
  feature on; without it, the old Mac isn't looked for); else its Time
  Machine disk, attached, or Arq's backup, which the person restores in
  Arq for kit to put in place
  (`kit apply --restore-from time-machine|arq`; `mac` goes back to the old
  Mac).
- When: once Homebrew's rsync is installed and, copying from the old Mac,
  the secrets have synced. Claude Code's MCP servers and skills, the
  reinstall, and a step declared `--after restore` wait for it; nothing
  else does, so a step of your own that writes into a listed folder must
  say `--after restore`. Linked files and apps' settings don't wait: the
  copy leaves them out. It runs beside the rest, so the long copy holds up
  nothing quick.
- What it leaves out: what kit links, prefs saves or a kind puts in place
  (Claude Code's skills, the files secrets fill), which kit brings itself;
  kit's own folders; what the backups leave out (`[backup exclusions]`,
  and, from the old Mac, folders marked excluded from backups, though one
  holding git checkouts is copied whole); Finder's .DS_Store and .localized
  files; and what its line's `--skip` names.
- What it never touches: a path already holding files from before the
  set-up, never merged into. What the set-up itself wrote there, as a
  shell's history, is set aside in kit's state first.
- Then the reinstall: each restored project's dependencies are installed
  again from its lockfile (Composer, npm, pnpm, Yarn, Bun, uv), in main
  checkouts and the person's own worktrees, never agents' or submodules.
  Yarn Berry projects are the person's to install; Go, Cargo, Swift and
  Terraform are left alone. `kit apply reinstall` tries those that failed
  again.

A Mac without a `from`, or a line added on a Mac in use, restores nothing
unless `kit apply --restore-from` says so (`time-machine` or `arq`; `mac`
too, on a Mac with a `from`), and then only while the path is absent or
empty: one holding data is never touched.

## What isn't there

- Most apps have never been opened. A first launch is slow, may show
  onboarding or macOS's question about apps from the internet, and the app
  may not answer a URL, AppleScript or its command line for a while.
- No setting has been restored till the prefs step runs, near the end.
- No secret has synced till the password manager's card is answered.
- No repository is there but kit-config till the data restore brings the
  person's projects, no tool is signed in but gh, and no cache is warm:
  Homebrew downloads everything afresh.
- No file made by hand on the old Mac is there, but what `[restore]` lists,
  once the restore has brought it.
- Your shell isn't there: steps run in kit's environment.

The Mac is busy, dozens of installs at once, and the network is shared:
what waits on the network or an app gets minutes, trying again, not one
try after a fixed sleep.

## Thinking a change through

1. `kit agent order <name>`: what's done by the time it applies, and what
   comes after it, secrets and apps' settings always placed.
2. For each thing it uses, ask whether it's in that list. If not, name it
   (`--after`, `--secret`), or check for it (exit 3) and say why.
3. Ask whether it passes here only because of something this Mac has: an
   app opened long ago, a cached download, a token in your shell, a file
   made by hand.
4. Run it here: `kit apply <name>`, then `kit status <name>`.
5. Tell the person what this Mac can't prove: the next rehearsal of a new
   Mac, set up from scratch, is where it's proven.
