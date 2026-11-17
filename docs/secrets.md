# Secrets from 1Password, on every Mac

Keep a token or a key in 1Password once, and each Mac that needs it gets it: a variable in your
shell, or a file. Only the place each value is kept goes in kit-config, never the value. No
value is printed or taken on a command line. You'd want it for every token your tools read.

## Try it

```bash
# Keep a token: kit asks for it, unshown
kit secret add API_TOKEN \
  --item op://vault/item --field GitHub/token
```

kit stores the value in the item and reads it back to compare. Then it declares the secret,
syncs it, and commits and pushes kit-config:

```text
[secrets op://vault/item]
API_TOKEN GitHub/token
```

```bash
# From a script or an agent: the value on stdin
kit secret add OTHER_TOKEN --field GitHub/other \
  --stdin < token.txt
# A key file, kept as an attachment
kit secret add ~/.ssh/id.pem --from id.pem
```

Neither names an item, so both go in the one block for an item that this Mac's declarations
file has.

## What happens

- **A variable lands in `~/.secrets.zsh`,** for your `.zshrc` to source.
- **A file lands at its path,** readable by you only, unless `--mode` says otherwise.
- **Every Mac that declares it gets it.** `--shared` declares it for every Mac.
- **A value already in 1Password needs no copy.** `--ref op://vault/item/field` declares it
  and stores nothing.
- **Nothing logs a value.** kit hides each value it sets from the run log.
- **A failed read keeps the old value.** If `kit secret sync` can't read a value, the one in
  place stays.

## Where a value goes

A new value goes in an item, and its line goes in a block for that item:

| If you give | The line goes in |
|---|---|
| `--item <item>` | This file's block for that item, made if needed |
| `--in <name>` | This file's block for the item of the block named, made if needed |
| Neither, and one block for an item | That block |
| Neither, and none or several | Nowhere: kit refuses, and lists the blocks |

"This file" is this Mac's declarations file, or the shared one with `--shared`. So `--in` with
the name of a shared block, without `--shared`, makes a block in this Mac's file. The plain
`[secrets]` block counts for none of this. `--field` says where in the item the value goes. A
file from `--from` needs no `--field`: it's kept under its own name.

`kit secret places` lists the blocks. On a Mac with three blocks, it prints:

```console
  ● tokens  1Password · op://vault/Item · shared · 2 secrets · sections Other, Section
  ● [secrets]  references in full · laptop · 1 secret
  ● op://vault/Other  1Password · laptop · 1 secret · section Section
```

The first is named with `--as` in its header: `[secrets op://vault/Item --as tokens]`.

## Private taps need a token

`kit brew add` and `kit cask add` check whether a tap's download is private. If it is, the
install needs `HOMEBREW_GITHUB_API_TOKEN`, a GitHub token kept as a secret:

- If the token isn't declared, kit refuses the add, and says to declare it first.
- If it is, the line goes in a section held for the token:

```text
[homebrew casks --secret HOMEBREW_GITHUB_API_TOKEN]
owner/tap/app
```

A shared line needs the token on every Mac, so declare it with `--shared`. The token is set for
those installs only, from the secrets kit keeps, never from your shell. On a new Mac, the line
waits until the token has synced.

## Good to know

- **1Password may ask for your approval,** such as a fingerprint. It may ask before a value is
  read, kept or deleted. While it waits, kit says "waiting for your approval in 1Password",
  for up to 10 minutes. A command an agent runs waits on you too.
- **1Password is the one store today.** The reference picks it: `op://` means 1Password.
- **A new Mac syncs after you sign in.** Signing in to 1Password is a card partway through the
  run. What needs a secret waits until then.
- **A step of your own gets a secret only by name.** It declares `--secret NAME`: see
  [Steps](steps.md).
- **Removing asks about the value.** `kit secret remove` takes it off the Mac.
  `--keep-value` or `--delete-value` says what happens in 1Password.

## Reference

| Command or flag | What it does |
|---|---|
| `kit secret add <name>` | Stores, declares and syncs it |
| `add --stdin` | Reads the value on standard input |
| `add --from <file>` | Keeps a file as an attachment |
| `add --ref <reference>` | Declares a value kept already |
| `kit secret places` | Lists the blocks |
| `kit secret sync` | Puts every secret in place again, after a change in 1Password |
| `kit secret remove <name>...` | Takes it off, and undeclares it |

## Related

- [Packages: install once, and your next Mac has it](packages.md)
- [Built for agents](agents.md)
- [A new Mac from one line](new-mac.md)
