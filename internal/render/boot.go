package render

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
)

// BootFace is the boot's face at a terminal, before the hand-off (kit-look,
// rule 22): the self-test played out, then the boot's areas in turn, drawn
// in place under it, each shown once the one before is done (Lee, 9 Oct:
// "reveal what needs to be revealed at the point that it's needed"). An
// area before the last says what it's for till its step is done, as
// signing in does; the last is the boot order, a row a step. What a step
// asks is asked in its row, and signing in takes the whole window, the way
// chosen. Amber, as the raw machine.
type BootFace struct {
	t    ask.Terminal
	look look.Boot
	now  func() time.Time
	// stop stops the boot, as esc does.
	stop func()

	mu    sync.Mutex
	steps []event.Step
	rows  map[string]*bootRow
	q     *question
	// working is whether the bar shows: once a step has gone a while
	// without asking anything.
	working  bool
	began    time.Time
	stopping bool
	note     string
	// mac and when are the self-test's, for the head over a full screen.
	mac  string
	when time.Time
	// mode is the way of signing in that has the whole window: enter, with
	// the phone; s, here in the browser; "" while it has none.
	mode  string
	frame int
	// signing is the step a way of signing in was chosen for.
	signing string

	changed chan struct{}
	closed  bool
	live    chan error
	quit    chan struct{}
}

// bootRow is how a step of the boot stands, as its row shows it: todo is
// what the person's to do meanwhile; took is how long it took, and asked
// whether it asked anything, so waited on someone.
type bootRow struct {
	running  bool
	began    time.Time
	doing    string
	todo     string
	output   []string
	code     *event.DeviceCode
	finished *check.Result
	took     time.Duration
	asked    bool
}

// questionKind is what a question takes: a choice, typed words, keys, a
// password, or a key once something's done.
type questionKind int

const (
	choosing questionKind = iota
	naming
	picking
	secret
	waiting
)

// question is what the face is asking, in a step's row.
type question struct {
	step string
	kind questionKind
	text string
	// todo is what to do before a key, for a question that waits.
	todo    string
	choices []look.Choice
	cursor  int
	typed   []rune
	keys    []look.Key
	answer  chan answer
}

type answer struct {
	chosen int
	text   string
	err    error
}

// keptOutput is how many of a tool's last lines show under its row.
const keptOutput = 5

// NewBootFace is the boot's face at t: dark is whether its background is,
// and stop stops the boot when esc is pressed.
func NewBootFace(t ask.Terminal, dark bool, now func() time.Time, stop func()) *BootFace {
	return &BootFace{t: t, look: look.NewBoot(dark), now: now, stop: stop, rows: map[string]*bootRow{}, changed: make(chan struct{}, 1)}
}

// signal has the screen drawn again.
func (f *BootFace) signal() {
	if f.closed {
		return
	}
	select {
	case f.changed <- struct{}{}:
	default:
	}
}

// PlaySelfTest writes the head, then plays the self-test out, a line at a
// time, each working for a moment before it says what it found: the checks
// are done by then, quicker than the eye.
func (f *BootFace) PlaySelfTest(ctx context.Context, e event.SelfTest) error {
	f.mu.Lock()
	f.mac, f.when = e.Mac, e.Time
	f.mu.Unlock()
	b := f.look
	var head strings.Builder
	for _, l := range append(b.Head(b.Meta("bootstrap", e.Mac, when(e.Time))...), "") {
		head.WriteString(l + "\r\n")
	}
	_, _ = fmt.Fprint(f.t.Out, head.String())
	s := &selfTestScreen{look: b, test: e, changed: make(chan struct{}, 1)}
	go s.tick()
	return ask.Live(ctx, f.t, s)
}

// selfTestScreen plays the self-test out, a line every few frames.
type selfTestScreen struct {
	look    look.Boot
	test    event.SelfTest
	mu      sync.Mutex
	frame   int
	changed chan struct{}
}

// selfTestFrames is how many frames each line works for.
const selfTestFrames = 4

func (s *selfTestScreen) tick() {
	t := time.NewTicker(80 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		s.mu.Lock()
		s.frame++
		over := s.frame/selfTestFrames > len(s.test.Tests)
		s.mu.Unlock()
		if over {
			close(s.changed)
			return
		}
		select {
		case s.changed <- struct{}{}:
		default:
		}
	}
}

