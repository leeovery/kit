package capture_test

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/capture"
	"github.com/leeovery/kit/internal/render/rendertest"
)

var update = flag.Bool("update", false, "rewrite the golden files with what the fixtures draw")

// Each still draws its screen as kit's faces draw it, held by its golden
// file, testdata/<name>.golden, the screen's words without their colours:
// a report printed at 80 columns, the same at 120, as kit draws no wider
// than its look allows; a window of its own at its size, once it holds,
// a column or two wider than what it draws, unless it's the clip's window,
// and in it without scrolling, showing the words its tape waits for.
// -update writes them again.
func TestFixtures(t *testing.T) {
	for _, f := range capture.Fixtures() {
		t.Run(f.Name, func(t *testing.T) {
			t.Parallel()
			got := drawn(t, f, 80)
			path := filepath.Join("testdata", f.Name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s (run with -update to write it): %v", path, err)
			}
			if got != string(want) {
				t.Errorf("drew\n%s\nwant %s's\n%s", got, path, want)
			}
			if wide := drawn(t, f, 120); !f.Held() && wide != got {
				t.Errorf("at 120 columns, drew\n%s\nwant it as at 80", wide)
			}
			if widest := widestLine(got); f.Held() && !f.Whole() && (widest > f.Width-2 || widest < f.Width-6) {
				t.Errorf("drew %d columns in a window %d wide: want it a column or two wider than what it draws", widest, f.Width)
			}
			if lines := strings.Count(got, "\n") + 1; f.Held() && lines > f.Height {
				t.Errorf("drew %d lines in a window %d high: its head scrolls away", lines, f.Height)
			}
			if f.Held() && (!plainWords.MatchString(f.Shows) || !strings.Contains(got, f.Shows)) {
				t.Errorf("shows %q: want words its screen shows, with nothing a tape's Wait+Screen reads as a pattern", f.Shows)
			}
		})
	}
}

// widestLine is how many columns screen's widest line takes.
func widestLine(screen string) int {
	widest := 0
	for l := range strings.SplitSeq(screen, "\n") {
		widest = max(widest, ansi.StringWidth(strings.TrimRight(l, " ")))
	}
	return widest
}

// plainWords are words a tape's Wait+Screen finds as they are.
var plainWords = regexp.MustCompile(`^[A-Za-z0-9 ,'-]+$`)

// drawn is the screen f draws: printed at a terminal width wide; a window
// of its own at its size, as it holds; its running marks as they start,
// whichever way they've turned.
func drawn(t *testing.T, f capture.Fixture, width int) string {
	t.Helper()
	at := &screen{w: width, h: 40}
	if f.Held() {
		at.w, at.h = f.Width, f.Height
	}
	ctx, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	held, drew := make(chan struct{}), make(chan error, 1)
	go func() { drew <- f.Draw(ctx, capture.Screen{Out: at, Size: at.size}, func() { close(held) }) }()
	if !f.Held() {
		if err := <-drew; err != nil {
			t.Fatalf("Draw() = %v", err)
		}
		return turned(rendertest.Screen(at.raw()))
	}
	select {
	case <-held:
	case err := <-drew:
		t.Fatalf("ended before it held its screen: %v\n%s", err, tail(at.text()))
	case <-ctx.Done():
		t.Fatalf("never held its screen\n%s", tail(at.text()))
	}
	shown := at.raw()
	stop()
	if err := <-drew; err != nil {
		t.Fatalf("stopped once held: %v", err)
	}
	return turned(rendertest.Screen(shown))
}

// turned is a screen with its running marks as they start, whichever way
// they've turned since.
func turned(screen string) string {
	return strings.NewReplacer("◓", "◐", "◑", "◐", "◒", "◐").Replace(screen)
}

// A name that names nothing says what there is.
func TestNamedNothing(t *testing.T) {
	if _, err := capture.FixtureNamed("nothing"); err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("FixtureNamed(nothing) = %v; want an error naming status", err)
	}
	if _, err := capture.ScenarioNamed("nothing"); err == nil || !strings.Contains(err.Error(), "boot and arrival") {
		t.Errorf("ScenarioNamed(nothing) = %v; want an error naming boot and arrival", err)
	}
}

// screen is a terminal a scenario plays at, w by h, noting what's drawn.
type screen struct {
	mu   sync.Mutex
	out  bytes.Buffer
	w, h int
}

func (s *screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.Write(p)
}

func (s *screen) size() (int, int) { return s.w, s.h }

// raw is what's been drawn, as it was.
func (s *screen) raw() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

// text is what's been drawn, without its colours and controls.
func (s *screen) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ansi.Strip(s.out.String())
}

