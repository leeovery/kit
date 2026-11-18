package render_test

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/render/rendertest"
	"github.com/leeovery/kit/internal/setup"
	"github.com/leeovery/kit/internal/snapshot"
)

// setupFace is kit setup's face, not yet shown, given events, stopping
// with stop.
func setupFace(stop func(), events ...event.Event) *render.SetupFace {
	f := render.NewSetupFace(ask.Terminal{Out: io.Discard}, true, func() time.Time { return rendertest.At }, stop, func() string { return "laptop" })
	for _, e := range events {
		f.Emit(e)
	}
	return f
}

// asking is f asking, in the background, what ask asks: once f's view
// shows text, f, and what it was answered once it's ended.
func asking(t *testing.T, f *render.SetupFace, text string, ask func() error) <-chan error {
	t.Helper()
	answered := make(chan error, 1)
	go func() { answered <- ask() }()
	for range 500 {
		if strings.Contains(ansi.Strip(strings.Join(f.View(80, 60), "\n")), text) {
			return answered
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("never asked %q", text)
	return nil
}

// Setup's screen fits a phone's 52 columns, the maintainer's panes of 73,
// 108 and 138, and 80: never wider than the terminal, nor than kit's look
// at the widest, nothing cut but what's never decided on; the question
// asked, whole.
func TestSetupScreenFitsEveryWidth(t *testing.T) {
	done := rendertest.SetupDone(rendertest.At)
	commit := slices.IndexFunc(done, func(e event.Event) bool {
		s, ok := e.(event.StepStarted)
		return ok && s.Step == setup.DataStep
	})
	for _, width := range []int{52, 73, 80, 108, 138} {
		f := setupFace(nil, done...)
		f.Finish("kit apply", "carries on now")
		check := func(what string, lines []string) {
			for _, l := range lines {
				if w := ansi.StringWidth(l); w > min(width, look.Width) {
					t.Errorf("at %d columns, %s drew %d: %q", width, what, w, ansi.Strip(l))
				}
			}
		}
		left := f.Leaves(width)
		check("the end", left)
		text := ansi.Strip(strings.Join(left, "\n"))
		if !strings.Contains(text, "INSTALLED BY HAND  3") || !strings.HasSuffix(text, "kit apply carries on now") ||
			!strings.Contains(strings.Join(strings.Fields(text), " "), "→ kit keeps secrets in a store, never in kit-config: kit secret add") {
			t.Errorf("at %d columns, left\n%s\nwant what to do whole", width, text)
		}

		asked := setupFace(func() {}, done[:commit]...)
		ticks := []look.Tick{{Name: "~/Library/Mobile Documents/com~apple~CloudDocs/Projects", More: "over 12.4 GB", Ticked: true}}
		answered := asking(t, asked, "bring back", func() error {
			_, err := asked.Tick(context.Background(), setup.DataStep, "which folders should a new Mac bring back from your backups?", ticks)
			return err
		})
		view := asked.View(width, 60)
		check("the question", view)
		if text := ansi.Strip(strings.Join(view, "\n")); !strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(text, "│", "")), " "), "which folders should a new Mac bring back from your backups?") {
			t.Errorf("at %d columns, asked\n%s\nwant the question whole", width, text)
		}
		asked.Key(ask.Key{Name: "enter"})
		if err := <-answered; err != nil {
			t.Errorf("enter answered %v", err)
		}
	}
}

