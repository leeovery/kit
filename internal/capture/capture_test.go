package capture_test

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/capture"
)

var update = flag.Bool("update", false, "rewrite the golden files with what the fixtures draw")

// The status still is kit status on a Mac in daily use, two things
// waiting on the person, as kit's face prints it, at any width: kit draws
// no wider than its look allows. Its words held by status.golden, without
// colour; -update writes it again.
func TestStatusFixture(t *testing.T) {
	f, err := capture.FixtureNamed("status")
	if err != nil {
		t.Fatal(err)
	}
	draw := func(width int) string {
		var out bytes.Buffer
		if err := f.Draw(&colorprofile.Writer{Forward: &out, Profile: colorprofile.NoTTY}, width); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	got, path := draw(80), filepath.Join("testdata", "status.golden")
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
		t.Errorf("drew\n%s\nwant status.golden's\n%s", got, want)
	}
	if wide := draw(120); wide != got {
		t.Errorf("at 120 columns, drew\n%s\nwant it as at 80", wide)
	}
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
	}{
		{"boot", []string{
			"computers are fun", "press enter",
			"POWER-ON SELF-TEST", "[ OK ] Apple M4 Pro  14 cores", "[ OK ] Network  github.com · 41 ms",
			"enter with your phone", "Scan it with your phone's camera", "WDJB-MJHT",
			"[ OK ] GitHub  someone · kit-config has laptop and studio",
			"which of your Macs is this?", "this Mac's password, once", "••••••••••",
			"==> Installation successful!", "drag Ghostty into the list", "choose Open",
			"[ OK ] Ghostty  opened · kit carries on there", "this window can be closed",
		}},
		{"arrival", []string{
			"hello again", "press any key", "bootstrap · laptop · Fri 20 Nov · 09:52",
			"tab next row · ↑↓ scroll · q stop", "Homebrew: 5.0.3 · Xcode tools 26.1", "Homebrew: /opt/homebrew",
			"Installed 1password-cli", "Sign in to 1Password", "Installed slack", "set up · 3 plugins",
		}},
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
		})
	}
}

// Stopped while it plays, a scenario ends there, at once and without an
// error: in its splash or arrival, at a question, at work, on apply's
// screen.
func TestScenarioStoppedWhileItPlays(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, at string }{
		{"boot", "computers are fun"},
		{"boot", "which of your Macs is this?"},
		{"boot", "==> Downloading and installing Homebrew..."},
		{"arrival", "hello again"},
		{"arrival", "Installed 1password"},
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
