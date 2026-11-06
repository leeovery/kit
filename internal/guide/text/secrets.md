# Secrets

A secret is a value kept in a secrets store (a password manager) and put in
place on each Mac by kit: an environment variable in `~/.secrets.zsh`, which
the shell sources, or a file. kit-config holds where each is kept, never
its value.

## Never on a command line

A value never goes on a command line, in a file in kit-config, in a commit,
or in what you print. `kit secret add <name> --stdin` takes it on standard
input; at a terminal, kit asks for it unshown. kit never prints a value, and
hides the values it sets from what it logs.

## Adding one

`kit secret add <name>` keeps a value in its store, declares it, syncs it,
and reads it back to compare. `<name>` is what it fills: a variable's name,
as in API_TOKEN, or a file's path, from ~ or /.

- The value comes typed at a terminal, on standard input (`--stdin`), from
  a file (`--from`, kept as an attachment), or is in a store already
  (`--ref` and where it's kept, in full: nothing is stored then).
- A new value goes where you say, nowhere unless you do: `--in` and a
  block's name, or `--item` and the store's item, in full. `--field` says
  where in the item. `kit secret places` lists the blocks, as this guide's
  facts do.
- `--mode` sets a file's permissions (600 otherwise).
- `--shared` declares it for every Mac.

`kit secret add --help` says how the stores kit knows write their
references.

## How they're declared

Secrets sit in secrets sections of a declarations file: a block for a
store's item, its lines short, or the plain `[secrets]`, each line's
reference in full:

    [secrets op://vault/item --as tokens]
    API_TOKEN Section/token

    [secrets]
    OTHER_TOKEN op://vault/other/token

## What uses one

- A step of your own names it with `--secret NAME`: its apply waits till
  it has synced, and runs with it set when it's a variable. Its check may
  run before then, without it.
- A list section held for it, as in `[homebrew formulae --secret NAME]`:
  its lines install once it has synced, with it set for those commands
  alone. `kit brew add` and `kit cask add` put a package from a private
  repository there by themselves.
- git's settings are applied after the secrets, which they can sign with.

Nothing else gets a secret: a step's environment is kit's, not the shell's,
so a token your shell has is not there unless it's declared and named.

## On a new Mac

Secrets sync once the password manager is signed in, a card the person
answers partway through the run. Until then, what needs a secret waits. A
store that isn't signed in fails its own secrets alone, and a value that
fails to read keeps the one it had.

## Changing and removing

After changing a value in its store, `kit secret sync` puts every secret in
place again. `kit secret remove <name>` takes it off this Mac and
undeclares it; its value stays in the store (`--keep-value`) or goes
(`--delete-value`). Ask the person before removing one.
