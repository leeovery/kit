# Moving to a new Mac

When the person asks whether they're ready to move to a new Mac, check the
Mac they're moving from. Each check is a row of `kit status`: run
`kit status --json` there, and read these steps and their items.

- `time-machine` and `arq`: the backups, and how old the last one is. A new
  Mac that can't reach this one restores from them.
- `prefs`: apps' settings saved, needing attention once the last save is
  over a day old.
- `remote-login` and `file-sharing`: on, so the new Mac can copy from this
  one over the local network.
- `host-key`: this Mac's SSH host key and local hostname, as kit.toml
  records them (`host_key`, `local_hostname`). The new Mac copies only from
  the Mac presenting that key, at that name.
- The step that adds the key the Macs share to this Mac's authorized keys,
  when kit-config has one, as `authorized-key`.
- rsync, declared under `brew`: the copy runs Homebrew's rsync on this Mac.
- `config-sync` and `config`: kit-config pushed, with nothing in it left
  uncommitted. Read `config`'s items: an edit's first day, it reads `ok`.
- `secret`: every secret synced.

Then, on the day:

- `kit prefs capture` that morning, so the new Mac restores the latest
  settings, not the night's.
- `kit apply --plan` says what's declared and missing here; `kit apply`
  puts it in place.

Outside kit:

- Claude Code's sessions finished on this Mac: one still running as its
  folder is copied goes across half-way.
- The new Mac's boot asks which Mac it starts from: the person names this
  one, which kit records as its `from` in kit.toml.

What kit can't check: Full Disk Access for remote users, the switch in
Remote Login's settings that lets the copy read the whole home folder. It's
a step done by hand, and macOS gives kit no way to read it: ask the person
to confirm it's on.

Tell the person what isn't ready, each with what to do about it. Ready is
every one of these fine.
