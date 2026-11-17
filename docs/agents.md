# Built for agents: Claude sets up your Mac with you

kit is agent-first. Claude or any other agent runs the same commands you do. Every command
that checks or changes something answers in JSON with `--json`. Without a terminal, kit never
waits on a question: it takes the safe answer, or fails and names the flag that gives one. `kit agent` gives an agent the rules
for changing your Mac, so that each change lands on your next Mac too.

## Try it

```bash
# The guide for agents, this config's facts first
kit agent
```

```text
kit agent: how to change this Mac's set-up through kit

This config
  this Mac    laptop: its own lines and files in laptop/, every Mac's in shared/
  Macs        laptop (MacBook Pro), studio (Mac Studio)
  kit         0.1.0
```

```bash
# How this Mac stands, as one JSON document
kit status --json
```

```console
{
  "schema": 1,
  "kit": "0.1.0",
  "machine": "laptop",
  "attention": true,
```

```bash
# A mistake comes back as JSON too
kit reconcile brew:nosuch --snooze --json
```

```console
{
  "schema": 1,
  "error": "no item brew:nosuch: kit reconcile lists them",
  "exit": 2
}
```

## What happens

- **An agent works through kit-config.** It installs with `kit brew add`, keeps a setting with
  `kit defaults add`, and adds a script with `kit step add`. Each one commits and pushes, so a
  new Mac gets it.
- **Secrets stay off the command line.** `kit secret add --stdin` takes a value on standard
  input, and kit never prints one.
- **Drift comes with its ids.** `kit reconcile --json` lists each item and the answers it takes,
  and the agent settles one by naming its id.
- **The guide goes deeper on request.** `kit agent secrets`, `kit agent steps` and the other
  topics each cover one part, found by the start of the name.
- **A new Mac stays in view.** `kit agent order <name>` says what a new Mac's run has done by
  the time a step runs, and what comes after it.

## Exit statuses

| If kit exits | It means |
|---|---|
| 0 | kit did its job, even if something needs attention |
| 1 | Something kit tried failed, such as an install |
| 2 | kit couldn't run, such as on a flag it doesn't know |

`kit status` exits 0 when it finds drift. To know if something needs attention, read
`attention` in its JSON.

## What only you can do

An agent can't do these, so it asks you:

- **GitHub's sign-in,** on your phone or in the browser, when the boot starts a new Mac.
- **1Password's approval,** such as a fingerprint. It may be asked for before a secret is
  read, kept or deleted, and kit waits up to 10 minutes for it.
- **The App Store's sign-in,** a step by hand that kit-config declares before App Store apps.
  The App Store apps wait until you've done it.
- **Your administrator's password,** at a terminal. Without a terminal, an install that needs
  it doesn't run, and kit names the command to run at one. If sudo holds a password already,
  nothing waits.

## Good to know

- **`kit agent` prints text,** at a terminal or not, and refuses `--json`. So do help and `kit`
  alone.
- **One document on standard output.** If something goes wrong after a command has printed its
  JSON, the error is a line on standard error.
- **Removing undoes it on the Mac.** The guide tells an agent to ask you before it removes
  anything.

## Reference

| Command or flag | What it does |
|---|---|
| `kit agent` | The guide, this config's facts first |
| `kit agent <topic>` | One topic, such as `secrets` |
| `kit agent order <name>` | Where a step falls in a new Mac's run |
| `--json` | One JSON document, but from `kit agent` and help |
| `--plain` | Plain lines, no colour or animation |
| `<command> --help` | What it needs and what it changes |

## Related

- [Drift: keep or undo what changed by hand](drift.md)
- [Secrets from 1Password, on every Mac](secrets.md)
- [A new Mac from one line](new-mac.md)