// A screen too short for setup gives way, rows done first, then those
// waiting, then the wordmark, so the question asked stays whole, its keys
// under it.
func TestSetupScreenGivesWay(t *testing.T) {
	done := rendertest.SetupDone(rendertest.At)
	at := slices.IndexFunc(done, func(e event.Event) bool {
		s, ok := e.(event.StepStarted)
		return ok && s.Step == setup.PrefsStep
	})
	f := setupFace(func() {}, done[:at]...)
	answered := asking(t, f, "save them", func() error {
		_, err := f.Choose(context.Background(), setup.PrefsStep, "save them every night?", []look.Choice{{Label: "Save them every night"}, {Label: "Not now"}})
		return err
	})
	view := ansi.Strip(strings.Join(f.View(80, 12), "\n"))
	if lines := strings.Count(view, "\n") + 1; lines > 12 || !strings.Contains(view, "▲ Apps' settings  save them every night?\n  │ ❯ Save them every night \n  │   Not now") || !strings.HasSuffix(view, "esc stop") || strings.Contains(view, "Homebrew formulae") {
		t.Errorf("12 lines high, drew\n%s\nwant the question whole, its keys, rows done gone first", view)
	}
	f.Key(ask.Key{Name: "down"})
	f.Key(ask.Key{Name: "enter"})
	if err := <-answered; err != nil {
		t.Error(err)
	}
}

// q stops setup, as esc does, but while a name's typed, when it's typed;
// space ticks a thing on a checklist, and enter keeps what's ticked.
func TestSetupScreenKeys(t *testing.T) {
	done := rendertest.SetupDone(rendertest.At)
	snapshot := slices.IndexFunc(done, func(e event.Event) bool {
		s, ok := e.(event.StepStarted)
		return ok && s.Step == setup.DataStep
	})
	stopped := 0
	f := setupFace(func() { stopped++ }, done[:snapshot]...)
	typed := make(chan string, 1)
	asking(t, f, "call it", func() error {
		name, err := f.Text(context.Background(), setup.MacStep, "what will kit-config call it?", "mac")
		typed <- name
		return err
	})
	for _, k := range []ask.Key{{Name: "backspace"}, {Text: "q"}, {Text: "x"}, {Name: "enter"}} {
		f.Key(k)
	}
	if got := <-typed; got != "maqx" || stopped != 0 {
		t.Errorf("typed %q, stopped %d times; want maqx, q typed", got, stopped)
	}

	kept := make(chan []bool, 1)
	asking(t, f, "keep", func() error {
		ticked, err := f.Tick(context.Background(), setup.FilesStep, "which should kit-config keep?", []look.Tick{{Name: "~/.zshrc", Ticked: true}, {Name: "~/.tmux.conf", Ticked: true}})
		kept <- ticked
		return err
	})
	for _, k := range []ask.Key{{Name: "down"}, {Text: " "}, {Name: "enter"}} {
		f.Key(k)
	}
	if got := <-kept; !slices.Equal(got, []bool{true, false}) {
		t.Errorf("kept %v, want the first alone", got)
	}

	answered := asking(t, f, "save them", func() error {
		_, err := f.Choose(context.Background(), setup.PrefsStep, "save them every night?", []look.Choice{{Label: "Yes"}, {Label: "No"}})
		return err
	})
	f.Key(ask.Key{Text: "q"})
	if err := <-answered; err != ask.ErrCancelled || stopped != 1 {
		t.Errorf("q answered %v, stopped %d times; want it cancelled, setup stopped", err, stopped)
	}
}

