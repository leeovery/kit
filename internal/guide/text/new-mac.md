# A new Mac

What a change has to survive: a blank Mac, set up from kit-config months
from now, by a person who may not be watching.

## The boot

One line pasted into Terminal installs kit and runs `kit bootstrap`: it
signs in to GitHub on the person's phone, asks which Mac this is and for
the administrator's password once, then installs Homebrew, the terminal
kit.toml names and kit-config, and links its files. It carries on in that
terminal with the whole of `kit apply`.

So when `kit apply` starts: Homebrew is there, gh is signed in to GitHub,
kit-config is cloned, and nothing else of the person's is.

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
not: a wait never hangs the run, so a step can still find what it needs
missing, and its check says so (exit 3).

Partway through:

- The password manager's sign-in is a card the person answers; secrets
  sync after it, and what's held for a secret waits for it.
- Steps done by hand that something waits on come up as cards when their
  turn comes; the rest come up at the end. The person may be away.
- Installs that need an administrator's password use the one asked for at
  the start.
- Apps' settings are restored once every cask and App Store app is in.

A step whose check exits 3 isn't applied in that run: the person runs
`kit apply` again later, and it's applied then. Better a step that comes
after what it needs than one that waits for a second run.

## What isn't there

- No app has been opened. Its first launch is slow, may show onboarding
  or macOS's question about apps from the internet, and it may not answer
  a URL, AppleScript or its command line for a while.
- No setting has been restored till the prefs step runs, near the end.
- No secret has synced till the password manager's card is answered.
- No repository is cloned but kit-config, no tool is signed in but gh, and
  no cache is warm: Homebrew downloads everything afresh.
- No file is there that a person made by hand on the old Mac.
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
