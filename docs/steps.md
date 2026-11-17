# Steps of your own: set-up no package covers

A step of your own is a script in [kit-config](glossary.md) that checks one piece of set-up and
puts it right. kit runs it with every other step, on this Mac and on a new one. A step by hand
covers what only you can do, such as a sign-in. You'd want one for set-up that no package or
setting covers.

## Try it

```bash
# Declare a step, after Homebrew's formulae
kit step add tool "Installs the tool" --after brew
```

kit writes its script as a template, then commits and pushes kit-config:

```console
  ● tool  declared in laptop · its script, laptop/steps/tool/run, is a template: write its check and apply
```

The line lands in `[steps]`, in its place by name. The order of the lines doesn't set the
order steps run in: `--after` and `--before` do.

```text
[steps]
tool "Installs the tool" --after brew
```

Fill in `check` and `apply`, then try it with `kit apply tool`.

## What happens

- **The step shows under STEPS** in `kit status`, with the first line its check prints:

  ```console
    STEPS
    ● fonts  16 installed
  ```

- **A new Mac runs it in its place,** once what it comes after is done.
- **A step by hand raises a card** at a terminal, saying what to do.

## The script answers check and apply

kit runs `run check` in the step folder. If the check says it isn't done, kit runs `run apply`,
then checks again. It's done only once that check says so.

| `run check` exits | kit reads it as |
|---|---|
| 0 | Done |
| 1 | Not done: `kit apply` runs `run apply` |
| 3 | Can't be done yet: not applied this run |
| anything else | The check failed |

A check runs on every `kit status` and every [hourly run](running-by-itself.md), so it must
change nothing and ask no one. If `run apply` fails, the last line it prints says why.

## A step waits for what it needs

Four flags on `kit step add` say what a step needs. All but `--sudo` can repeat.

| Flag | What it does |
|---|---|
| `--after <name>` | Waits for a step, or a thing such as `cask:firefox` |
| `--before <name>` | Makes that step wait for this one |
| `--secret <name>` | Waits for the secret to sync |
| `--sudo` | Needs an administrator's password to apply |

A secret is named by what it fills, and must be declared first: see
[Secrets from 1Password](secrets.md). If the secret is a variable, the script runs with it set.
A secret that fills a file is only waited for. With `--sudo`, kit asks for the password once at a terminal,
for `sudo` in the script. `--after restore` waits for the
[data restore](your-data.md) on a new Mac.

## Steps done by hand

Some set-up only you can do, such as signing in to the App Store. Declare it in `[manual]`:

```bash
# A step only you can do; you say when it's done
kit manual add licence "Enter the editor's licence"
kit manual done licence
# Or give a command that tells kit it's done
kit manual add tool "Install the tool" \
  -- test -d /Applications/Tool.app
```

```text
[manual]
licence "Enter the editor's licence"
tool "Install the tool" --test test -d /Applications/Tool.app
```

The boot, and a whole `kit apply` at a terminal, show each step by hand that isn't done as a
card. A comment above the line says why, and indented lines under it become numbered steps.

With `--test`, kit sees the step done once the command exits 0, and carries on. Without one,
you press space on the card, or run `kit manual done`. If a card waits a while, kit posts a
notification too.

## Where a step falls on a new Mac

On this Mac, everything a step comes after is done already. `kit agent order` shows what a new
Mac has done by the time the step runs:

```bash
# Where uses-app, a step after cask:ghostty, falls
kit agent order uses-app
```

```console
uses-app on a new Mac

Before it
  homebrew        Homebrew, finished
  cask:ghostty    installed, likely never opened yet; its settings restored by
                  prefs, which may come before or after it
```

## Good to know

- **The script runs in kit's environment.** It gets kit's PATH, from `[paths]`, and only a few
  basics such as `HOME` and `USER`: nothing your `.zshrc` sets.
- **Exit 3 costs a run.** Name what a step needs with `--after` or `--secret` instead.
- **Steps by hand come last.** Their cards come up once the rest is done, unless you add
  `--before <step>` to the line by hand.

## Reference

| Command | What it does |
|---|---|
| `kit step add <name> "<what>"` | Declare a step, and write its script |
| `kit step remove <name>` | Undeclare it, and remove its folder |
| `kit manual add <name> "<what>"` | Declare a step by hand |
| `kit manual done <name>` | Mark it done on this Mac (`--undo`) |
| `kit agent order <name>` | Where it falls in a new Mac's run |

Each `add` takes `--shared` for every Mac and `--note` for why; `kit manual add` also takes
`--group`. Each has a `remove`.

## Related

- [A new Mac from one line](new-mac.md)
- [Running by itself](running-by-itself.md)
- [Secrets from 1Password](secrets.md)
