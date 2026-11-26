# The README's demos

Records the clip and stills in the project's README and docs, `art/`, by drawing kit's real
screens through the capture tool, `cmd/capturetool`: kit's own faces (`internal/render`,
`internal/look`), in-process, from fixed data: the clips' steps are the render tests' own
(`internal/render/rendertest`). kit itself never runs: it changes the Mac it runs on. The capture
tool runs no program and reads or writes nothing of the Mac's, no config, state, home or network,
so it's safe to record anywhere.

Once what [Setting up](#setting-up) lists is installed, a rerun after any change to what kit draws
remakes the art, with nothing else to do:

```bash
demo/record.sh               # all the art: record each tape, then make its art
demo/record.sh status        # the art named
demo/record.sh new-mac       # a clip in parts: each part's tape, then joined
```

## What's recorded

| Tape | Draws | Art |
|---|---|---|
| `new-mac-1-terminal`, `new-mac-2-ghostty` | scenarios `boot`, `arrival` | `art/new-mac.png`, an APNG: the README's lead, a new Mac from the boot's splash in Terminal to kit apply's record in Ghostty; and `demo/out/new-mac.mp4`, the same, for a page that takes a video |
| `status` | fixture `status` | `art/status.webp`, a still: `kit status` on a Mac in daily use, two things waiting on the person: an app declared and not yet installed, a tool installed by hand |
| made by `record.sh` | each fixture that's a window of its own | `art/<name>.webp`, a still each: the README's Screens (`apply-card`, `apply-todos`, `boot-sign-in`, `reconcile`, `log`), and every docs page's, named `<page>-<what>` (`go run ./cmd/capturetool --stills` lists them) |

`look.tape` holds the settings every tape sources; `look-terminal.tape` is the boot's, for what's
recorded in Terminal. A tape's first line says what it makes: `# art: still` a still cut to what
it draws, `# art: whole` a still kept whole, `# art: part of <clip>` a part of a clip, and any
other line a clip of its own. Each tape ends on a
`Wait+Screen` for a line only its run's end draws, so a scenario stuck on a key, or the capture
tool failing, fails the recording rather than leaving art that looks right.

A still that's a window of its own has no tape in `demo/`. `capturetool --stills` says, for each,
its look, its size in cells, the words it shows once drawn, and whether it's kept whole;
`record.sh` writes its tape from that into `demo/out/tapes/`, the window its size, and waits for
those words. Each docs still's window is a column or two wider than what it draws, so the still is
no bigger than its words. Each README screen, `status` too, is the clip's window, 120 × 34, kept
whole, so the grid's screens are one size and their text one scale. `go test ./internal/capture`
holds both to that, and the words to what the screen shows.

The README shows the clip at `width="820"` and each screen at `width="410"`: half and a quarter
of their 1640 px, so a phone draws them sharp.

The art goes to `art/` at the repository's root, so `git diff` shows what a recording changed, and
the art is committed with the change it shows: images alone, as a README doesn't play a video
committed beside it. Each re-record of the clip adds about 6 MB to the public history, which keeps
every version: re-record it when its screens change, never by habit.

## The capture tool

```bash
go run ./cmd/capturetool --list              # every fixture and scenario
go run ./cmd/capturetool --fixture status    # a still, printed at the terminal's width
go run ./cmd/capturetool --fixture reconcile # a still, a window of its own: enter ends it
go run ./cmd/capturetool --scenario boot     # a clip: enter starts it, enter again ends it
```

- **A fixture** is a still: a run's events emitted on kit's face, its clock reading a fixed
  moment, moved on only by the run's own pauses, which take no time. `status` is printed, as
  `kit status` prints its report: its tape types `kit status`, and `demo/bin/kit` is a shim that
  draws the fixture of that command, on a `PATH` without Homebrew's, so the kit installed on the
  Mac can't be reached. The rest are each a window of their own, its size theirs
  (`internal/capture/stills.go`): drawn on a cleared screen, then held, live, till a line on
  stdin; the screens of the clip, at a moment of it, draw from the same run.
- **A scenario** is a clip, played in real time from events and keys scripted at moments into it
  (`internal/capture/scenarios.go`). Its clock reads a fixed day, running on from there, so the
  heads and headers say the same day and minute in every recording; the animations run on kit's
  own timers, as kit draws them. The keys it presses reach kit's faces through a pipe, as they'd
  come from the keyboard, each once the face shows what it answers, a moment after, as a person
  reads it; the script goes on only once the face has taken it, as kit's boot does. It waits for
  a line on stdin to start, the screen cleared, so a tape shows it from its first frame, and holds
  its last screen, live, till the next.
- `go test ./internal/capture` plays every scenario through, its own pauses four times as fast,
  kit's animations in their own time, checks each stops at once wherever it's stopped, and holds
  each still's screen, its words, to `testdata/<name>.golden` (`-update` writes them again);
  `kit`'s own binary is checked never to import the capture.

## The scenarios

- **`boot`**, in Terminal: the splash, the bare machine's noise resolving into the wordmark, enter
  pressed at "press enter"; the self-test played out a line at a time; signing in to GitHub with
  the phone, its QR code the whole window; then the boot order: This Mac chosen, the password
  typed, Homebrew at work with its last lines under it, the apps, kit-config and kit, Full Disk
  Access done by hand, Ghostty opening, and "this window can be closed".
- **`arrival`**, in Ghostty: "hello again", a key pressed; the decode in colour, the wordmark
  locking in kit's gradient, shrinking into the header's corner, its line typed in; then kit
  apply's screen under it, flying through what takes a new Mac half an hour: rows at work,
  1Password's sign-in coming up as a card on the right and signed in, Homebrew counting through
  its installs, the rest done; the to-dos at the end worked through, a card at a time, the backup
  disk left for later; then the run's record, what's left to do and what's done.

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
- **Everything after, in Ghostty:** Nord Wave, Ghostty's own values, from its theme file
  (`Ghostty.app/Contents/Resources/ghostty/themes/Nord Wave`). `look.tape` holds it as one `Set
  Theme` line, with Nordfox's, which kit's look was drawn against, commented out under it:
  switching is swapping which is commented.
- **The font,** both: JetBrains Mono, as every frame of kit's look was designed in, Terminal's too.
  At 22px a cell is 13 × 29 px, so 120 × 34 is 1560 × 986, twice the 780 the README shows it at,
  so a high-density screen draws it sharp. The status still, 80 columns of kit's report, is drawn
  at 30px to come out as wide.
- **Colour:** the capture tool draws in truecolor, as Ghostty and Terminal on macOS 26 show. kit's
  words take the theme's colours, `look.tape`'s; only its wordmark, bar, pink and pills keep
  colours of their own.

## How a tape becomes art

`record.sh` builds the capture tool into `demo/out/`, deletes a tape's old frames, runs it through
vhs, and hands its frames to `finalize.sh`:

- **The frames, not a video.** vhs writes every frame it shows to `demo/out/<name>/`, as the
  terminal drew it. A video's compression shifts every pixel a little from frame to frame, which an
  animated image then stores whole; from the frames themselves it stores only what changed.
- **The art.** For a clip, an APNG, which GitHub shows inline, at the frames' 30 a second, and an
  H.264 MP4 of the same frames in `demo/out/`; for a still, a lossless WebP of the last frame. Each
  frame is set in a 40 px margin of its own background. The APNG's image outside the animation is
  its last frame, apply's record, so what doesn't play an APNG shows where the clip ends, not its
  blank first window.
- **The clip is an APNG, not a WebP.** macOS draws images through ImageIO, which decodes each frame
  of an animated WebP from the first, keyframes or not, so a frame takes longer the later it comes:
  Safari, Quick Look and Preview play a WebP clip at about a quarter of its pace, and iOS draws
  images through ImageIO too. ImageIO plays an APNG at its pace, as Chrome and Firefox play both.
  Stills stay WebP, as ImageIO draws one frame at once.
- **256 colours.** The clip's frames share one palette of 256 colours, made from them all, with no
  dither. A PNG of 256 colours stores a pixel in a byte, not three, which halves the APNG. A screen
  of kit's text holds far fewer colours, so it comes through all but unchanged: at 55 dB or more
  against the frame drawn. The fade and the arrival's noise lose most, at 46 to 48 dB.
- **Noise at 10 frames a second.** The splash's decode and the arrival's change the whole window
  every frame, and no image format squeezes them: a lossy or near-lossless WebP comes out larger
  than a lossless one, and so does the clip drawn at a smaller font. A frame changing more from the
  one before than `noisy` says is noise, and in a run of it one frame in `noise_every`, 3, is kept,
  held for the ones dropped. kit's text, changing a row at a time, and a screen redrawn, a lone
  frame, keep every frame. Each frame's time is rounded from where the frames before it end, so the
  APNG is as long as the MP4.
- **A clip in parts.** vhs records one terminal, in one theme, so the boot's move from Terminal to
  Ghostty is two tapes, and `finalize.sh --join` joins their frames, the last 0.4 s of the
  Terminal's fading into the first of Ghostty's, its blank window before "hello again": the window
  changing under the boot, as it does on a new Mac.
- **The crop.** A docs still's window can be taller than what it draws, so its last frame can end
  in empty rows. `finalize.sh --still` finds the first and last rows holding a pixel unlike the
  margin's, reading every pixel, and cuts below the last, leaving under it the margin above the
  first. `--whole` keeps the window whole, as clips keep theirs.

`demo/out/` is remade each time and never committed.

## Setting up

```bash
brew install vhs ffmpeg webp apngasm go
brew install --cask font-jetbrains-mono
```

vhs 0.12 or later. `record.sh` checks for each before it starts, and names what's missing.

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
- **A scenario's keys.** Each waits for words on the screen, and a key whose words never come holds
  the scenario still, never reaching its end: `go test ./internal/capture` finds it, as the tape's
  `Wait+Screen` would.
