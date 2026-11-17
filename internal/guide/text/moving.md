# Moving to a new Mac

When the person asks whether they're ready to move to a new Mac, check the
Mac they're moving from. Run `kit status --json` there, and read these
steps and their items.

- `time-machine` and `arq`: the backups, and how old the last one is. A new
  Mac that can't reach this one restores from them.
- `prefs`: apps' settings saved, needing attention once the last save is
  over a day old.
- `remote-login`: on, as the new Mac copies from this one over SSH, with
  rsync.
- `host-key`: this Mac's SSH host key and local hostname, as kit.toml
  records them (`host_key`, `local_hostname`). The new Mac copies only from
  the Mac presenting that key, at that name. It reads ok with Remote Login
  off, having nothing to record: it needs `remote-login` on.
- rsync, declared under `brew`: the copy runs Homebrew's rsync on this Mac.
- `config-sync` and `config`: kit-config pushed, with nothing in it left
  uncommitted. Read `config`'s items: an edit's first day, it reads `ok`.
- `secret`: every secret synced.
- If kit-config declares a step that adds a key the Macs share to this
  Mac's authorized keys, as `authorized-key`, that step too.

Then, on the day:

- `kit prefs capture` that morning, so the new Mac restores the latest
  settings, not the night's.
- `kit apply --plan` says what's declared and missing here; `kit apply`
  puts it in place.

What the person does on the new Mac:

- In macOS's own setup, make the user with this Mac's user name and home
  folder: the restore refuses a Mac whose user or home differs.
- The boot asks "which of your Macs is this?". To copy from this Mac, they
  pick new, name the new Mac, and choose this Mac as where it starts from;
  or, beforehand, `kit machine add <name> --from <mac>` adds it, started
  from this Mac, and they pick its name. Picking this Mac's own name
  instead restores from this Mac's backups, not from this Mac.

What no row here checks:

- Full Disk Access for remote users, the switch in System Settings ›
  General › Sharing › Remote Login › ⓘ that lets the copy read the whole
  home folder. The new Mac's restore checks it over SSH and names that
  switch when it's off. Ask the person to turn it on now, or, if
  kit-config declares a step by hand for it, to mark it done.
- If the person works in Claude Code, its sessions finished on this Mac:
  one still running as its folder is copied goes across half-way.

Tell the person what isn't ready, each with what to do about it. Ready is
every one of these fine.
