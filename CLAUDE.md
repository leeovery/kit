# CLAUDE.md

Guidance for Claude Code working in this repository.

kit sets a Mac up from a config repository and keeps it that way: packages, settings, secrets
and checks, one reconcile engine for every kind of thing. The design, including what gets built
when, is `docs/design.md`.

## Gates

Run all of these before reporting work done, and before any merge: `scripts/gates` runs them
in order and stops at the first that fails. Each must pass clean. CI is off until kit is
ready, so they're the only gate. Stage everything first (`git add -A`), and run the gates on
their own, never piped: a pipe's status is its last command's, so `scripts/gates | tail`
passes whatever the gates found. Merge only when `scripts/gates` itself exits 0.

```bash
gofmt -l .                   # must print nothing
go vet ./...
scripts/test-isolated        # every test, race detector on, isolated: see Test isolation
golangci-lint run            # standard linters plus modernize (.golangci.yml)
shellcheck internal/steps/template.sh   # the script steps of the user's own start from
go build ./...
scripts/personal-data-scan   # no personal data in the files or the history
```

## House rules

- **Go 1.27, standard library first.** A new dependency needs a stated reason in its pull
  request. The expected ones: `spf13/cobra` for commands; `charm.land/lipgloss/v2` for the
  terminal face, with `charmbracelet/colorprofile` to bring its colour down to what the
  terminal shows, `charmbracelet/x/ansi` to measure and cut styled text, and
  `charmbracelet/x/term` to tell a terminal and its size; `charm.land/bubbletea/v2` once a live
  view needs it; and `BurntSushi/toml` for `kit.toml`. No helper or utility libraries: no
  `samber/*`, no `testify`, no `lo`.
- **Layout:** `cmd/kit` holds `main`. Everything else lives under `internal/`, one package per
  concern.
- **The core never prints.** Commands run the engine, which emits events; faces render them (a
  terminal's, plain, JSON) and the run log records them. A command that can't ask a question
  fails, naming the flag that answers it.
- **Every external command goes through the runner** (`internal/runner`), never `os/exec`
  directly: it's how kit sets its own PATH and environment, logs each command, and how tests
  replace every program with a fake.
- **Steps have checks.** No step without a check that's cheap and has no side effects; one job
  per step; data from the config repository, never hard-coded paths or names.
- **Tests:** the standard `testing` package, table-driven where cases differ only by data,
  golden files for what the faces print (refresh with `-update`, review the diff), and isolated
  as Test isolation says.
- **Errors:** wrap with `fmt.Errorf("…: %w", err)`. Handle an error once: log it or return it,
  never both.
- **Comments are rare and earned:** only what the code can't say, such as a non-obvious
  constraint, or something that looks wrong but was proven right (say so). Never narration.
- **Nothing personal, ever,** in code, tests, fixtures, docs or commit messages: no real names
  of Macs, hosts or networks, no account names, emails, paths in a real home, 1Password
  references or hardware identifiers. Use placeholders: Macs `laptop` and `studio`,
  `someone@example.com`, `/Users/someone`, `op://vault/item/field`, addresses from
  `192.0.2.0/24`. The repo is public, and its history goes with it; `scripts/personal-data-scan`
  checks both, with private patterns CI holds as a secret.
- **Releases are the maintainer's.** Never tag, release, or write a changelog or release
  notes. The maintainer releases with mint (`./release`), which runs the gates first; the tag
  then has GoReleaser add the binaries and update the Homebrew tap.
- **Never log a secret.** Values go from 1Password to their file without passing through
  events; arguments, environment and output are redacted before they're logged.

## Test isolation

No test touches the real network, environment, home directory, config, state, logs or
programs. Ever.

- **Inject** what a test needs: a runner (the fake in `internal/runner/runnertest`), clocks, a
  `getenv`, a home directory, a config repository, a state directory and a `PATH` of its own.
  Write files under `t.TempDir()`; set variables with `t.Setenv`.
- **Every package with tests** runs them through `internal/testguard`, in a `TestMain` of that one
  statement: `func TestMain(m *testing.M) { os.Exit(testguard.Main(m)) }`. Before the tests, it:
  - points `HOME` and XDG's directories into a throwaway root;
  - clears kit's variables (`KIT_`), and those of the programs it drives that can hold real
    tokens or lead to real state (`HOMEBREW_`, `OP_`, `GH_`, `GITHUB_`, `GIT_`, `SSH_`,
    `CLAUDE_`, `ANTHROPIC_`, tmux's) and proxies';
  - puts only stubs of brew, claude, composer, defaults, gh, git, go, launchctl, mas, npm, op,
    open, osascript, sudo, tmutil and tmux on `PATH`;
  - lets `http.DefaultTransport`, and transports cloned from it, dial loopback, and unix
    sockets in the temporary directory, alone.
- **testguard fails the run, even when every test passed,** when:
  - a stub ran, or a dial was blocked;
  - kit's real config repository changed (but its `.git` directory), or its state or logs
    directory appeared, or the Mac's name in its state changed: in the home, or where
    `KIT_CONFIG`, `XDG_CONFIG_HOME` and `XDG_STATE_HOME` put them as the run began, links
    resolved.
- **testguard's own tests fail**, reading the module's source:
  - a package with tests but without that `TestMain`, or with anything else in it;
  - a change to the environment anywhere but `testguard`;
  - a process started outside the runners its allow-list names, an `exec.Cmd` made directly or
    `golang.org/x/sys/unix`'s `Exec` included;
  - an `http.Transport` made from scratch rather than cloned from `http.DefaultTransport`, as
    the guard sees no dial of its;
  - code, tests included but testguard's own, that imports `os/user`, as the home directory is
    injected, or reads the environment as its package initialises, before `TestMain` runs.
- **`scripts/test-isolated` is the test gate:** every test, race detector on, inside a macOS
  sandbox (`scripts/isolation.sb`) that denies:
  - the network beyond loopback, and unix sockets outside the temporary directory;
  - writes into the home directory, but Go's caches, and into kit's real config and state
    directories, wherever the environment puts them;
  - writes into the directories on `PATH` outside the home, such as `/opt/homebrew/bin`;
  - running the real brew, mas, gh, op, git, claude, composer, Node (so npm), Arq's arqc,
    asimov, prefsync, defaults, tmutil, sudo, launchctl, osascript, open and tmux (Go builds
    the tests, so it's stubbed, not denied).
- **It unsets the variables testguard clears** before anything starts, as testguard clears them
  only once every package's init has run.
- **Each run first proves the sandbox holds**, and `--self-check` does only that: a dial off the
  machine, a connect to a live unix socket outside the temporary directory, a write into the
  home, the real config, state and logs directories and each writable directory on `PATH`, and
  running each of those programs that's installed, are all denied; and the tests start without
  those variables.
- **Without the sandbox:** on macOS, without `/usr/bin/sandbox-exec`, the tests don't run; off
  macOS, testguard alone guards them.
- **Never loosen a guard to make a test pass.** A test that needs what a guard blocks is a finding:
  inject it instead.
