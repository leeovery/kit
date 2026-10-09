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
// rule 22): the self-test written once, then the boot order drawn in place
// under it, filling in as its steps go, and what a step asks asked in its
// row. Amber, as the raw machine.
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
	// mac and when are the self-test's, for the head over the QR code.
	mac  string
	when time.Time
	// qr is whether GitHub's QR code has the whole screen.
	qr bool

	changed chan struct{}
	closed  bool
	live    chan error
	quit    chan struct{}
}

// bootRow is how a step of the boot stands, as its row shows it.
type bootRow struct {
	running  bool
	began    time.Time
	doing    string
	output   []string
	code     *event.DeviceCode
	finished *check.Result
}

// questionKind is what a question takes: a choice, typed words, or a key.
type questionKind int

const (
	choosing questionKind = iota
	naming
	pressing
)

// question is what the face is asking, in a step's row.
type question struct {
	step    string
	kind    questionKind
	text    string
	choices []look.Choice
	cursor  int
	typed   []rune
	key     string
	does    string
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

func (f *BootFace) write(lines []string) {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l + "\r\n")
	}
	_, _ = fmt.Fprint(f.t.Out, b.String())
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

// Show sets the boot order, steps, as it's drawn.
func (f *BootFace) Show(steps []event.Step) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = steps
	f.signal()
}

// Start shows the boot order, steps, under the self-test, and keeps it
// drawn in place till Close.
func (f *BootFace) Start(steps []event.Step) {
	f.Show(steps)
	f.live, f.quit = make(chan error, 1), make(chan struct{})
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-f.quit:
				return
			case <-tick.C:
				f.mu.Lock()
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
		f.write(f.selfTest(e))
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
		f.row(e.Step).doing = e.Says
	case event.DeviceCode:
		code := e
		f.row(e.Step).code = &code
	case event.Output:
		r := f.row(e.Step)
		r.output = lastOf(append(r.output, e.Line), keptOutput)
	case event.StepFinished:
		r := f.row(e.Step)
		result := e.Result
		if r.code != nil {
			f.qr = false
		}
		r.running, r.code, r.finished = false, nil, &result
	default:
		return
	}
	f.signal()
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

// Close leaves the boot order where it was drawn, as it ended.
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

// selfTest is the self-test as it's written: the wordmark with the boot,
// the Mac and when beside it, then a line each thing it found.
func (f *BootFace) selfTest(e event.SelfTest) []string {
	b := f.look
	out := append(b.Head(b.Meta("bootstrap", e.Mac, when(e.Time))...), "", b.Header("Power-on self-test"))
	rows := make([]look.Row, len(e.Tests))
	for i, t := range e.Tests {
		parts := make([]string, len(t.Says))
		for j, s := range t.Says {
			parts[j] = b.Mid(s)
		}
		s := look.Done
		switch t.State {
		case check.Attention:
			s = look.Skipped
		case check.Failed:
			s = look.Failed
		}
		rows[i] = look.Row{State: s, Name: t.Name, Says: b.Says(parts...)}
	}
	return append(append(out, b.Rows(look.Width, rows...)...), "")
}