// Signing in, the way chosen takes the whole window, the code with it, at
// any width; the other way's key changes it; esc gives the window back,
// the code under GitHub's row, the ways' keys at the foot.
func TestSetupScreenSignsIn(t *testing.T) {
	f := setupFace(func() {}, rendertest.SetupDone(rendertest.At)[0], event.StepStarted{Time: rendertest.At, Step: setup.GitHubStep})
	keys := []look.Key{{Key: "enter", Does: "with your phone"}, {Key: "s", Does: "here, in your browser"}}
	pick := func(text string) <-chan string {
		picked := make(chan string, 1)
		asking(t, f, text, func() error {
			k, err := f.Pick(t.Context(), setup.GitHubStep, keys)
			picked <- k
			return err
		})
		return picked
	}
	way := pick("how will you sign in?")
	f.Key(ask.Key{Name: "down"})
	f.Key(ask.Key{Name: "enter"})
	if got := <-way; got != "s" {
		t.Fatalf("picked %q, want s", got)
	}
	f.Emit(event.DeviceCode{Time: rendertest.At, Step: setup.GitHubStep, URI: "https://github.com/login/device", Code: "WDJB-MJHT", Expires: rendertest.At.Add(15 * time.Minute)})
	fits := func(says string) {
		for _, width := range []int{52, 73, 80} {
			full := f.Full(width, 40)
			if text := ansi.Strip(strings.Join(full, "\n")); !strings.Contains(text, says) || !strings.Contains(text, "WDJB-MJHT") {
				t.Errorf("at %d columns, the window is\n%s\nwant it to say %q, and the code", width, text, says)
			}
			for _, l := range full {
				if w := ansi.StringWidth(l); w > width {
					t.Errorf("at %d columns, signing in drew %d: %q", width, w, ansi.Strip(l))
				}
			}
		}
	}
	fits("Your browser is open at github.com/login/device.")
	way = pick("here, in your browser · esc stop")
	f.Key(ask.Key{Name: "enter"})
	if got := <-way; got != "enter" {
		t.Fatalf("picked %q, want enter", got)
	}
	fits("Scan it with your phone's camera")
	pick("here, in your browser · esc stop")
	f.Key(ask.Key{Name: "esc"})
	view := ansi.Strip(strings.Join(f.View(80, 30), "\n"))
	if f.Full(80, 30) != nil || !strings.Contains(view, "→ enter WDJB-MJHT at github.com/login/device") || !strings.HasSuffix(view, "enter with your phone · s here, in your browser · esc stop") {
		t.Errorf("esc: the window given back, drew\n%s", view)
	}
}

// The snapshot's parts show as they're found, a row each; the folders
// being sized show on Your data's row, the question they're for. Setup's
// commit shows on kit-config's row: at work, then pushed, or, the push
// failing, why, and what to do, under it.
func TestSetupScreenFindsAndCommits(t *testing.T) {
	done := rendertest.SetupDone(rendertest.At)
	started := func(step string) int {
		return slices.IndexFunc(done, func(e event.Event) bool {
			s, ok := e.(event.StepStarted)
			return ok && s.Step == step
		})
	}
	f := setupFace(nil, done[:started(setup.SnapshotStep)+1]...)
	for _, e := range []event.Finding{
		{Step: setup.SnapshotStep, Part: "Homebrew formulae"}, {Step: setup.SnapshotStep, Part: "macOS settings"},
		{Step: setup.SnapshotStep, Part: "Homebrew formulae", Found: true, Count: 12},
		{Step: setup.SnapshotStep, Part: "macOS settings", Doing: "reading what's here"},
		{Step: setup.SnapshotStep, Part: snapshot.FoldersLabel, Doing: "sizing your folders"},
	} {
		f.Emit(e)
	}
	want := "● Homebrew formulae  12\n  ◐ macOS settings  reading what's here\n  ◐ Your data  sizing your folders\n  ○ Apps' settings"
	if view := ansi.Strip(strings.Join(f.View(80, 40), "\n")); !strings.Contains(view, want) {
		t.Errorf("finding, drew\n%s\nwant it to hold\n%s", view, want)
	}

	f = setupFace(nil, done[:started(setup.CommitStep)+1]...)
	if view := ansi.Strip(strings.Join(f.View(80, 40), "\n")); !strings.Contains(view, "◐ kit-config  committing and pushing") {
		t.Errorf("committing, drew\n%s", view)
	}
	f.Emit(event.StepFinished{Time: rendertest.At, Step: setup.CommitStep, Result: check.Result{State: check.Attention,
		Summary: "370 declared · committed, not pushed: the remote end hung up; kit setup again carries on"}})
	want = "▲ kit-config  370 declared · committed, not pushed\n  │ → the remote end hung up; kit setup again carries on\n"
	if view := ansi.Strip(strings.Join(f.View(80, 40), "\n")); !strings.Contains(view, want) {
		t.Errorf("the push failing, drew\n%s\nwant it to hold\n%s", view, want)
	}
}