// play plays s at a screen of its size, at four times its own pace, and
// returns the screen, a channel told once it's held its last screen, and
// one given what Play returned.
func play(ctx context.Context, s capture.Scenario) (*screen, <-chan struct{}, <-chan error) {
	at := &screen{w: s.Width, h: s.Height}
	held, played := make(chan struct{}), make(chan error, 1)
	go func() {
		played <- s.Faster(4).Play(ctx, capture.Screen{Out: at, Size: at.size}, func() { close(held) })
	}()
	return at, held, played
}

// Each scenario plays through, its keys pressed as what they answer shows,
// to its last screen, held: each of what it shows, shown in turn.
func TestScenariosPlayThrough(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		shows []string
		// ends is what its record matches: kit apply's clock runs on through
		// its half hour, as a new Mac's does.
		ends *regexp.Regexp
	}{
		{"boot", []string{
			"computers are fun", "press enter",
			"POWER-ON SELF-TEST", "[ OK ] Apple M4 Pro  14 cores", "[ OK ] Network  github.com · 41 ms",
			"enter with your phone", "Scan it with your phone's camera", "WDJB-MJHT",
			"[ OK ] GitHub  someone · kit-config has laptop and studio",
			"which of your Macs is this?", "this Mac's password, once", "••••••••••",
			"==> Installation successful!", "drag Ghostty into the list", "choose Open",
			"[ OK ] Ghostty  opened · kit carries on there", "this window can be closed",
		}, nil},
		{"arrival", []string{
			"hello again", "press any key", "bootstrap · laptop · Fri 20 Nov · 09:52",
			"tab next row · ↑↓ scroll · q stop", "Homebrew: 5.0.3 · Xcode tools 26.1", "Homebrew: /opt/homebrew",
			"Installed 1password-cli", "Sign in to 1Password", "Installed slack", "set up · 3 plugins",
			"12 in place", "Homebrew formulae: 129 installed",
			"TO DO", "Allow Karabiner-Elements", "Install the editor from its site", "Add a Time Machine disk",
			"carrying on", "1 to do · ", "TO DO  1 · kit apply brings them back", "DONE",
		}, regexp.MustCompile(`1 to do · \d+ fine · (2\d|3\d)m\d\ds`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := capture.ScenarioNamed(tt.name)
			if err != nil {
				t.Fatal(err)
			}
			ctx, stop := context.WithTimeout(context.Background(), time.Minute)
			defer stop()
			at, held, played := play(ctx, s)
			select {
			case <-held:
			case err := <-played:
				t.Fatalf("ended before its last screen: %v\n%s", err, tail(at.text()))
			case <-ctx.Done():
				t.Fatalf("never reached its last screen: a key it pressed found nothing waiting for it\n%s", tail(at.text()))
			}
			stop()
			if err := <-played; err != nil {
				t.Fatalf("stopped once held: %v", err)
			}
			text, from := at.text(), 0
			for _, want := range tt.shows {
				i := strings.Index(text[from:], want)
				if i < 0 {
					t.Fatalf("after what it showed before, it never showed %q", want)
				}
				from += i + len(want)
			}
			if tt.ends != nil && !tt.ends.MatchString(at.text()) {
				t.Errorf("its record never matched %s\n%s", tt.ends, tail(at.text()))
			}
		})
	}
}

// Stopped while it plays, a scenario ends there, at once and without an
// error: in its splash or arrival, at a question, at work, on apply's
// screen, among its to-dos.
func TestScenarioStoppedWhileItPlays(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, at string }{
		{"boot", "computers are fun"},
		{"boot", "which of your Macs is this?"},
		{"boot", "==> Downloading and installing Homebrew..."},
		{"arrival", "hello again"},
		{"arrival", "Installed 1password"},
		{"arrival", "Allow remote users Full Disk Access"},
	} {
		t.Run(tt.name+" at "+tt.at, func(t *testing.T) {
			t.Parallel()
			s, err := capture.ScenarioNamed(tt.name)
			if err != nil {
				t.Fatal(err)
			}
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			at, _, played := play(ctx, s)
			deadline := time.Now().Add(time.Minute)
			for !strings.Contains(at.text(), tt.at) {
				if time.Now().After(deadline) {
					t.Fatalf("never showed %q", tt.at)
				}
				time.Sleep(10 * time.Millisecond)
			}
			stop()
			select {
			case err := <-played:
				if err != nil {
					t.Fatalf("Play() = %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("still playing 3s after it was stopped")
			}
		})
	}
}

// tail is the end of text, where a scenario stuck shows where it stuck.
func tail(text string) string {
	return text[max(0, len(text)-2000):]
}
