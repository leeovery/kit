## Checking a change

- `kit status <name>`: the step's check, and what needs attention in it.
  `kit status` alone checks the whole Mac: exit 0 when nothing needs
  attention.
- `kit apply <name>`: applies that step, with the steps it needs; not the
  ones it comes after, which a new Mac's run applies first.
- `kit apply --plan`: says what applying would do, and does nothing.
- `kit agent order <name>`: what a new Mac's run has done by the time a
  step, or a thing as in cask:firefox, applies, and what comes after it.
- `kit why <name>`: where a package is declared, whether it's installed,
  and what needs it. `kit list` lists everything declared for this Mac.
- `kit log`: past runs, every check and command, with timings.
- `kit reconcile`: settles what differs from the config, and commits or
  undoes edits made by hand in kit-config.

Every command takes `--json`, for one JSON document to read.
