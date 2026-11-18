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
shellcheck --shell=sh install.sh        # the install script: POSIX sh alone
shellcheck -x scripts/release-*         # the release's hooks
shellcheck demo/*.sh                    # the scripts that record the README's art
shellcheck --shell=sh demo/bin/kit      # the shim its status tape types kit at
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
  notes. The maintainer releases with mint (`./release`), whose hooks run the gates and build
  the binaries on the Mac before the tag (`scripts/release-build`), then upload them to the
  release and update the Homebrew tap (`scripts/release-upload`).
- **Never log a secret.** Values go from their store to their file without passing through
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
  - puts only stubs of the programs kit drives on `PATH` (the list is `stubbed`, in
    `internal/testguard/stubs.go`: brew, mas, sudo, op, ssh, agntc, curl, swiftc and the rest);
  - lets `http.DefaultTransport`, and transports cloned from it, dial loopback, and unix
    sockets in the temporary directory, alone.
- **testguard fails the run, even when every test passed,** when a stub ran, or a dial was
  blocked.
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
  - running the real brew, mas, gh, op, git, ssh, ssh-keygen, claude, composer, Node (so npm), Arq's arqc,
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

## Writing the docs

The README and `docs/` are for someone deciding whether to use kit, or with one job to do, often
reading in the GitHub app on a phone. `docs/design.md` is for contributors and keeps its own
style. `kit agent`'s guide (`internal/guide/text/`) is for agents: read it for facts, never copy
its sentences.

### The rules

1. **One idea a sentence.** Under 5% of a page's sentences run over 25 words.
2. **One topic a paragraph,** led by its point, 3 to 5 sentences, never over 100 words.
3. **Example first, rule after.** The command and what it prints, then the explanation.
4. **No fact-chains.** Three or more items make a list; conditions make an if-then table.
5. **Name the thing.** At most one "its" a sentence, and only for the subject. Repeat the noun
   rather than leave a pronoun to resolve.
6. **No possessive chains.** One possessive a noun phrase, under 1.5 per 100 words. "kit's
   PATH" is fine. "The cask's app's settings" isn't: write "the settings the cask's app
   keeps". A verb beats both. Never drop the verb from a second clause.
7. **Plain words in their usual sense:** "if", not "where"; "only", not "alone"; "because" or
   "for example", not "as".
8. **One term, one meaning.** Each term is defined once, in `docs/glossary.md`, and used in
   that sense only. A second sense gets a new word.
9. **Headings that inform,** their first two words carrying them. H3 at most.
10. **The main path on the page.** Edge cases go one level down, behind a clear link.
11. **Link to whole pages,** never a heading in another file: the GitHub app opens such a link
    at the top of the page.
12. **Benefits first, and specific:** a number, or something the reader will see. No praise
    words ("powerful", "seamless") or minimisers ("simply", "just").
13. **"You" and the active voice.** Say who does what; a condition goes before the instruction
    it governs.

### The phone

- Code lines run 52 columns at most. A comment goes on a line of its own, above the command.
- Tables run 4 columns at most, with short cells.
- No forced line breaks (`<br>`), but in the README's Screens grid.
- What kit shows at a terminal goes on a page as a still, drawn by kit's own faces from scripted
  runs, never a code block or a copy.
- Each still is a fixture in `internal/capture`, drawn from the run the page's words come from
  and pinned by its golden file. `demo/` records it to `art/<page>-<what>.webp`.
- Embed a still as `<img src="../art/…" alt="…" width="…">`. Its alt text says what it shows,
  and its width is half its pixels, so a phone draws it sharp.
- Text fences stay only for what a reader copies or searches:
  - JSON, for agents;
  - kit's plain face, on a page about scripts and agents;
  - an error message, quoted in troubleshooting;
  - declarations, `kit.toml` and layout lines.

### Facts and placeholders

- Check every claim against the code and the commands' `--help`. Start at `internal/kind` and
  `internal/cli`. The rules of the config, drift and the engine are in their own packages, and
  what kit prints is in `internal/render`'s tests and testdata. The code wins over any page,
  the guide included.
- Use kit's own words for kit's things, as the glossary has them: kit-config, a declarations
  file, a kind, drift, reconcile. "Feature" means only what `kit feature` switches on: call
  the rest pages or benefits.
- Never call `kit status`'s CONFIG section "drift": it holds more, such as kit-config's sync
  and the counts.
- Placeholders as the house rules say: Macs `laptop` and `studio`, `/Users/someone`,
  `op://vault/item/field`, `owner/tap` for a tap, `owner/repo` for a repository.

### Measuring

```bash
S=~/.claude/skills/readme-and-docs
python3 -I $S/scripts/measure.py --root . \
  --exclude docs/design.md --detail
```

It fails a page that breaks a rule's limit, and prints signs for a reviewer: fix what it flags
in the prose, never by bending prose to the meter.

### The README

About 700 words, in this order:

1. **The top,** laid out as Lee's other tools' READMEs (Switchboard's is the model), all inside
   `<div align="center">`: the logo (kit.app's icon, `width="128"`), `# kit`, the tagline in
   bold, the lead and opening paragraph, the badges (licence, Go, platform), a row of page links
   and the hero, one animation that tours kit. Then `</div>` and a `---` rule. The benefits come
   under `## Why kit?`.
2. **Benefits,** six at most: a bold benefit of six words or fewer, one specific sentence, and a
   link to its page. The smaller pages go in one line of links under them.
3. **Screens:** a grid of stills drawn by kit's own faces from scripted runs (tapes in `demo/`,
   VHS, `art/*.webp`, as Switchboard and Portal make theirs), each captioned with the one thing
   it answers, each linking to its page. Never a code block or a mock-up of a screen kit draws.
   Every screen is the clip's window, whole, so all are one size and their text one scale. The
   grid runs two to a row, an even number of screens, each `width` a quarter of its pixels. As
   in Switchboard's, a `<br>` sets each caption under its screen, the one place one's allowed.
4. **Install:** one line of requirements, the commands, one line on removing kit.
5. **Quick start:** two to four commands, a comment above each.
6. **Questions:** four or five one-line answers to readers' objections, each linking to a page.
7. **Where next:** the docs index (`docs/README.md`), commands, configuration, troubleshooting;
   one line for contributors.
8. **Licence.**

A benefit gets a bullet and a page, never a section of the README. The commands table, the
config layout and the gates live on their own pages.

### A benefit page

300 to 700 words, headings no deeper than H2, in this order:

| Part | What it holds |
|---|---|
| Title | The benefit, in the reader's words |
| Opening | 40 to 60 words: what it does for you, and when you'd want it |
| The moment | One still or clip, once one exists, as The phone says |
| Try it | 2 to 4 commands, a comment above each, then the still of what kit shows |
| What happens | 3 to 6 one-sentence bullets on what you'll notice |
| Good to know | 2 to 4 bullets: limits, costs, objections |
| Reference | A table of 8 rows at most, or links to the reference pages |
| Related | 2 or 3 links to other pages |

A page may add a section or two of its own between What happens and Good to know, if its
reader needs it, such as packages' "Installs by hand show up". Edge cases still go one level
down. `docs/packages.md` is the first page written to it. Other pages:

- **A command page:** "`kit cmd`: what it's for", an example with its output, the synopsis,
  a flags table.
- **A reference page:** one file or format, an example, then a table of field and meaning.
- **A troubleshooting page:** each problem a heading in the reader's words, then the cause in a
  sentence, then the fix.
- **The glossary:** each term in bold, then one to three plain sentences.

A page named `claude.md` or `readme.md` folds to `CLAUDE.md` or `README.md` on a Mac: name it
otherwise.
