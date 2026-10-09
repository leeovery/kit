package render_test

import (
	"bytes"
	"context"
	"io"
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
	{Name: "github", Title: "GitHub", Area: "Sign in", Waiting: "This Mac is set up from your kit-config, a private repository on GitHub. Sign in to GitHub, and kit fetches it."},
	{Name: "mac", Title: "This Mac", Area: "Boot order"},
	{Name: "password", Title: "Password", Area: "Boot order", Waiting: "once"},
	{Name: "homebrew", Title: "Homebrew", Area: "Boot order"},
}

var signInKeys = []look.Key{{Key: "enter", Does: "with your phone"}, {Key: "s", Does: "here, in Safari"}}

func plainView(f *render.BootFace, height int) string {
	lines := f.View(80, height)
	for i, l := range lines {
		lines[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return strings.Join(lines, "\n") + "\n"
}

func plainFull(f *render.BootFace) string {
	var out []string
	for _, l := range f.Full(120, 30) {
		out = append(out, strings.TrimRight(ansi.Strip(l), " "))
	}
	return strings.Join(out, "\n")
}

// The boot's face, one thing at a time: signing in says what it's for and
// how, the code coming only once a way's chosen; signed in, it's a line,
// and the boot order shows, a question in its row, a step at work with its
// last lines.
func TestBootFace(t *testing.T) {
	at := time.Date(2026, 10, 8, 18, 2, 0, 0, time.UTC)
	now := at
	f := render.NewBootFace(ask.Terminal{Out: &bytes.Buffer{}}, true, func() time.Time { return now }, nil)
	f.Show(bootSteps)
	var screens strings.Builder
	picked := make(chan string, 1)
	go func() {
		key, _ := f.Pick(context.Background(), "github", signInKeys)
		picked <- key
	}()
	waitFor(t, func() bool { return strings.Contains(plainView(f, 40), "with your phone") })
	screens.WriteString("--- signing in\n" + plainView(f, 40))

	f.Emit(event.StepStarted{Time: at, Step: "github"})
	f.Key(ask.Key{Name: "enter"})
	if key := <-picked; key != "enter" {
		t.Fatalf("picked %q", key)
	}
	f.Emit(event.StepFinished{Time: now, Step: "github", Result: check.Result{State: check.OK, Summary: "someone · kit-config has laptop and studio"}})
	chosen := make(chan int)
	go func() {
		i, _ := f.Choose(context.Background(), "mac", "which of your Macs is this?", []look.Choice{{Label: "laptop", Does: "MacBook, the primary"}, {Label: "studio"}})
		chosen <- i
	}()
	waitFor(t, func() bool { return strings.Contains(plainView(f, 40), "which of your Macs") })
	f.Key(ask.Key{Name: "down"})
	screens.WriteString("--- signed in, a question\n" + plainView(f, 40))
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

var orderSteps = []event.Step{
	{Name: "github", Title: "GitHub", Area: "Sign in", Waiting: "This Mac is set up from your kit-config, a private repository on GitHub. Sign in to GitHub, and kit fetches it."},
	{Name: "mac", Title: "This Mac", Area: "Boot order"},
	{Name: "password", Title: "Password", Area: "Boot order", Waiting: "once"},
	{Name: "homebrew", Title: "Homebrew", Area: "Boot order"},
	{Name: "apps", Title: "Apps", Area: "Boot order"},
	{Name: "kit-config", Title: "kit-config", Area: "Boot order"},
	{Name: "full-disk-access", Title: "Full Disk Access", Area: "Boot order", Waiting: "for Ghostty"},
	{Name: "terminal", Title: "Ghostty", Area: "Boot order"},
}

// What the boot needs of you, in its row: the password, a dot a character;
// Full Disk Access, what to do, then enter; and while Ghostty opens, what
// macOS asks. A step that took a while, without asking, says how long.
func TestBootFaceNeedsYou(t *testing.T) {
	at := time.Date(2026, 10, 9, 15, 2, 0, 0, time.UTC)
	f := render.NewBootFace(ask.Terminal{Out: &bytes.Buffer{}}, true, func() time.Time { return at }, nil)
	f.Show(orderSteps)
	ok := func(step, summary string, took time.Duration) {
		f.Emit(event.StepStarted{Time: at, Step: step})
		f.Emit(event.StepFinished{Time: at, Step: step, Result: check.Result{State: check.OK, Summary: summary}, Duration: took})
	}
	var screens strings.Builder
	ok("github", "someone · kit-config has laptop and studio", 3*time.Second)
	ok("mac", "laptop", 0)
	f.Emit(event.StepStarted{Time: at, Step: "password"})
	typed := make(chan string, 1)
	go func() {
		pw, _ := f.Secret(context.Background(), "password", "this Mac's password, once")
		typed <- pw
	}()
	waitFor(t, func() bool { return strings.Contains(plainView(f, 40), "this Mac's password") })
	for _, k := range []string{"s", "e", "c", "r", "e"} {
		f.Key(ask.Key{Text: k})
	}
	f.Key(ask.Key{Name: "backspace"})
	screens.WriteString("--- the password\n" + plainView(f, 40))
	f.Key(ask.Key{Name: "enter"})
	if pw := <-typed; pw != "secr" {
		t.Errorf("typed %q", pw)
	}
	f.Emit(event.StepFinished{Time: at, Step: "password", Result: check.Result{State: check.OK, Summary: "Touch ID on for sudo"}, Duration: 12 * time.Second})
	ok("homebrew", "7.0.9 · Xcode tools 27.0", 6*time.Minute+2*time.Second)
	ok("apps", "Ghostty 1.3.1 · 1Password 8.12.40", 98*time.Second)
	ok("kit-config", "cloned · 61 files linked", 6*time.Second)
	f.Emit(event.StepStarted{Time: at, Step: "full-disk-access"})
	done := make(chan string, 1)
	go func() {
		key, _ := f.Wait(context.Background(), "full-disk-access", "for Ghostty, before it first opens",
			"System Settings is open at Full Disk Access, and Finder at Applications: drag Ghostty into the list, then press enter", []look.Key{{Key: "enter", Does: "done"}})
		done <- key
	}()
	waitFor(t, func() bool { return strings.Contains(plainView(f, 40), "drag Ghostty") })
	screens.WriteString("--- Full Disk Access\n" + plainView(f, 40))
	f.Key(ask.Key{Name: "enter"})
	if key := <-done; key != "enter" || f.Full(120, 30) != nil {
		t.Errorf("pressed %q; want enter, and no full screen", key)
	}
	f.Emit(event.StepFinished{Time: at, Step: "full-disk-access", Result: check.Result{State: check.OK, Summary: "granted to Ghostty"}, Duration: 40 * time.Second})
	f.Emit(event.StepStarted{Time: at, Step: "terminal"})
	f.Emit(event.Doing{Time: at, Step: "terminal", Says: "opening", Todo: "macOS asks before Ghostty first opens: choose Open"})
	screens.WriteString("--- the hand-off\n" + plainView(f, 40))
	golden(t, "boot-needs-you.golden", screens.String())
}

// Signing in takes the whole window, the way chosen: with the phone, the QR
// code and the code; esc goes back, and the other way can be chosen; here,
// the code to enter in Safari. Signed in, the window's given back.
func TestBootFaceSignIn(t *testing.T) {
	at := time.Date(2026, 10, 8, 18, 2, 0, 0, time.UTC)
	f := render.NewBootFace(ask.Terminal{Out: &bytes.Buffer{}}, true, func() time.Time { return at }, nil)
	f.Emit(event.SelfTest{Time: at, Mac: "Some-MacBook"})
	f.Show(bootSteps)
	pick := func(key string) {
		t.Helper()
		got := make(chan string, 1)
		go func() {
			k, _ := f.Pick(context.Background(), "github", signInKeys)
			got <- k
		}()
		waitFor(t, func() bool { return strings.Contains(plainView(f, 40), "with your phone") })
		f.Key(ask.Key{Name: key, Text: key})
		if k := <-got; k != key {
			t.Fatalf("picked %q; want %q", k, key)
		}
	}
	if f.Full(120, 30) != nil {
		t.Fatal("full screen before a way was chosen")
	}
	pick("enter")
	if full := plainFull(f); !strings.Contains(full, "getting a code") {
		t.Errorf("before the code:\n%s", full)
	}
	f.Emit(event.StepStarted{Time: at, Step: "github"})
	f.Emit(event.DeviceCode{Time: at, Step: "github", URI: "https://github.com/login/device", Code: "WDJB-MJHT", Expires: at.Add(15 * time.Minute)})
	phone := plainFull(f)
	for _, want := range []string{"Some-MacBook", "█████", "Scan it with your phone's camera", "WDJB-MJHT", "s here, in Safari, instead · esc back"} {
		if !strings.Contains(phone, want) {
			t.Errorf("with the phone, want %q:\n%s", want, phone)
		}
	}
	f.Key(ask.Key{Name: "esc"})
	if f.Full(120, 30) != nil {
		t.Fatal("esc didn't go back")
	}
	pick("s")
	here := plainFull(f)
	for _, want := range []string{"Safari is open at github.com/login/device.", "Enter this code there:", "WDJB-MJHT", "enter with your phone instead · esc back"} {
		if !strings.Contains(here, want) {
			t.Errorf("here, want %q:\n%s", want, here)
		}
	}
	if strings.Contains(here, "█████") {
		t.Error("the QR code shows when signing in here")
	}
	f.Emit(event.StepFinished{Time: at, Step: "github", Result: check.Result{State: check.OK, Summary: "someone"}})
	if f.Full(120, 30) != nil {
		t.Error("signed in, the full screen stayed")
	}
}

// Never taller than the terminal: rows done, then those waiting, give way,
// so what's at work stays whole.
func TestBootFaceFits(t *testing.T) {
	at := time.Date(2026, 10, 8, 18, 2, 0, 0, time.UTC)
	f := render.NewBootFace(ask.Terminal{Out: &bytes.Buffer{}}, true, func() time.Time { return at }, nil)
	f.Show(bootSteps)
	f.Emit(event.StepFinished{Time: at, Step: "github", Result: check.Result{State: check.OK, Summary: "someone"}})
	f.Emit(event.StepFinished{Time: at, Step: "mac", Result: check.Result{State: check.OK, Summary: "studio"}})
	f.Emit(event.StepStarted{Time: at, Step: "password"})
	f.Emit(event.Doing{Time: at, Step: "password", Says: "asking"})
	if got := f.View(80, 6); len(got) > 6 {
		t.Fatalf("%d lines at 6", len(got))
	}
	text := plainView(f, 6)
	if !strings.Contains(text, "Password") || strings.Contains(text, "This Mac") {
		t.Errorf("at 6 lines:\n%s\nwant the step at work, rows done given way", text)
	}
}

// The self-test plays out a line at a time, then stays, each line saying
// what it found.
func TestPlaySelfTest(t *testing.T) {
	in, keys := io.Pipe()
	defer func() { _ = keys.Close() }()
	var out bytes.Buffer
	f := render.NewBootFace(ask.Terminal{In: in, Out: &out, Size: func() (int, int) { return 80, 24 }}, true, time.Now, nil)
	err := f.PlaySelfTest(context.Background(), event.SelfTest{Time: time.Date(2026, 10, 8, 18, 2, 0, 0, time.UTC), Mac: "Some-MacBook", Tests: []event.Test{
		{Name: "Apple M1 Max", State: check.OK, Says: []string{"10 cores"}},
		{Name: "Network", State: check.OK, Says: []string{"github.com", "41 ms"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := ansi.Strip(out.String())
	for _, want := range []string{"bootstrap", "Some-MacBook", "Apple M1 Max", "[ OK ] Apple M1 Max  10 cores\r\n", "[ OK ] Network  github.com · 41 ms\r\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("played %q; want it to hold %q", got, want)
		}
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