func (s *selfTestScreen) View(width, _ int) []string {
	s.mu.Lock()
	frame := s.frame
	s.mu.Unlock()
	return s.lines(min(width, look.Width), frame/selfTestFrames, frame)
}

// lines are the self-test with done lines finished, the one at done working.
func (s *selfTestScreen) lines(width, done, frame int) []string {
	b := s.look.At(frame)
	out := []string{b.Header("Power-on self-test")}
	for i, t := range s.test.Tests {
		if i > done {
			break
		}
		row := look.Row{State: look.Running, Name: t.Name}
		if i < done {
			parts := make([]string, len(t.Says))
			for j, said := range t.Says {
				parts[j] = b.Mid(said)
			}
			row.State, row.Says = look.Done, b.Says(parts...)
			if t.State == check.Failed {
				row.State = look.Failed
			}
		}
		out = append(out, b.Rows(width, row)...)
	}
	return out
}

func (s *selfTestScreen) Key(ask.Key) bool { return false }

func (s *selfTestScreen) Leaves(width int) []string {
	return append(s.lines(min(width, look.Width), len(s.test.Tests), 0), "")
}

func (s *selfTestScreen) Changes() <-chan struct{} { return s.changed }

// Show sets the boot's steps, as they're drawn.
func (f *BootFace) Show(steps []event.Step) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = steps
	f.signal()
}

// Start shows the boot's steps under the self-test, and keeps them drawn in
// place till Close.
func (f *BootFace) Start(steps []event.Step) {
	f.Show(steps)
	f.live, f.quit = make(chan error, 1), make(chan struct{})
	go func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-f.quit:
				return
			case <-tick.C:
				f.mu.Lock()
				f.frame++
				f.notice()
				f.signal()
				f.mu.Unlock()
			}
		}
	}()
	go func() { f.live <- ask.Live(context.Background(), f.t, f) }()
}

// notice notes the boot at work without asking: a step running a while
// with no question up shows the bar from then on.
func (f *BootFace) notice() {
	if f.working || f.q != nil {
		return
	}
	for _, r := range f.rows {
		if r.running && r.code == nil && f.now().Sub(r.began) >= 3*time.Second {
			f.working = true
		}
	}
}

func (f *BootFace) Emit(e event.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch e := e.(type) {
	case event.SelfTest:
		f.mac, f.when = e.Mac, e.Time
		return
	case event.StepStarted:
		r := f.row(e.Step)
		if !r.running {
			r.running, r.began = true, e.Time
			if f.began.IsZero() {
				f.began = e.Time
			}
		}
	case event.Doing:
		r := f.row(e.Step)
		r.doing, r.todo = e.Says, e.Todo
	case event.DeviceCode:
		code := e
		r := f.row(e.Step)
		r.code, r.asked = &code, true
	case event.Output:
		r := f.row(e.Step)
		r.output = lastOf(append(r.output, e.Line), keptOutput)
	case event.StepFinished:
		r := f.row(e.Step)
		result := e.Result
		if e.Step == f.signing {
			f.mode, f.signing = "", ""
		}
		r.running, r.code, r.finished, r.took = false, nil, &result, e.Duration
	default:
		return
	}
	f.signal()
}

// Running is the step at work, for a question that comes from what it
// runs: "" when there's none.
func (f *BootFace) Running() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.steps {
		if r := f.rows[s.Name]; r != nil && r.running {
			return s.Name
		}
	}
	return ""
}

func (f *BootFace) row(step string) *bootRow {
	r, ok := f.rows[step]
	if !ok {
		r = &bootRow{}
		f.rows[step] = r
	}
	return r
}

// Finish notes the boot's last word, under the bar, as the screen's left.
func (f *BootFace) Finish(note string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.note = note
	f.signal()
}

// Close leaves the boot where it was drawn, as it ended.
func (f *BootFace) Close() error {
	f.mu.Lock()
	if f.live == nil || f.closed {
		f.mu.Unlock()
		return nil
	}
	close(f.quit)
	f.closed = true
	close(f.changed)
	f.mu.Unlock()
	return <-f.live
}

