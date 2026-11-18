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
	"github.com/leeovery/kit/internal/boot"
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

// setupTill is setup's run up to the start of step, not taking it in.
func setupTill(step string) []event.Event {
	done := rendertest.SetupDone(rendertest.At)
	return done[:slices.IndexFunc(done, func(e event.Event) bool {
		s, ok := e.(event.StepStarted)
		return ok && s.Step == step
	})]
}

// started is setup's run up to step, started.
func started(step string) []event.Event {
	return append(setupTill(step), event.StepStarted{Time: rendertest.At, Step: step})
}

// asking is f asking, in the background, what ask asks: once f's view
// shows text, f, and what it was answered once it's ended.
func asking(t *testing.T, f *render.SetupFace, text string, ask func() error) <-chan error {
	t.Helper()
	answered := make(chan error, 1)
	go func() { answered <- ask() }()
	for range 500 {
		if strings.Contains(viewed(f, 80, 60), text) {
			return answered
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("never asked %q:\n%s", text, viewed(f, 80, 60))
	return nil
}

// viewed is f's view, width wide and height high, plain.
func viewed(f *render.SetupFace, width, height int) string {
	return ansi.Strip(strings.Join(f.View(width, height), "\n"))
}

// rowEnds are the last words of each row in lines, a row a line its mark
// starts.
func rowEnds(lines []string) []string {
	var out []string
	for _, l := range lines {
		words := strings.Fields(ansi.Strip(l))
		if len(words) > 1 && slices.Contains([]string{"●", "▲", "✗", "–", "○"}, words[0]) {
			out = append(out, words[len(words)-1])
		}
	}
	return out
}

// Setup's screen fits a phone's 52 columns, the maintainer's panes of 73,
// 108 and 138, and 80: never wider than the terminal, nor than kit's look
// at the widest; every row's outcome kept, as its last word; the question
// asked, whole.
func TestSetupScreenFitsEveryWidth(t *testing.T) {
	done := rendertest.SetupDone(rendertest.At)
	whole := setupFace(nil, done...)
	whole.Finish("kit apply", "carries on now")
	ends := rowEnds(whole.Leaves(look.Width))
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
		if got := rowEnds(left); !slices.Equal(got, ends) {
			t.Errorf("at %d columns, the rows end %q; want %q, as at %d", width, got, ends, look.Width)
		}

		asked := setupFace(func() {}, setupTill(setup.DataStep)...)
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
	f := setupFace(func() {}, setupTill(setup.PrefsStep)...)
	answered := asking(t, f, "save them", func() error {
		_, err := f.Choose(context.Background(), setup.PrefsStep, "save them every night?", []look.Choice{{Label: "Save them every night"}, {Label: "Not now"}})
		return err
	})
	view := viewed(f, 80, 12)
	if lines := strings.Count(view, "\n") + 1; lines > 12 || !strings.Contains(view, "▲ Apps' settings  save them every night?\n  │ ❯ Save them every night \n  │   Not now") || !strings.HasSuffix(view, "esc stop") || strings.Contains(view, "Homebrew formulae") {
		t.Errorf("12 lines high, drew\n%s\nwant the question whole, its keys, rows done gone first", view)
	}
	f.Key(ask.Key{Name: "down"})
	f.Key(ask.Key{Name: "enter"})
	if err := <-answered; err != nil {
		t.Error(err)
	}
}

// A checklist longer than the screen shows the things around the cursor,
// the question's row above them, its keys under them, however the row and
// the keys wrap: at a phone's 52 columns and at 80, from 10 lines high.
func TestSetupScreenChecklistScrolls(t *testing.T) {
	const question = "which folders should a new Mac bring back from your backups?"
	var ticks []look.Tick
	for name := range strings.FieldsSeq("a b c d e f g h i j k l m n o p q r s t u v w x y") {
		ticks = append(ticks, look.Tick{Name: "~/" + name + "-folder", More: "1.2 GB", Ticked: true})
	}
	f := setupFace(func() {}, setupTill(setup.DataStep)...)
	answered := asking(t, f, "bring back", func() error {
		_, err := f.Tick(context.Background(), setup.DataStep, question, ticks)
		return err
	})
	for range 20 {
		f.Key(ask.Key{Name: "down"})
	}
	for _, width := range []int{52, 80} {
		for height := 10; height <= 31; height++ {
			view := viewed(f, width, height)
			if lines := strings.Count(view, "\n") + 1; lines > height || !strings.Contains(view, "▲ Your data") || !strings.Contains(view, "❯ ■  ~/u-folder") || !strings.HasSuffix(view, "esc stop") {
				t.Errorf("25 things, the cursor on the 21st, %d by %d, drew\n%s\nwant the row, the things around the cursor, the keys", width, height, view)
			}
		}
	}
	f.Key(ask.Key{Name: "enter"})
	if err := <-answered; err != nil {
		t.Error(err)
	}
}

// At a phone's 52 columns, the keys at the foot wrap between keys, each
// with what it does: a checklist's, and the ways to sign in.
func TestSetupScreenKeysWrapWhole(t *testing.T) {
	f := setupFace(func() {}, started(setup.GitHubStep)...)
	foot := func(f *render.SetupFace, what string) {
		t.Helper()
		lines := strings.Split(viewed(f, 52, 40), "\n")
		var keys []string
		for i, l := range slices.Backward(lines) {
			if l == "" {
				keys = lines[i+1:]
				break
			}
		}
		for _, l := range keys {
			for keyed := range strings.SplitSeq(strings.TrimSpace(l), " · ") {
				if len(strings.Fields(keyed)) < 2 {
					t.Errorf("%s at 52 columns, the keys\n%s\nwant each key with what it does", what, strings.Join(keys, "\n"))
				}
			}
		}
	}
	keys := []boot.Key{{Key: "enter", Does: "with your phone"}, {Key: "s", Does: "here, in your browser"}}
	way := make(chan string, 1)
	asking(t, f, "how will you sign in?", func() error {
		k, err := f.Pick(t.Context(), setup.GitHubStep, keys)
		way <- k
		return err
	})
	f.Key(ask.Key{Name: "enter"})
	<-way
	f.Emit(event.DeviceCode{Time: rendertest.At, Step: setup.GitHubStep, URI: "https://github.com/login/device", Code: "WDJB-MJHT", Expires: rendertest.At.Add(15 * time.Minute)})
	asking(t, f, "here, in your browser · esc stop", func() error {
		_, err := f.Pick(t.Context(), setup.GitHubStep, keys)
		return err
	})
	f.Key(ask.Key{Name: "esc"})
	foot(f, "signing in")

	data := setupFace(func() {}, setupTill(setup.DataStep)...)
	answered := asking(t, data, "which folders?", func() error {
		_, err := data.Tick(context.Background(), setup.DataStep, "which folders?", []look.Tick{{Name: "~/Documents"}})
		return err
	})
	foot(data, "a checklist")
	data.Key(ask.Key{Name: "enter"})
	if err := <-answered; err != nil {
		t.Error(err)
	}
}

// q stops setup, as esc does, but while a name's typed, when it's typed;
// space ticks a thing on a checklist, and enter keeps what's ticked.
func TestSetupScreenKeys(t *testing.T) {
	stopped := 0
	f := setupFace(func() { stopped++ }, setupTill(setup.DataStep)...)
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

// Signing in, the ways are offered as choices, each saying what it is;
// the way chosen takes the whole window, the code with it, at any width,
// and at 24 lines the code and when it expires whole; the other way's key
// changes it; esc gives the window back, the code under GitHub's row, the
// ways' keys at the foot.
func TestSetupScreenSignsIn(t *testing.T) {
	f := setupFace(func() {}, rendertest.SetupDone(rendertest.At)[0], event.StepStarted{Time: rendertest.At, Step: setup.GitHubStep})
	keys := []boot.Key{{Key: "enter", Does: "with your phone", About: "scan a QR code with its camera"}, {Key: "s", Does: "here, in your browser", About: "kit opens it"}}
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
	want := "▲ GitHub  not signed in · how will you sign in?\n  │ ❯ With your phone  scan a QR code with its camera\n  │   Here, in your browser  kit opens it"
	if view := viewed(f, 80, 30); !strings.Contains(view, want) {
		t.Errorf("asking how to sign in, drew\n%s\nwant\n%s", view, want)
	}
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
		signsInWithin(t, "setup", f.Full(80, 24))
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
	view := viewed(f, 80, 30)
	if f.Full(80, 30) != nil || !strings.Contains(view, "→ enter WDJB-MJHT at github.com/login/device") || !strings.HasSuffix(view, "enter with your phone · s here, in your browser · esc stop") {
		t.Errorf("esc: the window given back, drew\n%s", view)
	}
}

// The snapshot's parts show as they're found, a row each, the features and
// backup tools by name; the folders being sized show on Your data's row,
// the question they're for. Setup's commit shows on kit-config's row: at
// work, then pushed, or, the push failing, why, and what to do, under it.
func TestSetupScreenFindsAndCommits(t *testing.T) {
	f := setupFace(nil, started(setup.SnapshotStep)...)
	for _, e := range []event.Finding{
		{Step: setup.SnapshotStep, Part: "Homebrew formulae"}, {Step: setup.SnapshotStep, Part: "macOS settings"}, {Step: setup.SnapshotStep, Part: "Features"},
		{Step: setup.SnapshotStep, Part: "Homebrew formulae", Found: true, Count: 12},
		{Step: setup.SnapshotStep, Part: "macOS settings", Doing: "reading what's here"},
		{Step: setup.SnapshotStep, Part: "Features", Found: true, Count: 2, Names: []string{"Touch ID for sudo", "Remote Login"}},
		{Step: setup.SnapshotStep, Part: snapshot.FoldersLabel, Doing: "sizing your folders"},
	} {
		f.Emit(e)
	}
	want := "● Homebrew formulae  12\n  ◐ macOS settings  reading what's here\n  ● Features  Touch ID for sudo · Remote Login\n  ◐ Your data  sizing your folders\n  ○ Apps' settings"
	if view := viewed(f, 80, 40); !strings.Contains(view, want) {
		t.Errorf("finding, drew\n%s\nwant it to hold\n%s", view, want)
	}

	f = setupFace(nil, started(setup.CommitStep)...)
	if view := viewed(f, 80, 40); !strings.Contains(view, "◐ kit-config  committing and pushing") {
		t.Errorf("committing, drew\n%s", view)
	}
	f.Emit(event.StepFinished{Time: rendertest.At, Step: setup.CommitStep, Result: check.Result{State: check.Attention,
		Summary: "370 declared · committed, not pushed: the remote end hung up; kit setup again carries on"}})
	want = "▲ kit-config  370 declared · committed, not pushed\n  │ → the remote end hung up; kit setup again carries on\n"
	if view := viewed(f, 80, 40); !strings.Contains(view, want) {
		t.Errorf("the push failing, drew\n%s\nwant it to hold\n%s", view, want)
	}
}

// Stopped while the snapshot looks, as the engine ends a run stopped: a
// part cut short says it stopped, never that it failed; the snapshot's own
// row says nothing more; each step after it, the engine saying the run was
// stopped, or that it needs one that was, says it stopped; kit-config's row
// stays as it was done, the commit never reached; and nothing's said of the
// apps installed by hand, nor of secrets.
func TestSetupScreenStoppedRows(t *testing.T) {
	stopped := check.Result{State: check.Failed, Reason: event.ErrStopped.Error()}
	f := setupFace(func() {}, started(setup.SnapshotStep)...)
	f.Emit(event.Finding{Step: setup.SnapshotStep, Part: "Homebrew formulae", Found: true, Count: 12})
	f.Emit(event.Finding{Step: setup.SnapshotStep, Part: "macOS settings", Doing: "reading what's here"})
	f.Key(ask.Key{Name: "esc"})
	f.Emit(event.Finding{Step: setup.SnapshotStep, Part: "macOS settings", Found: true, Err: "sw_vers: context canceled"})
	for _, step := range []string{setup.SnapshotStep, setup.DataStep, setup.PrefsStep, setup.ByHandStep, setup.SecretsStep} {
		f.Emit(event.StepFinished{Time: rendertest.At, Step: step, Result: stopped})
	}
	for _, step := range []string{setup.FilesStep, setup.InstallStep, setup.CommitStep} {
		f.Emit(event.StepFinished{Time: rendertest.At, Step: step, Result: check.Result{State: check.Deferred, Reason: "needs Declared"}})
	}
	left := ansi.Strip(strings.Join(f.Leaves(80), "\n"))
	want := "● kit-config  someone/kit-config · new, private\n"
	for _, row := range []string{want, "– macOS settings  stopped", "– Your data  stopped", "– Apps' settings  stopped", "– Linked files  stopped"} {
		if !strings.Contains(left, row) {
			t.Errorf("stopped, left\n%s\nwant %q", left, row)
		}
	}
	for _, never := range []string{"✗", "the run was stopped", "skipped", "Declared", "To install", "INSTALLED BY HAND", "→"} {
		if strings.Contains(left, never) {
			t.Errorf("stopped, left\n%s\nwant no %q", left, never)
		}
	}
}

// What kit-config declares that this Mac hasn't has a row only once it's
// asked, or said; a kit-config setup started has nothing to install, and
// no row. Asked, the question's summary is beside its name, the things
// under it, muted, then its answers, a command dim after a dot.
func TestSetupScreenToInstall(t *testing.T) {
	f := setupFace(func() {}, started(setup.InstallStep)...)
	if view := viewed(f, 80, 60); strings.Contains(view, "To install") {
		t.Errorf("before it's asked, drew\n%s\nwant no row to install", view)
	}
	answered := asking(t, f, "To install", func() error {
		_, err := f.Choose(context.Background(), setup.InstallStep, "kit-config declares 3 things this Mac hasn't\nfd, lazygit and ripgrep",
			[]look.Choice{{Label: "Install them with the rest"}, {Label: "Stop here to look first", Cmd: "kit status"}})
		return err
	})
	want := "▲ To install  kit-config declares 3 things this Mac hasn't\n  │ fd, lazygit and ripgrep\n  │ ❯ Install them with the rest \n  │   Stop here to look first · kit status"
	if view := viewed(f, 80, 60); !strings.Contains(view, want) {
		t.Errorf("asked, drew\n%s\nwant\n%s", view, want)
	}
	f.Key(ask.Key{Name: "enter"})
	if err := <-answered; err != nil {
		t.Fatal(err)
	}
	f.Emit(event.StepFinished{Time: rendertest.At, Step: setup.InstallStep, Result: check.Result{State: check.OK, Summary: "with the rest"}})
	if view := viewed(f, 80, 60); !strings.Contains(view, "● To install  with the rest") {
		t.Errorf("answered, drew\n%s", view)
	}

	f = setupFace(nil, setupTill(setup.InstallStep)...)
	f.Emit(event.StepFinished{Time: rendertest.At, Step: setup.InstallStep, Result: check.Result{State: check.OK, Summary: setup.NothingNew}})
	if view := viewed(f, 80, 60); strings.Contains(view, "To install") {
		t.Errorf("kit-config new, drew\n%s\nwant no row to install", view)
	}
	f.Emit(event.StepFinished{Time: rendertest.At, Step: setup.InstallStep, Result: check.Result{State: check.OK, Summary: "nothing more"}})
	if view := viewed(f, 80, 60); !strings.Contains(view, "● To install  nothing more") {
		t.Errorf("kit-config joined, nothing to install, drew\n%s", view)
	}
}
