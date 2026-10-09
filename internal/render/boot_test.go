package render_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

var bootSteps = []event.Step{
	{Name: "github", Title: "GitHub", Waiting: "on your phone"},
	{Name: "mac", Title: "This Mac"},
	{Name: "password", Title: "Password", Waiting: "once"},
	{Name: "homebrew", Title: "Homebrew"},
}

func plainView(f *render.BootFace, height int) string {
	lines := f.View(80, height)
	for i, l := range lines {
		lines[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return strings.Join(lines, "\n") + "\n"
}

// The boot's face: the self-test written once; the boot order waiting, the
// rows that will need you saying so; GitHub's code under its row while it
// waits; a question in its row; a step at work with its last lines, and the
// bar once it's gone a while; one done, one failed, saying what to do.
func TestBootFace(t *testing.T) {
	var out bytes.Buffer
	at := time.Date(2026, 10, 8, 18, 2, 0, 0, time.UTC)
	now := at
	f := render.NewBootFace(ask.Terminal{Out: &out}, true, func() time.Time { return now }, nil)
	f.Emit(event.SelfTest{Time: at, Mac: "Some-MacBook", Tests: []event.Test{
		{Name: "Apple M1 Max", State: check.OK, Says: []string{"10 cores"}},
		{Name: "Network", State: check.OK, Says: []string{"github.com", "41 ms"}},
	}})
	var screens strings.Builder
	screens.WriteString(strings.ReplaceAll(ansi.Strip(out.String()), "\r\n", "\n"))
	// Drawn without its terminal's loop, the steps given as Start gives them.
	f.Show(bootSteps)
	screens.WriteString("--- waiting\n" + plainView(f, 40))

	f.Emit(event.StepStarted{Time: at, Step: "github"})
	f.Emit(event.DeviceCode{Time: at, Step: "github", URI: "https://github.com/login/device", Code: "WDJB-MJHT", Expires: at.Add(15 * time.Minute)})
	f.Emit(event.Doing{Time: at, Step: "github", Says: "waiting for your phone"})
	now = at.Add(39 * time.Second)
	screens.WriteString("--- the code\n" + plainView(f, 40))

	f.Emit(event.StepFinished{Time: now, Step: "github", Result: check.Result{State: check.OK, Summary: "someone · kit-config has laptop and studio"}})
	chosen := make(chan int)
	go func() {
		i, _ := f.Choose(context.Background(), "mac", "which of your Macs is this?", []look.Choice{{Label: "laptop", Does: "MacBook, the primary"}, {Label: "studio"}})
		chosen <- i
	}()
	waitFor(t, func() bool { return strings.Contains(plainView(f, 40), "which of your Macs") })
	f.Key(ask.Key{Name: "down"})
	screens.WriteString("--- a question\n" + plainView(f, 40))
	f.Key(ask.Key{Name: "enter"})
	if i := <-chosen; i != 1 {
		t.Errorf("chose %d; want studio", i)
	}
	f.Emit(event.StepFinished{Time: now, Step: "mac", Result: check.Result{State: check.OK, Summary: "studio"}})
	f.Emit(event.StepFinished{Time: now, Step: "password", Result: check.Result{State: check.Failed, Reason: "sudo refused the password: run kit bootstrap again"}})
	f.Emit(event.StepStarted{Time: now, Step: "homebrew"})
	f.Emit(event.Doing{Time: now, Step: "homebrew", Says: "installing"})
	f.Emit(event.Output{Time: now, Step: "homebrew", Line: "==> Installing Command Line Tools"})
	now = now.Add(2*time.Minute + 14*time.Second)
	screens.WriteString("--- at work\n" + plainView(f, 40))
	golden(t, "boot.golden", screens.String())
}

// Never taller than the terminal: rows done, then those waiting, give way,
// so what's at work stays whole.
func TestBootFaceFits(t *testing.T) {
	at := time.Date(2026, 10, 8, 18, 2, 0, 0, time.UTC)
	f := render.NewBootFace(ask.Terminal{Out: &bytes.Buffer{}}, true, func() time.Time { return at }, nil)
	f.Show(bootSteps)
	f.Emit(event.StepStarted{Time: at, Step: "github"})
	f.Emit(event.DeviceCode{Time: at, Step: "github", URI: "https://github.com/login/device", Code: "WDJB-MJHT", Expires: at.Add(15 * time.Minute)})
	got := f.View(80, 6)
	if len(got) > 6 {
		t.Fatalf("%d lines at 6", len(got))
	}
	text := plainView(f, 6)
	if !strings.Contains(text, "WDJB-MJHT") || strings.Contains(text, "Homebrew") {
		t.Errorf("at 6 lines:\n%s\nwant the code, rows waiting given way", text)
	}
}

// Enter while GitHub's code shows gives its QR code the whole screen, and
// enter again gives it back; approved, the code's gone, and the screen.
func TestBootFaceQRCode(t *testing.T) {
	at := time.Date(2026, 10, 8, 18, 2, 0, 0, time.UTC)
	f := render.NewBootFace(ask.Terminal{Out: &bytes.Buffer{}}, true, func() time.Time { return at }, nil)
	f.Emit(event.SelfTest{Time: at, Mac: "Some-MacBook"})
	f.Show(bootSteps)
	f.Emit(event.StepStarted{Time: at, Step: "github"})
	f.Emit(event.DeviceCode{Time: at, Step: "github", URI: "https://github.com/login/device", Code: "WDJB-MJHT", Expires: at.Add(15 * time.Minute)})
	if f.Full(120, 30) != nil {
		t.Fatal("the QR code took the screen before enter")
	}
	f.Key(ask.Key{Name: "enter"})
	full := f.Full(120, 30)
	var lines []string
	for _, l := range full {
		lines = append(lines, strings.TrimSpace(ansi.Strip(l)))
	}
	text := strings.Join(lines, "\n")
	if len(full) != 30 || !strings.Contains(text, "WDJB-MJHT") || !strings.Contains(text, "Some-MacBook") || lines[len(lines)-1] != "enter back · esc stop" {
		t.Errorf("full screen:\n%s", text)
	}
	f.Key(ask.Key{Name: "enter"})
	if f.Full(120, 30) != nil {
		t.Error("enter again didn't give the screen back")
	}
	f.Key(ask.Key{Name: "enter"})
	f.Emit(event.StepFinished{Time: at, Step: "github", Result: check.Result{State: check.OK, Summary: "someone"}})
	if f.Full(120, 30) != nil {
		t.Error("approved, the QR code kept the screen")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("waited too long")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The splash: the bare machine's noise, the wordmark resolving out of it;
// any key skips to its end, the tagline typed and the prompt up; enter
// there ends it, esc stops; it leaves nothing.
func TestSplash(t *testing.T) {
	s := render.NewSplash(true)
	text := func() string {
		var out []string
		for _, l := range s.Full(120, 30) {
			out = append(out, ansi.Strip(l))
		}
		return strings.Join(out, "\n")
	}
	if first := text(); strings.Contains(first, render.Tagline) || strings.Count(first, "█") > 400 {
		t.Errorf("the first frame should be noise, the wordmark not yet resolved:\n%s", first)
	}
	if s.Key(ask.Key{Text: "x"}) {
		t.Fatal("a key before the end ended it")
	}
	if end := text(); !strings.Contains(end, render.Tagline) || !strings.Contains(end, "press enter") {
		t.Errorf("skipped to the end:\n%s", end)
	}
	if !s.Key(ask.Key{Name: "enter"}) || s.Stopped() {
		t.Error("enter at the end didn't start the boot")
	}
	if stop := render.NewSplash(true); !stop.Key(ask.Key{Name: "esc"}) || !stop.Stopped() {
		t.Error("esc didn't stop it")
	}
	if s.Leaves(120) != nil || s.View(120, 30) != nil {
		t.Error("it drew something in place")
	}
}
