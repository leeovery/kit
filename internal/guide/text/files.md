# Files

## Linked files

kit-config's `<mac>/home/` and `shared/home/` hold files linked into the
home folder, each to the same path under ~: `<mac>/home/.config/tool/config`
is linked at `~/.config/tool/config`. kit links them file by file, never a
folder.

`kit file add <path>` moves a file, or every file in a folder, into this
Mac's home folder in kit-config (every Mac's with `--shared`), links it
back in its place, and commits and pushes the change.
`kit file remove <path>` puts a copy back in place of the link, and takes
it out of kit-config.

An edit through a link is an edit in kit-config, and nothing commits it for
you: `kit status` shows kit-config's edits not committed, and
`kit reconcile` commits or undoes them. Commit your own edits yourself.

A link replaced by a copy (a program that saved over it) is drift:
`kit status` shows it, and `kit reconcile file:<path> --revert` puts
kit-config's copy back.

## Which files to link

Link a file only you write: a shell's config, a tool's config file you
edit by hand.

Never link a file its program replaces when it saves, as many apps and some
tools do with their settings: the program puts a copy where the link was.
Those are captured by kit prefs instead, listed in kit-config's
`[prefs files]` (see `kit agent prefs`).

Never link a file holding a secret: kit-config isn't the place for values.
A file that is a secret is declared as one, with `kit secret add`, which
writes it on each Mac (see `kit agent secrets`).

## On a new Mac

The boot links kit-config's files, before kit apply starts, so a new Mac
has them from the first step. In kit apply, the file step checks them
again, in no order with anything else. On this Mac, a file just added is
linked by `kit file add` itself.

What a linked file expects (a program, a plugin folder, another file) is
a package, a step or another linked file of its own: a file that works
here because something else is already here can break a new Mac's shell.

## The PATH

`kit path add <dir>` puts a directory last on the PATH, kit's and the
shell's, for this Mac or every Mac (`--shared`): kit-config's `[paths]`.
Steps of your own run with that PATH, then the system's, never your
shell's.
