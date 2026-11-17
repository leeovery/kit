## Checking a change

- `kit status <name>`: the step's check, and what needs attention in it.
  `kit status` alone checks the whole Mac. It exits 0 whether or not
  something needs attention: read that in `--json`, as below.
- `kit apply <name>`: applies that step, with the steps it needs; not the
  ones it comes after, which a new Mac's run applies first. What needs an
  administrator's password waits without a terminal: the person runs that
  at one.
- `kit apply --plan`: says what applying would do, and does nothing.
- `kit agent order <name>`: what a new Mac's run has done by the time a
  step, or a thing as in cask:firefox, applies, and what comes after it.
- `kit why <name>`: where a package is declared, whether it's installed,
  and what needs it. `kit list` lists everything declared for this Mac.
- `kit log`: past runs, every check and command, with timings.
- The hourly run: launchd opens kit.app on the hour, and kit.app runs
  `kit nightly`, the jobs due, then every check. It tells no one what it
  finds: read it in `kit status` (`kit status nightly` says when it last
  ran) and `kit log`. `kit apply` installs it, and asks the person to give
  kit.app Full Disk Access.
- `kit reconcile`: settles what differs from the config, and commits or
  undoes edits made by hand in kit-config.

## Reading kit status --json

`attention` is true when any step isn't `ok`. Each of `steps` has its
`id`, its `area` (Backups, Mac, Drift, Config, Checks) and a `state`:
`ok`, done and right; `attention`, checked and wanting; `failed`, its check
couldn't find out; `deferred`, not checked, as what it needs isn't there.

- Its `items` say what: each an `id`, as in brew:jq, and a `state`, in its
  kind's words (missing, extra), or `problem` or `manual` for what only
  the person can put right or do, its `to_do` saying what.
- An item with `quiet` needs nothing yet, whatever it says, as in `new` on
  its first day, `snoozed` or `temporary`. A step whose items are all quiet
  reads `ok`: read its items too. `later` is one that comes by itself;
  `action` is what `kit apply` would do about it.
- The `config` step reads `ok` on an edit's first day: each of its items
  is a file in kit-config not committed. Commit it before you finish.

## Exit statuses

- 0: kit did its job, even when what it found needs attention; and a
  lookup that found nothing, as `kit log <command>` with no run of it.
- 1: something kit tried failed: a check, an install.
- 2: kit couldn't run: a command line it couldn't read, a config it
  couldn't load.

## Without a terminal

kit asks nothing: what needs an answer fails, naming the flag that gives
it, or takes the safe one.

- `kit cask remove` keeps the app's files and settings, as `--keep` does.
- `kit reconcile` lists what differs, each with its id and the flags that
  settle it, as in `kit reconcile brew:jq --adopt`.
- `kit` alone prints its help: `kit status` is the report.

Every command takes `--json`, for one JSON document to read, but `kit`
alone, help (kit help, `--help`, or a group alone, as `kit brew`) and
`kit agent`, which print text and refuse it, and `kit splash`, which plays
only at a terminal. With `--json`, what went wrong is a document too:
`error`, what it was, and `exit`, the status; after the command's own
document, it's a line on standard error.