// View is the boot order as it stands, never taller than height: rows
// done, then those waiting, give way first, so what's at work stays whole.
func (f *BootFace) View(width, height int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	width = min(width, look.Width)
	blocks := f.blocks(width, true)
	foot := f.foot(width, true)
	head := []string{f.look.Header("Boot order")}
	size := func() int {
		n := len(head) + len(foot)
		for _, b := range blocks {
			n += len(b.lines)
		}
		return n
	}
	for size() > height {
		drop := -1
		for i, b := range blocks {
			if b.state == look.Done {
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
			if len(head) > 0 {
				head = nil
				continue
			}
			break
		}
		blocks = append(blocks[:drop], blocks[drop+1:]...)
	}
	out := head
	for _, b := range blocks {
		out = append(out, b.lines...)
	}
	out = append(out, foot...)
	if len(out) > height {
		out = out[len(out)-height:]
	}
	return out
}

// Leaves is the boot order as it ended, to stay in the scrollback.
func (f *BootFace) Leaves(width int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	width = min(width, look.Width)
	out := []string{f.look.Header("Boot order")}
	for _, b := range f.blocks(width, false) {
		out = append(out, b.lines...)
	}
	return append(out, f.foot(width, false)...)
}

func (f *BootFace) Changes() <-chan struct{} { return f.changed }

// block is a step's lines, and how it stands.
type block struct {
	state look.State
	lines []string
}

// blocks are the steps' rows, each with what belongs under it while live.
func (f *BootFace) blocks(width int, live bool) []block {
	out := make([]block, 0, len(f.steps))
	for _, s := range f.steps {
		row := f.stepRow(s, width, live)
		out = append(out, block{state: row.State, lines: f.look.Rows(width, row)})
	}
	return out
}

// stepRow is a step's row as it stands: waiting, at work, asking, or done.
func (f *BootFace) stepRow(s event.Step, width int, live bool) look.Row {
	b := f.look
	title := s.Title
	r := f.rows[s.Name]
	switch {
	case f.q != nil && f.q.step == s.Name && f.q.kind != pressing:
		return look.Row{State: look.NeedsYou, Name: title, Says: b.Words(look.NeedsYou, f.q.text), Under: f.asked()}
	case r != nil && r.finished != nil:
		return f.finishedRow(title, *r.finished, live)
	case r != nil && r.running:
		says := []string{b.Words(look.Running, cmp.Or(r.doing, "working"))}
		if took := f.now().Sub(r.began); took >= 10*time.Second && r.code == nil {
			says = append(says, b.Mid(clock(took)))
		}
		row := look.Row{State: look.Running, Name: title, Says: b.Says(says...)}
		if live {
			switch {
			case r.code != nil:
				row.Under = []string{f.codeLine(*r.code)}
			default:
				row.Under = b.Output(r.output...)
			}
		}
		return row
	}
	row := look.Row{State: look.Queued, Name: title}
	if s.Waiting != "" {
		row.Says = b.Mid(s.Waiting)
	}
	return row
}

// finishedRow is a step that's ended: done, saying what it did; failed,
// saying what went wrong and, under it, what to do; skipped.
func (f *BootFace) finishedRow(title string, res check.Result, live bool) look.Row {
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

// codeLine is GitHub's code under its row: the code, where to enter it,
// and how long it has.
func (f *BootFace) codeLine(c event.DeviceCode) string {
	b := f.look
	return b.Mid("enter ") + b.Rev(" "+c.Code+" ") + b.Mid(" at ") + b.Full(site(c.URI)) + b.Dim(" · ") + b.Mid(left(c.Expires.Sub(f.now()))+" left")
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

// code is the code showing, if one is.
func (f *BootFace) code() *event.DeviceCode {
	for _, s := range f.steps {
		if r := f.rows[s.Name]; r != nil && r.code != nil {
			return r.code
		}
	}
	return nil
}

// Full is GitHub's QR code, the whole screen, while it's asked for: the
// boot's head over it, the code centred, where to enter it, how long it
// has; nil the rest of the time.
func (f *BootFace) Full(width, height int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.code()
	if !f.qr || c == nil {
		return nil
	}
	b := f.look
	centre := func(l string) string { return strings.Repeat(" ", max((width-ansi.StringWidth(l))/2, 0)) + l }
	out := append([]string{""}, b.Head(b.Meta("bootstrap", f.mac, when(f.when))...)...)
	out = append(out, "")
	code, err := b.QR(c.URI)
	if err == nil {
		for _, l := range code {
			out = append(out, centre(l))
		}
	}
	out = append(out, "",
		centre(b.Full("Scan it with your phone's camera, or open ")+b.Strong(site(c.URI))),
		centre(b.Full("and enter  ")+b.Rev(" "+c.Code+" ")),
		centre(b.Mid("expires in "+left(c.Expires.Sub(f.now())))),
	)
	keys := []look.Key{{Key: "enter", Does: "back"}}
	if f.q != nil && f.q.kind == pressing {
		keys = append(keys, look.Key{Key: f.q.key, Does: f.q.does})
	}
	keys = append(keys, look.Key{Key: "esc", Does: "stop"})
	for len(out) < height-1 {
		out = append(out, "")
	}
	out = append(out[:min(len(out), height-1)], b.Keys(keys...))
	return out
}

// asked is what's under the row of the question being asked.
func (f *BootFace) asked() []string {
	b := f.look
	switch f.q.kind {
	case choosing:
		return b.Answers(f.q.choices, f.q.cursor)
	case naming:
		return []string{b.Full("❯") + " " + b.Full(string(f.q.typed)) + b.Rev(" ")}
	}
	return nil
}

// foot is what's under the rows: the bar once the boot's at work, the
// boot's last word, the keys while it's live.
func (f *BootFace) foot(width int, live bool) []string {
	b := f.look
	var out []string
	if f.working {
		done := 0
		for _, r := range f.rows {
			if r.finished != nil && r.finished.State == check.OK {
				done++
			}
		}
		line := "  " + b.Bar(done, len(f.steps)) + "  " + b.Says(b.Full(fmt.Sprintf("%d of %d", done, len(f.steps))), b.Mid(clock(f.now().Sub(f.began))))
		out = append(out, "", look.Cut(line, width))
	}
	if f.note != "" {
		out = append(out, "", "  "+b.Todo(f.note))
	}
	if !live {
		return out
	}
	keys := []look.Key{{Key: "esc", Does: "stop"}}
	if f.code() != nil {
		keys = append([]look.Key{{Key: "enter", Does: "QR code"}}, keys...)
	}
	if f.q != nil {
		switch f.q.kind {
		case choosing:
			keys = []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "enter", Does: "decide"}, keys[len(keys)-1]}
		case naming:
			keys = []look.Key{{Key: "enter", Does: "done"}, keys[len(keys)-1]}
		case pressing:
			keys = append(keys[:len(keys)-1], look.Key{Key: f.q.key, Does: f.q.does}, keys[len(keys)-1])
		}
	}
	if f.stopping {
		keys = nil
	}
	if keys != nil {
		out = append(out, "", b.Keys(keys...))
	}
	return out
}

// Key takes a key: esc stops the boot; the rest answer what's asked.
func (f *BootFace) Key(k ask.Key) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.q
	if k.Is("esc", "ctrl+c") {
		if !f.stopping {
			f.stopping = true
			if q != nil {
				f.answered(answer{err: ask.ErrCancelled})
			}
			if f.stop != nil {
				f.stop()
			}
		}
		return false
	}
	if f.code() != nil && k.Is("enter") && (q == nil || q.kind == pressing && q.key != "enter") {
		f.qr = !f.qr
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
	case pressing:
		if k.Is(q.key) {
			f.qr = false
			f.answered(answer{})
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

// Press waits for key, the keys at the foot saying what it does.
func (f *BootFace) Press(ctx context.Context, step, key, does string) error {
	_, err := f.ask(ctx, &question{step: step, kind: pressing, key: key, does: does})
	return err
}

// clock says how long d is: 41s, 4m31s.
func clock(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
