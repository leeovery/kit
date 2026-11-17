package capture_test

import (
	"bytes"
	"context"
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

// The status still is the screen kit's face prints for the run its golden
// file holds, colour and all, at any width: kit draws no wider than its
// look allows.
func TestStatusIsTheGoldensScreen(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "render", "testdata", "pretty-colour.golden"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := capture.FixtureNamed("status")
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{80, 120} {
		var out bytes.Buffer
		if err := f.Draw(&colorprofile.Writer{Forward: &out, Profile: colorprofile.TrueColor}, width); err != nil {
			t.Fatal(err)
		}
		if out.String() != string(want) {
			t.Errorf("at %d columns, drew\n%s\nwant pretty-colour.golden's\n%s", width, out.String(), want)
		}
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

// Each scenario plays through in real time, as its tape records it, its
// keys landing on what waits for them, to its last screen, held: each of
// what it shows, shown in turn.
func TestScenariosPlayThrough(t *testing.T) {
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
			"tab next row · ↑↓ scroll · q stop",
			"Installed 1password-cli", "Sign in to 1Password", "Installed slack", "set up · 3 plugins",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, err := capture.ScenarioNamed(tt.name)
			if err != nil {
				t.Fatal(err)
			}
			at := &screen{w: s.Width, h: s.Height}
			ctx, stop := context.WithTimeout(context.Background(), time.Minute)
			defer stop()
			held := make(chan struct{})
			played := make(chan error, 1)
			go func() { played <- s.Play(ctx, capture.Screen{Out: at, Size: at.size}, func() { close(held) }) }()
			select {
			case <-held:
			case err := <-played:
				t.Fatalf("ended before its last screen: %v\n%s", err, at.text())
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
// error, taking down what it showed.
func TestScenarioStoppedWhileItPlays(t *testing.T) {
	for _, s := range capture.Scenarios() {
		t.Run(s.Name, func(t *testing.T) {
			t.Parallel()
			at := &screen{w: s.Width, h: s.Height}
			ctx, stop := context.WithTimeout(context.Background(), 700*time.Millisecond)
			defer stop()
			began := time.Now()
			if err := s.Play(ctx, capture.Screen{Out: at, Size: at.size}, nil); err != nil {
				t.Fatalf("Play() = %v", err)
			}
			if took := time.Since(began); took > 3*time.Second {
				t.Errorf("took %s to stop", took)
			}
		})
	}
}

// tail is the end of text, where a scenario stuck shows where it stuck.
func tail(text string) string {
	return text[max(0, len(text)-2000):]
}
