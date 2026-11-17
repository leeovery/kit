# The README's demos

Records the clips and stills in the project's README and docs, `art/*.webp`, by drawing kit's real
screens through the capture tool, `cmd/capturetool`: kit's own faces (`internal/render`,
`internal/look`), in-process, fed what the render tests are fed (`internal/render/rendertest`): the
status still is the screen `pretty-colour.golden` holds, and the clips' steps are the tests' own. kit itself never runs: it changes the Mac it
runs on. The capture tool runs no program and reads or writes nothing of the Mac's, no config,
state, home or network, so it's safe to record anywhere.

A rerun after any change to what kit draws remakes the art, with no setting up:

```bash
demo/record.sh               # all the art: record each tape, then make its art
demo/record.sh status        # the art named
demo/record.sh new-mac       # a clip in parts: each part's tape, then joined
```

## What's recorded

| Tape | Draws | Art |
|---|---|---|
| `status` | fixture `status` | `art/status.webp`, a still: `kit status`, the pretty face's report on a Mac with some of everything, the run `pretty-colour.golden` holds |
| `new-mac-1-terminal`, `new-mac-2-ghostty` | scenarios `boot`, `arrival` | `art/new-mac.webp`, `.mp4`: the README's lead, a new Mac from the boot's splash in Terminal to kit apply's screen in Ghostty |

`look.tape` holds the settings every tape sources; `look-terminal.tape` is the boot's, for what's
recorded in Terminal. A tape's first line says what it makes: `# art: still` a still, `# art: part
of <clip>` a part of a clip, and any other line a clip of its own. Each tape ends on a
`Wait+Screen` for a line only its run's end draws, so a scenario stuck on a key, or the capture
tool failing, fails the recording rather than leaving art that looks right.

The art goes to `art/` at the repository's root, so `git diff` shows what a recording changed, and
the art is committed with the change it shows.

## The capture tool

```bash
go run ./cmd/capturetool --list              # every fixture and scenario
go run ./cmd/capturetool --fixture status    # a still, printed at the terminal's width
go run ./cmd/capturetool --scenario boot     # a clip: enter starts it, enter again ends it
```

- **A fixture** is a still: a run's events emitted on kit's face, which prints its report as
  `kit status` does. The status tape types `kit status`: `demo/bin/kit` is a shim that draws the
  fixture of that command, on a `PATH` without Homebrew's, so the kit installed on the Mac can't
  be reached.
- **A scenario** is a clip, played in real time from events and keys scripted at moments into it
  (`internal/capture/scenarios.go`). Its clock reads a fixed day, running on from there, so the
  heads and headers say the same day and minute in every recording; the animations run on kit's
  own timers, as kit draws them. The keys it presses reach kit's faces through a pipe, as they'd
  come from the keyboard, and the script goes on only once the face has taken them, as kit's boot
  does. It waits for a line on stdin to start, the screen cleared, so a tape shows it from its
  first frame, and holds its last screen, live, till the next.
- `go test ./internal/capture` plays every scenario through, in real time, and holds the status
  fixture to `pretty-colour.golden`; `kit`'s own binary is checked never to import the capture.

## The scenarios

- **`boot`**, in Terminal: the splash, the bare machine's noise resolving into the wordmark, enter
  pressed at "press enter"; the self-test played out a line at a time; signing in to GitHub with
  the phone, its QR code the whole window; then the boot order: This Mac chosen, the password
  typed, Homebrew at work with its last lines under it, the apps, kit-config and kit, Full Disk
  Access done by hand, Ghostty opening, and "this window can be closed".
- **`arrival`**, in Ghostty: "hello again", a key pressed; the decode in colour, the wordmark
  locking in kit's gradient, shrinking into the header's corner, its line typed in; then kit
  apply's screen under it, rows at work, Homebrew's installs in the log, 1Password's sign-in
  coming up as a card on the right, and Claude Code's step finishing.

What's scripted rather than real: the steps' events, and the keys pressed. Nothing is installed or
asked of a real GitHub, Homebrew or macOS; the QR code is a real one for `github.com/login/device`,
with a made-up code. The boot's Mac is `MacBook-Pro`, set up as `laptop`; the GitHub login is
`someone`.

## The look

- **The boot, in Terminal:** a new Mac's Terminal opens with macOS's "Clear Dark" profile, 120 by
  30, and the boot's amber is made for it. The clip's window is 120 by 34, four lines taller, so the
  boot at its tallest, Homebrew's last lines and the bar under the self-test, doesn't scroll its
  head away, as it would at 30; kit draws the same either way. `look-terminal.tape` takes its text and sixteen colours
  from Terminal's own profile (`Terminal.app/Contents/Resources/Initial Settings/Clear
  Dark.terminal`) and its background as the boot draws on it, `#212734`, the colour it reverses its
  tags onto (`internal/look/boot.go`).
