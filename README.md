# kit

Set up a Mac from a config repository, and keep it that way.

kit reads a private config repository that declares what each of your Macs should have
(Homebrew formulae and casks, App Store apps, npm, Composer and Go tools, GitHub CLI
extensions, tmux plugins and login items; settings and secrets to come), and
tells you where a Mac differs. It's pretty at a terminal, plain without one, and `--json`
for scripts and agents, so an agent can drive it with the same commands you run.

> **Pre-1.0.** Built for its author's Macs, public because nothing personal is in it.
> Commands, config and output will change. See [the design](docs/design.md) for what's
> built and what's planned.

## Install

Not released yet. Build it from source, with Go 1.27:

```bash
go build -o ~/.local/bin/kit ./cmd/kit
```

Then clone your config repository to `~/.config/kit`, name this Mac (`kit machine <name>`),
and run `kit status`.

## Configuration

kit reads its config repository from `~/.config/kit` (or `$KIT_CONFIG`):

```
kit.toml                your Macs, and which is the primary
shared/declarations     what every Mac declares
<mac>/declarations      what one Mac declares, in a folder named after it
```

Each declarations file is sections of flat lines, as in `[homebrew formulae]` then a name a
line, a `# note` after any; `[paths]` is the directories kit puts on its PATH.

## Development

Go 1.27 on macOS. Every change passes the gates in [CLAUDE.md](CLAUDE.md), which
`scripts/gates` runs in order: `gofmt`, `go vet`, `scripts/test-isolated` (the tests, inside a
sandbox that keeps them off the real system), `golangci-lint`, `go build` and
`scripts/personal-data-scan`. Releases are the maintainer's, with mint (`./release`).

## Licence

MIT