// area is a run of the boot's steps in one area, as it's drawn.
type area struct {
	name  string
	steps []event.Step
	// order is whether it's the boot order: the last area, a row a step.
	order bool
}

// areas are the boot's areas, in order, as far as they're shown: an area
// after one not yet done stays hidden.
func (f *BootFace) areas() []area {
	var out []area
	for _, s := range f.steps {
		if len(out) == 0 || out[len(out)-1].name != s.Area {
			out = append(out, area{name: s.Area})
		}
		out[len(out)-1].steps = append(out[len(out)-1].steps, s)
	}
	if len(out) > 0 {
		out[len(out)-1].order = true
	}
	for i, a := range out {
		for _, s := range a.steps {
			if r := f.rows[s.Name]; r == nil || r.finished == nil || r.finished.State != check.OK {
				return out[:i+1]
			}
		}
	}
	return out
}

// View is the boot as it stands, never taller than height: in the boot
// order, rows done, then those waiting, give way first, so what's at work
// stays whole.
func (f *BootFace) View(width, height int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	width = min(width, look.Width)
	blocks := f.blocks(width, true)
	foot := f.foot(width, true)
	size := func() int {
		n := len(foot)
		for _, b := range blocks {
			n += len(b.lines)
		}
		return n
	}
	for size() > height {
		drop := -1
		for i, b := range blocks {
			if b.state == look.Done && !b.heading {
				drop = i
				break
			}
		}
		if drop < 0 {
			for i, b := range slices.Backward(blocks) {
				if b.state == look.Queued {
					drop = i
					break
				}
			}
		}
		if drop < 0 {
			for i, b := range blocks {
				if b.heading {
					drop = i
					break
				}
			}
		}
		if drop < 0 {
			break
		}
		blocks = append(blocks[:drop], blocks[drop+1:]...)
	}
	var out []string
	for _, b := range blocks {
		out = append(out, b.lines...)
	}
	if len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	out = append(out, foot...)
	if len(out) > height {
		out = out[len(out)-height:]
	}
	return out
}

// Leaves is the boot as it ended, to stay in the scrollback.
func (f *BootFace) Leaves(width int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	width = min(width, look.Width)
	var out []string
	for _, b := range f.blocks(width, false) {
		out = append(out, b.lines...)
	}
	return append(out, f.foot(width, false)...)
}

func (f *BootFace) Changes() <-chan struct{} { return f.changed }

// block is a step's lines, or a heading's, and how it stands.
type block struct {
	state   look.State
	lines   []string
	heading bool
}

// blocks are the areas shown, a blank line between: each its heading, then,
// for the boot order, a row a step; for an area before it, what it's for
// till its step is done, then the step's row.
func (f *BootFace) blocks(width int, live bool) []block {
	b := f.look.At(f.frame)
	var out []block
	for i, a := range f.areas() {
		heading := []string{b.Header(cmp.Or(a.name, "Boot order"))}
		if i > 0 {
			heading = append([]string{""}, heading...)
		}
		r := f.rows[a.steps[0].Name]
		switch {
		case !a.order && len(a.steps) == 1 && (r == nil || r.finished == nil):
			lines := heading
			for _, l := range wrap(a.steps[0].Waiting, width-4) {
				lines = append(lines, "  "+b.Full(l))
			}
			if r != nil && r.running && r.code != nil {
				lines = append(lines, "", "  "+b.Mid(cmp.Or(r.doing, "waiting")))
			}
			out = append(out, block{state: look.NeedsYou, lines: lines})
		case !a.order:
			lines := heading
			for _, s := range a.steps {
				lines = append(lines, b.Rows(width, f.stepRow(s, b, live))...)
			}
			out = append(out, block{state: look.Done, lines: lines})
		default:
			out = append(out, block{state: look.Done, lines: heading, heading: true})
			for _, s := range a.steps {
				row := f.stepRow(s, b, live)
				out = append(out, block{state: row.State, lines: b.Rows(width, row)})
			}
		}
	}
	return out
}