- **Everything after, in Ghostty:** kit's look is drawn against Nordfox, Ghostty's own values.
- **The font,** both: JetBrains Mono, as every frame of kit's look was designed in, Terminal's too.
  At 22px a cell is 13 × 29 px, so 120 × 34 is 1560 × 986, twice the 780 the README shows it at,
  so a high-density screen draws it sharp. The status still, 80 columns of kit's report, is drawn
  at 30px to come out as wide.
- **Colour:** the capture tool draws in truecolor, as Ghostty and Terminal on macOS 26 show; kit's
  colours are its own, on the terminal's background.

## How a tape becomes art

`record.sh` builds the capture tool into `demo/out/`, deletes a tape's old frames, runs it through
vhs, and hands its frames to `finalize.sh`:

- **The frames, not a video.** vhs writes every frame it shows to `demo/out/<name>/`, as the
  terminal drew it. A video's compression shifts every pixel a little from frame to frame, which an
  animated WebP then stores whole; from the frames themselves it stores only what changed,
  losslessly.
- **The art.** For a clip, an animated WebP, which GitHub shows inline, at the frames' 30 a second,
  and an H.264 MP4 of the same frames; for a still, a lossless WebP of the last frame. Each frame is
  set in a 40 px margin of its own background.
- **A clip in parts.** vhs records one terminal, in one theme, so the boot's move from Terminal to
  Ghostty is two tapes, and `finalize.sh --join` joins their frames, the last 0.4 s of the
  Terminal's fading into the first of Ghostty's, its blank window before "hello again": the window
  changing under the boot, as it does on a new Mac.
- **The crop.** A still's screen is as tall as its run's tallest moment, so its last frame can end
  in empty rows. `finalize.sh` finds the first and last rows holding a pixel unlike the margin's,
  reading every pixel, and cuts below the last, leaving under it the margin above the first. Clips
  keep their full height.

`demo/out/` is remade each time and never committed.

## Setting up

```bash
brew install vhs ffmpeg webp
ls ~/Library/Fonts /Library/Fonts | grep JetBrainsMono-Regular
```

vhs 0.12 or later.

## Gotchas

- **The sandbox.** vhs drives a headless browser that connects to `ttyd` over loopback, which an
  agent's sandbox refuses (`could not open ttyd: ... ERR_CONNECTION_REFUSED`): run `record.sh` with
  the sandbox off.
- **Settings come first.** A `Set` after any other command than `Output` and `Source` is ignored,
  and `LetterSpacing` takes no negative value.
- **Quoted paths.** A path with a `/` in a tape must be quoted, or the tape doesn't parse.
- **Silent failures.** vhs can run a tape, report nothing wrong, and write nothing. `record.sh`
  deletes a tape's frames before it records, and stops when there are none after.
- **`TMPDIR` on another volume.** vhs records a tape's frames into a directory under `$TMPDIR`, then
  moves it to the tape's `Output`. Across volumes the move fails without a word: `record.sh` gives
  vhs a `TMPDIR` in `demo/out`.
- **`Wait+Screen` reads the screen.** A scenario's last screen is what its tape waits for, so the
  line it waits on must be on the screen then, not scrolled away.
- **A scenario's keys.** A key pressed before what waits for it is up is lost, and the scenario
  holds still, never reaching its end: `go test ./internal/capture` finds it, as the tape's
  `Wait+Screen` would.