// wrap cuts text into lines no wider than width, at spaces.
func wrap(text string, width int) []string {
	var out []string
	line := ""
	for word := range strings.FieldsSeq(text) {
		switch {
		case line == "":
			line = word
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > width:
			out = append(out, line)
			line = word
		default:
			line += " " + word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// stepRow is a step's row as it stands: waiting, at work, asking, or done.
func (f *BootFace) stepRow(s event.Step, b look.Boot, live bool) look.Row {
	r := f.rows[s.Name]
	switch {
	case f.q != nil && f.q.step == s.Name && f.q.kind != picking:
		return look.Row{State: look.NeedsYou, Name: s.Title, Says: b.Words(look.NeedsYou, f.q.text), Under: f.asked()}
	case r != nil && r.finished != nil:
		row := f.finishedRow(s.Title, *r.finished)
		// How long a step took shows when it was work, not waiting on you.
		if row.State == look.Done && r.took >= 10*time.Second && !r.asked {
			row.Says = b.Says(row.Says, b.Mid(clock(r.took)))
		}
		return row
	case r != nil && r.running:
		says := []string{b.Words(look.Running, cmp.Or(r.doing, "working"))}
		if took := f.now().Sub(r.began); took >= 10*time.Second {
			says = append(says, b.Mid(clock(took)))
		}
		row := look.Row{State: look.Running, Name: s.Title, Says: b.Says(says...)}
		if live {
			if r.todo != "" {
				row.Under = f.todo(r.todo)
			}
			row.Under = append(row.Under, b.Output(r.output...)...)
		}
		return row
	}
	row := look.Row{State: look.Queued, Name: s.Title}
	if s.Waiting != "" {
		row.Says = b.Mid(s.Waiting)
	}
	return row
}

// finishedRow is a step that's ended: done, saying what it did; failed,
// saying what went wrong and, under it, what to do; skipped.
func (f *BootFace) finishedRow(title string, res check.Result) look.Row {
	b := f.look
	switch res.State {
	case check.OK:
		return look.Row{State: look.Done, Name: title, Says: f.parts(res.Summary)}
	case check.Deferred:
		return look.Row{State: look.Skipped, Name: title, Says: b.Mid("skipped")}
	}
	text := cmp.Or(res.Reason, res.Summary)
	if f.stopping && strings.Contains(text, context.Canceled.Error()) {
		return look.Row{State: look.Skipped, Name: title, Says: b.Mid("stopped")}
	}
	what, todo, _ := strings.Cut(text, ": ")
	row := look.Row{State: look.Failed, Name: title, Says: b.Words(look.Failed, what)}
	if res.State == check.Attention {
		row.State, row.Says = look.NeedsYou, b.Words(look.NeedsYou, what)
	}
	if todo != "" {
		row.Under = []string{b.Todo(todo)}
	}
	return row
}

// parts is a summary, its parts after dim dots.
func (f *BootFace) parts(summary string) string {
	ps := strings.Split(summary, " · ")
	for i, p := range ps {
		ps[i] = f.look.Mid(p)
	}
	return f.look.Says(ps...)
}

// site is an address without its scheme, as it's typed.
func site(uri string) string {
	return strings.TrimPrefix(strings.TrimPrefix(uri, "https://"), "http://")
}

// left says how long a code has: 14:21.
func left(d time.Duration) string {
	d = max(d, 0)
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// signingIn is the step being signed in, and its code once it has one: the
// step with a code showing, or whose question is how to sign in.
func (f *BootFace) signingIn() (step string, code *event.DeviceCode) {
	for _, s := range f.steps {
		if r := f.rows[s.Name]; r != nil && r.running && r.code != nil {
			return s.Name, r.code
		}
	}
	if f.signing != "" {
		return f.signing, nil
	}
	return "", nil
}

// Full is signing in, the whole window, the way chosen: with the phone, the
// QR code to scan and the code to enter; here, the code to enter in the
// browser kit opened. nil the rest of the time.
func (f *BootFace) Full(width, height int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	step, c := f.signingIn()
	if f.mode == "" || step == "" {
		return nil
	}
	b := f.look
	centre := func(l string) string { return strings.Repeat(" ", max((width-ansi.StringWidth(l))/2, 0)) + l }
	out := append([]string{""}, b.Head(b.Meta("bootstrap", f.mac, when(f.when))...)...)
	out = append(out, "")
	var keys []look.Key
	switch {
	case c == nil:
		for len(out) < height/2 {
			out = append(out, "")
		}
		out = append(out, centre(b.Mid("getting a code from GitHub")))
	case f.mode == "enter":
		if code, err := b.QR(c.URI); err == nil {
			for _, l := range code {
				out = append(out, centre(l))
			}
		}
		out = append(out, "",
			centre(b.Full("Scan it with your phone's camera, then enter this code:")),
			"",
			centre(b.Rev(" "+c.Code+" ")),
			"",
			centre(b.Mid("expires in "+left(c.Expires.Sub(f.now())))),
		)
		keys = []look.Key{{Key: "s", Does: "here, in Safari, instead"}}
	default:
		for len(out) < height/2-4 {
			out = append(out, "")
		}
		out = append(out,
			centre(b.Full("Safari is open at ")+b.Strong(site(c.URI))+b.Full(".")),
			centre(b.Full("Enter this code there:")),
			"",
			centre(b.Rev(" "+c.Code+" ")),
			"",
			centre(b.Mid("expires in "+left(c.Expires.Sub(f.now())))),
		)
		keys = []look.Key{{Key: "enter", Does: "with your phone instead"}}
	}
	keys = append(keys, look.Key{Key: "esc", Does: "back"})
	for len(out) < height-1 {
		out = append(out, "")
	}
	return append(out[:min(len(out), height-1)], b.Keys(keys...))
}

// asked is what's under the row of the question being asked.
func (f *BootFace) asked() []string {
	b := f.look
	switch f.q.kind {
	case choosing:
		return b.Answers(f.q.choices, f.q.cursor)
	case naming:
		return []string{b.Full("❯") + " " + b.Full(string(f.q.typed)) + b.Rev(" ")}
	case secret:
		return []string{b.Field(len(f.q.typed))}
	case waiting:
		return f.todo(f.q.todo)
	}
	return nil
}

// todo is what the person's to do, under a row: after an arrow, cut at
// spaces to fit beside it.
func (f *BootFace) todo(text string) []string {
	b := f.look
	lines := wrap(text, look.Width-len(look.BootUnder)-4)
	out := make([]string, len(lines))
	for i, l := range lines {
		if i == 0 {
			out[i] = b.Todo(l)
			continue
		}
		out[i] = "  " + b.Full(l)
	}
	return out
}

// foot is what's under the boot: the bar once it's at work, counting the
// boot order's steps; its last word; the keys while it's live.
func (f *BootFace) foot(width int, live bool) []string {
	b := f.look
	var out []string
	if f.working && len(f.steps) > 0 {
		last := f.steps[len(f.steps)-1].Area
		total, done := 0, 0
		for _, s := range f.steps {
			if s.Area != last {
				continue
			}
			total++
			if r := f.rows[s.Name]; r != nil && r.finished != nil && r.finished.State == check.OK {
				done++
			}
		}
		line := "  " + b.Bar(done, total) + "  " + b.Says(b.Full(fmt.Sprintf("%d of %d", done, total)), b.Mid(clock(f.now().Sub(f.began))))
		out = append(out, "", look.Cut(line, width))
	}
	if f.note != "" {
		out = append(out, "", "  "+b.Todo(f.note))
	}
	if !live || f.stopping {
		return out
	}
	keys := []look.Key{{Key: "esc", Does: "stop"}}
	if f.q != nil {
		switch f.q.kind {
		case choosing:
			keys = []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "enter", Does: "decide"}, keys[0]}
		case naming, secret:
			keys = []look.Key{{Key: "enter", Does: "done"}, keys[0]}
		case picking, waiting:
			keys = append(slices.Clone(f.q.keys), keys[0])
		}
	}
	return append(out, "", b.Keys(keys...))
}

// Key takes a key: esc goes back from signing in, or stops the boot; the
// rest answer what's asked.
func (f *BootFace) Key(k ask.Key) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.q
	if k.Is("esc", "ctrl+c") {
		if f.mode != "" && k.Is("esc") {
			f.mode = ""
			return false
		}
		if !f.stopping {
			f.stopping, f.mode = true, ""
			if q != nil {
				f.answered(answer{err: ask.ErrCancelled})
			}
			if f.stop != nil {
				f.stop()
			}
		}
		return false
	}
	if q == nil {
		return false
	}
	switch q.kind {
	case choosing:
		switch {
		case k.Is("up", "k"):
			q.cursor = (q.cursor + len(q.choices) - 1) % len(q.choices)
		case k.Is("down", "j"):
			q.cursor = (q.cursor + 1) % len(q.choices)
		case k.Is("enter"):
			f.answered(answer{chosen: q.cursor})
		}
	case naming:
		switch {
		case k.Is("enter"):
			if len(q.typed) > 0 {
				f.answered(answer{text: string(q.typed)})
			}
		case k.Is("backspace"):
			if len(q.typed) > 0 {
				q.typed = q.typed[:len(q.typed)-1]
			}
		case k.Name == "" && !k.Paste && utf8.RuneCountInString(k.Text) == 1:
			q.typed = append(q.typed, []rune(k.Text)...)
		}
	case secret:
		switch {
		case k.Is("enter"):
			f.answered(answer{text: string(q.typed)})
		case k.Is("backspace"):
			if len(q.typed) > 0 {
				q.typed = q.typed[:len(q.typed)-1]
			}
		case k.Name == "":
			q.typed = append(q.typed, []rune(strings.TrimRight(k.Text, "\r\n"))...)
		}
	case picking, waiting:
		for _, key := range q.keys {
			if k.Is(key.Key) {
				if q.kind == picking {
					f.mode, f.signing = key.Key, q.step
				}
				f.answered(answer{text: key.Key})
				break
			}
		}
	}
	return false
}

// answered answers the question being asked, and takes it down.
func (f *BootFace) answered(a answer) {
	f.q.answer <- a
	f.q = nil
}

// ask asks q, in its step's row, till it's answered or ctx is done.
func (f *BootFace) ask(ctx context.Context, q *question) (answer, error) {
	q.answer = make(chan answer, 1)
	f.mu.Lock()
	if f.stopping {
		f.mu.Unlock()
		return answer{}, ask.ErrCancelled
	}
	f.q = q
	f.row(q.step).asked = true
	f.signal()
	f.mu.Unlock()
	select {
	case a := <-q.answer:
		f.mu.Lock()
		f.signal()
		f.mu.Unlock()
		return a, a.err
	case <-ctx.Done():
		f.mu.Lock()
		if f.q == q {
			f.q = nil
		}
		f.signal()
		f.mu.Unlock()
		return answer{}, ctx.Err()
	}
}

// Choose asks step's question, its choices under it: the place of the one
// taken.
func (f *BootFace) Choose(ctx context.Context, step, text string, choices []look.Choice) (int, error) {
	a, err := f.ask(ctx, &question{step: step, kind: choosing, text: text, choices: choices})
	return a.chosen, err
}

// Name asks for a name, typed in step's row.
func (f *BootFace) Name(ctx context.Context, step, text string) (string, error) {
	a, err := f.ask(ctx, &question{step: step, kind: naming, text: text})
	return a.text, err
}

// Pick waits for one of keys, the keys at the foot saying what each does:
// the key pressed.
func (f *BootFace) Pick(ctx context.Context, step string, keys []look.Key) (string, error) {
	a, err := f.ask(ctx, &question{step: step, kind: picking, keys: keys})
	return a.text, err
}

// Secret asks for a password in step's row, typed without being shown, a
// dot a character.
func (f *BootFace) Secret(ctx context.Context, step, text string) (string, error) {
	a, err := f.ask(ctx, &question{step: step, kind: secret, text: text})
	return a.text, err
}

// Wait shows what the person's to do in step's row, says beside its name
// and todo under it, till they press one of keys: the key pressed.
func (f *BootFace) Wait(ctx context.Context, step, says, todo string, keys []look.Key) (string, error) {
	a, err := f.ask(ctx, &question{step: step, kind: waiting, text: says, todo: todo, keys: keys})
	return a.text, err
}

// clock says how long d is: 41s, 4m31s.
func clock(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
