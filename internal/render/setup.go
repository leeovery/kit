package render

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/setup"
)

// SetupFace is kit setup's face at a terminal, the boot's counterpart for a
// Mac in use (kit-look, round 13), in kit's own look: the wordmark, with
// setup, the Mac and when beside it; SETUP, a row a step; once that's
// done, DECLARED, a row a part of the snapshot, with what it found as it's
// found, then a row a question; once it's committed, the apps installed by
// hand, where secrets go, and what carries on. What a step asks is asked
// in its row; signing in takes the whole window, the way chosen, as the
// boot's does. Drawn in place under what's on screen, and left there.
type SetupFace struct {
	t    ask.Terminal
	dark bool
	now  func() time.Time
	// stop stops setup, as esc does; mac is this Mac's name, its model's
	// till it's named, read once, then again once it's named.
	stop func()
	mac  func() string
	name *string
	when time.Time

	mu    sync.Mutex
	steps []event.Step
	rows  map[string]*bootRow
	parts []event.Finding
	q     *question
	// picked is whether the way to sign in has been asked; mode is that
	// way, which has the whole window, and signing the step it's for.
	picked  bool
	mode    string
	signing string
	qr      qrCode
	// stopping is whether esc has stopped setup; halted, whether a step
	// has said the run was stopped, however it was.
	stopping bool
	halted   bool
	// carries is what carries on once setup's done, said last.
	carries string
	frame   int

	changed chan struct{}
	closed  bool
	live    chan error
	quit    chan struct{}
}

// NewSetupFace is kit setup's face at t: dark is whether its background is,
// stop stops setup when esc is pressed, and mac names this Mac as it's
// known.
func NewSetupFace(t ask.Terminal, dark bool, now func() time.Time, stop func(), mac func() string) *SetupFace {
	return &SetupFace{t: t, dark: dark, now: now, stop: stop, mac: mac, when: now(), rows: map[string]*bootRow{}, changed: make(chan struct{}, 1)}
}

func (f *SetupFace) signal() {
	if f.closed {
		return
	}
	select {
	case f.changed <- struct{}{}:
	default:
	}
}

// Start shows setup in place, and keeps it drawn till Close: a run's steps
// as its start lists them. A screen that ends early, its keys gone, stops
// setup.
func (f *SetupFace) Start() {
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
				f.signal()
				f.mu.Unlock()
			}
		}
	}()
	go func() {
		err := ask.Live(context.Background(), f.t, f)
		if err != nil && f.stop != nil {
			f.stop()
		}
		f.live <- err
	}()
}

func (f *SetupFace) Emit(e event.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch e := e.(type) {
	case event.RunStarted:
		f.steps = e.Steps
	case event.StepStarted:
		if r := f.row(e.Step); !r.running {
			r.running, r.began, r.finished = true, e.Time, nil
		}
	case event.Doing:
		f.row(e.Step).doing = e.Says
	case event.DeviceCode:
		code := e
		f.row(e.Step).code = &code
	case event.Finding:
		f.find(e)
	case event.StepFinished:
		if e.Step == f.signing {
			f.mode, f.signing = "", ""
		}
		if e.Step == setup.MacStep {
			f.name = nil
		}
		r := f.row(e.Step)
		result := e.Result
		r.running, r.code, r.finished = false, nil, &result
		f.halted = f.halted || result.Reason == event.ErrStopped.Error()
	default:
		return
	}
	f.signal()
}

func (f *SetupFace) row(step string) *bootRow {
	r, ok := f.rows[step]
	if !ok {
		r = &bootRow{}
		f.rows[step] = r
	}
	return r
}

// macName is this Mac's name, as mac says it, read once till it's named.
func (f *SetupFace) macName() string {
	if f.name == nil {
		name := f.mac()
		f.name = &name
	}
	return *f.name
}

// find notes a part of the snapshot as it's found: a part first told of
// takes its place after the others.
func (f *SetupFace) find(e event.Finding) {
	if i := slices.IndexFunc(f.parts, func(p event.Finding) bool { return p.Part == e.Part }); i >= 0 {
		f.parts[i] = e
		return
	}
	f.parts = append(f.parts, e)
}

// Finish says, under setup once it's done, what carries on: a command, and
// what it does.
func (f *SetupFace) Finish(command, does string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.carries = look.Cmd(command) + look.Muted(" "+does)
	f.signal()
}

// Close leaves setup where it was drawn, as it ended.
func (f *SetupFace) Close() error {
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

func (f *SetupFace) Changes() <-chan struct{} { return f.changed }

// View is setup as it stands, never taller than height, its running marks
// turning.
func (f *SetupFace) View(width, height int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	width = min(width, look.Width)
	lines := fit(f.blocks(width, height, true), f.foot(width, true), height, true)
	spin := look.Cyan(spinning[f.frame%len(spinning)])
	for i, l := range lines {
		lines[i] = strings.ReplaceAll(l, look.Mark(look.Running), spin)
	}
	return lines
}

// Leaves is setup as it ended, to stay in the scrollback.
func (f *SetupFace) Leaves(width int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	width = min(width, look.Width)
	for _, b := range f.blocks(width, 0, false) {
		out = append(out, b.lines...)
	}
	return append(out, f.foot(width, false)...)
}

// blocks are what's shown, a blank line between blocks: the head; SETUP;
// once that's done, DECLARED; once setup's committed, the apps installed
// by hand and where secrets go. Each row a block of its own, to give way
// as fit has it, a question's within height, when there's one.
func (f *SetupFace) blocks(width, height int, live bool) []block {
	out := []block{{state: look.Done, heading: true, lines: append([]string{""}, look.Head(look.Meta("setup", f.macName(), when(f.when))...)...)}}
	rows := func(rs ...look.Row) {
		for _, r := range rs {
			out = append(out, block{state: r.State, lines: look.Timeline("  ", width, r)})
		}
	}
	for _, a := range revealed(f.steps, f.rows) {
		out = append(out, block{state: look.Done, heading: true, lines: []string{"", look.Header(a.name, f.note(a))}})
		for _, s := range a.steps {
			switch {
			case s.Name == setup.SnapshotStep:
				rows(f.found(s, width)...)
			case s.Name == setup.InstallStep && !f.installs():
			case s.Name == setup.CommitStep, s.Name == setup.ByHandStep, s.Name == setup.SecretsStep:
			default:
				rows(f.stepRow(s, width, height, live))
			}
		}
	}
	if b, ok := f.byHand(width); ok {
		out = append(out, b)
	}
	if r := f.rows[setup.SecretsStep]; r != nil && r.finished != nil && r.finished.State == check.OK {
		said, command := r.finished.Summary, ""
		if i := strings.LastIndex(said, ": kit "); i >= 0 {
			said, command = said[:i+2], said[i+2:]
		}
		out = append(out, block{state: look.Done, heading: true, lines: append([]string{""}, margined(toDo(width-2, said, command))...)})
	}
	return out
}

// installs reports whether the install step has a row: once it asks, or
// once it's said what kit-config declares that this Mac hasn't. A
// kit-config setup started has nothing in it to install, and never has
// one.
func (f *SetupFace) installs() bool {
	if f.q != nil && f.q.step == setup.InstallStep {
		return true
	}
	r := f.rows[setup.InstallStep]
	return r != nil && f.reached(r.finished) && r.finished.Summary != setup.NothingNew
}

// note is what an area's header says: how far SETUP's got, while it's
// asking; DECLARED, what it declares, and how much, once it's committed.
func (f *SetupFace) note(a area) string {
	if a.name != setup.AreaDeclared {
		for i, s := range a.steps {
			if r := f.rows[s.Name]; r == nil || r.finished == nil || r.finished.State != check.OK {
				return fmt.Sprintf("%d of %d", i+1, len(a.steps))
			}
		}
		return ""
	}
	i := slices.IndexFunc(a.steps, func(s event.Step) bool { return s.Name == setup.SnapshotStep })
	if i < 0 {
		return ""
	}
	note := a.steps[i].Waiting
	if r := f.rows[setup.CommitStep]; r != nil && r.finished != nil && r.finished.State == check.OK {
		total := 0
		for _, n := range r.finished.Counts {
			total += n
		}
		note = fmt.Sprintf("%d · %s", total, note)
	}
	return note
}

// found are the snapshot's parts, a row each, as they're found, but those
// a step's row shows; and the snapshot's own, when it didn't go, but for
// being stopped, as its parts say so.
func (f *SetupFace) found(s event.Step, width int) []look.Row {
	var out []look.Row
	for _, p := range f.parts {
		if f.titled(p.Part) {
			continue
		}
		row := look.Row{State: look.Queued, Name: p.Part}
		switch {
		case f.cancelled(p.Err):
			row.State, row.Says = look.Skipped, look.Muted("stopped")
		case p.Err != "":
			row.State, row.Says = look.Failed, look.Words(look.Failed, p.Err)
		case p.Found && p.Count == 0:
			row.State, row.Says = look.Done, look.Muted("none")
		case p.Found && len(p.Names) > 0:
			row.State, row.Says = look.Done, look.Says(muted(p.Names)...)
		case p.Found:
			row.State, row.Says = look.Done, look.Muted(fmt.Sprint(p.Count))
		case p.Doing != "":
			row.State, row.Says = look.Running, look.Words(look.Running, p.Doing)
		}
		out = append(out, row)
	}
	if r := f.rows[s.Name]; r != nil && f.reached(r.finished) && r.finished.State != check.OK {
		out = append(out, f.ended(s.Title, *r.finished, width))
	}
	return out
}

// cancelled reports whether why is the run stopped: the reason the
// engine gives a step it was stopped before, or in the middle of; or, the
// run stopped, a part of the snapshot cut short by it.
func (f *SetupFace) cancelled(why string) bool {
	return why == event.ErrStopped.Error() || f.stopped() && strings.Contains(why, context.Canceled.Error())
}

// stopped reports whether the run's been stopped, by esc or otherwise.
func (f *SetupFace) stopped() bool { return f.stopping || f.halted }

// muted are words, each muted, for a row's says.
func muted(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = look.Muted(w)
	}
	return out
}

// titled reports whether a step's row is titled name.
func (f *SetupFace) titled(name string) bool {
	return slices.ContainsFunc(f.steps, func(s event.Step) bool { return s.Title == name })
}

// stepRow is a step's row as it stands: asking, done, at work or waiting.
// kit-config's says how setup's commit went, once it's begun; a question's,
// waiting, what's done for it meanwhile, as its part of the snapshot.
func (f *SetupFace) stepRow(s event.Step, width, height int, live bool) look.Row {
	r := f.rows[s.Name]
	if s.Name == setup.ConfigStep {
		if c := f.rows[setup.CommitStep]; c != nil && (c.running || f.reached(c.finished)) && r != nil && r.finished != nil {
			return f.committed(s.Title, *r.finished, c, width)
		}
	}
	switch {
	case f.q != nil && f.q.step == s.Name && f.q.kind != picking:
		return f.asking(s.Title, width, height)
	case r != nil && r.finished != nil:
		return f.ended(s.Title, *r.finished, width)
	case r != nil && r.running:
		row := look.Row{State: look.Running, Name: s.Title, Says: look.Words(look.Running, cmp.Or(r.doing, "working"))}
		if live && r.code != nil {
			row.Under = []string{look.Todo(look.Muted("enter ") + look.Cmd(r.code.Code) + look.Muted(" at "+site(r.code.URI)))}
		}
		return row
	}
	if i := slices.IndexFunc(f.parts, func(p event.Finding) bool { return p.Part == s.Title && p.Doing != "" }); i >= 0 {
		return look.Row{State: look.Running, Name: s.Title, Says: look.Words(look.Running, f.parts[i].Doing)}
	}
	return look.Row{State: look.Queued, Name: s.Title}
}

// reached reports whether a step ended as it ran to its end: done, needing
// you, or failed, not skipped or stopped.
func (f *SetupFace) reached(res *check.Result) bool {
	return res != nil && res.State != check.Deferred && !f.cancelled(cmp.Or(res.Reason, res.Summary))
}

// asking is the row of the question being asked: the question beside its
// name, what more it says under it, muted, then what answers it, as much
// of a checklist as height leaves room for.
func (f *SetupFace) asking(title string, width, height int) look.Row {
	text, more, _ := strings.Cut(f.q.text, "\n")
	parts := strings.Split(text, " · ")
	for i, p := range parts {
		parts[i] = look.Words(look.NeedsYou, p)
	}
	row := look.Row{State: look.NeedsYou, Name: title, Says: look.Says(parts...), Whole: true}
	if more != "" {
		for _, l := range look.Wrap(more, width-4) {
			row.Under = append(row.Under, look.Muted(l))
		}
	}
	room := 0
	if height > 0 {
		room = max(height-len(look.Timeline("  ", width, row))-len(f.foot(width, true)), 3)
	}
	row.Under = append(row.Under, f.asked(room)...)
	return row
}

// committed is kit-config's row once setup's commit has begun: committing,
// then pushed, kept when the row's cut, or why not.
// What the commit refused, or declared unchecked, goes under it.
func (f *SetupFace) committed(title string, chosen check.Result, c *bootRow, width int) look.Row {
	if c.finished == nil {
		return look.Row{State: look.Running, Name: title, Says: look.Words(look.Running, "committing and pushing")}
	}
	row := f.ended(title, *c.finished, width)
	if c.finished.State == check.OK {
		row = f.ended(title, chosen, width)
		row.Kept = look.Muted("pushed")
	}
	for _, it := range c.finished.Items {
		row.Under = append(row.Under, look.Fold("  │ ", width, look.Muted(it.Name+" "+it.State)+look.Dim(" · ")+look.Muted(it.Detail))...)
	}
	return row
}

// ended is a step that's ended: done, saying what it did; needing you or
// failed, saying what, and what to do under it; stopped, or skipped.
func (f *SetupFace) ended(title string, res check.Result, width int) look.Row {
	switch res.State {
	case check.OK:
		return look.Row{State: look.Done, Name: title, Says: look.Says(muted(strings.Split(res.Summary, " · "))...)}
	case check.Deferred:
		says := "skipped"
		if f.stopped() {
			says = "stopped"
		}
		return look.Row{State: look.Skipped, Name: title, Says: look.Muted(says)}
	}
	text := cmp.Or(res.Reason, res.Summary)
	if f.cancelled(text) {
		return look.Row{State: look.Skipped, Name: title, Says: look.Muted("stopped")}
	}
	state := look.Failed
	if res.State == check.Attention {
		state = look.NeedsYou
	}
	what, todo, _ := strings.Cut(text, ": ")
	row := look.Row{State: state, Name: title, Says: look.Words(state, what), Whole: true}
	if todo != "" {
		row.Under = toDo(width-4, todo, "")
	}
	return row
}

// byHand is the apps installed by hand, once setup's said which: a row
// each, what's to be done about them under them.
func (f *SetupFace) byHand(width int) (block, bool) {
	r := f.rows[setup.ByHandStep]
	if r == nil || r.finished == nil || r.finished.State != check.OK || r.finished.Summary == "none" {
		return block{}, false
	}
	res := *r.finished
	lines := []string{"", look.Header("Installed by hand", fmt.Sprint(len(res.Items)))}
	_, said, _ := strings.Cut(res.Summary, ": ")
	if len(res.Items) == 0 {
		lines[1], said = look.Header("Installed by hand", ""), res.Summary
	}
	for _, it := range res.Items {
		lines = append(lines, look.Rows("  ", width, look.Row{State: look.Skipped, Name: it.Name})...)
	}
	return block{state: look.Skipped, lines: append(lines, margined(toDo(width-2, said, ""))...)}, true
}

// toDo is what to do, width wide: an arrow, then what's said, then the
// command, white, wrapped under its words. Wrapped here, not by look.Fold,
// which takes what starts "kit " for one of kit's commands, and cuts it.
func toDo(width int, said, command string) []string {
	lines := look.Wrap(said+command, width-2)
	for i, l := range lines {
		styled := look.Muted(l)
		if rest, ok := strings.CutSuffix(l, command); ok && command != "" && i == len(lines)-1 {
			styled = look.Muted(rest) + look.Cmd(command)
		}
		lines[i] = "  " + styled
		if i == 0 {
			lines[i] = look.Todo(styled)
		}
	}
	return lines
}

// margined is lines from column 3.
func margined(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "  " + l
	}
	return out
}

// asked is what answers the question being asked, under its row: a
// checklist longer than room shows the things around the cursor, as kit's
// lists scroll. Room 0 is room for all of them.
func (f *SetupFace) asked(room int) []string {
	switch f.q.kind {
	case choosing:
		return look.Answers(f.q.choices, f.q.cursor)
	case naming:
		return []string{look.Typed(string(f.q.typed))}
	case ticking:
		ticks, cursor := f.q.ticks, f.q.cursor
		if room > 0 && len(ticks) > room {
			from := min(max(cursor-room/2, 0), len(ticks)-room)
			ticks, cursor = ticks[from:from+room], cursor-from
		}
		return look.Checklist(ticks, cursor)
	}
	return nil
}

// foot is what's under setup: what carries on once it's done; the keys
// while it's live.
func (f *SetupFace) foot(width int, live bool) []string {
	var out []string
	if f.carries != "" {
		out = append(out, "", look.Summary(f.carries))
	}
	if !live || f.stopping {
		return out
	}
	stop := look.Key{Key: "esc", Does: "stop"}
	keys := []look.Key{stop}
	if f.q != nil {
		switch f.q.kind {
		case choosing:
			keys = []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "enter", Does: "decide"}, stop}
		case naming:
			keys = []look.Key{{Key: "enter", Does: "done"}, stop}
		case ticking:
			keys = []look.Key{{Key: "↑↓", Does: "choose"}, {Key: "space", Does: "ticks"}, {Key: "enter", Does: "keeps these"}, stop}
		case picking:
			keys = append(slices.Clone(f.q.keys), stop)
		}
	}
	return append(append(out, ""), keyLines(width, keys)...)
}

// keyLines are keys as kit's keys show, width wide, wrapped only between
// keys, so each stays with what it does.
func keyLines(width int, keys []look.Key) []string {
	var lines [][]look.Key
	for _, k := range keys {
		if n := len(lines); n > 0 && ansi.StringWidth(look.Keys(append(slices.Clone(lines[n-1]), k)...)) <= width {
			lines[n-1] = append(lines[n-1], k)
			continue
		}
		lines = append(lines, []look.Key{k})
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = look.Keys(l...)
	}
	return out
}

// Full is signing in, the whole window, the way chosen: with the phone, the
// QR code to scan and the code to enter; here, the code to enter in the
// browser kit opened. nil the rest of the time.
func (f *SetupFace) Full(width, height int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.rows[f.signing]
	if f.mode == "" || r == nil || !r.running {
		return nil
	}
	centre := centred(width)
	head := append(append([]string{""}, look.Head(look.Meta("setup", f.macName(), when(f.when))...)...), "")
	var from int
	var art, says []string
	keys := []look.Key{{Key: "enter", Does: "with your phone instead"}}
	switch c := r.code; {
	case c == nil:
		from, says = height/2, []string{centre(look.Muted("getting a code from GitHub"))}
	case f.mode == "enter":
		for _, l := range f.qr.of(c.URI, func(text string) ([]string, error) { return look.QR(text, f.dark) }) {
			art = append(art, centre(l))
		}
		says = []string{""}
		for _, l := range look.Wrap("Scan it with your phone's camera, then enter this code:", width-4) {
			says = append(says, centre(look.White(l)))
		}
		keys = []look.Key{{Key: "s", Does: "here, in your browser, instead"}}
	default:
		from = height/2 - 4
		says = []string{centre(look.White("Your browser is open at ") + look.Strong(site(c.URI)) + look.White(".")), centre(look.White("Enter this code there:"))}
	}
	if c := r.code; c != nil {
		says = append(says, "", centre(look.Pill(c.Code, look.Done)), "", centre(look.Muted("expires in "+left(c.Expires.Sub(f.now())))))
	}
	return signInWindow(head, from, art, says, look.Keys(append(keys, look.Key{Key: "esc", Does: "back"})...), height)
}

// Key takes a key: esc goes back from signing in, or stops setup, as q does
// but while typing; the rest answer what's asked.
func (f *SetupFace) Key(k ask.Key) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.q
	typing := q != nil && q.kind == naming
	switch {
	case f.mode != "" && k.Is("esc"):
		f.mode = ""
	case k.Is("esc", "ctrl+c") || k.Is("q") && !typing:
		if f.stopping {
			return false
		}
		f.stopping, f.mode = true, ""
		if q != nil {
			f.answered(answer{err: ask.ErrCancelled})
		}
		if f.stop != nil {
			f.stop()
		}
	case q != nil:
		if a, ok := q.take(k); ok {
			if q.kind == picking {
				f.mode, f.signing = a.text, q.step
			}
			f.answered(a)
		}
	}
	return false
}

func (f *SetupFace) answered(a answer) {
	f.q.answer <- a
	f.q = nil
}

// ask asks q, in its step's row, till it's answered or ctx is done.
func (f *SetupFace) ask(ctx context.Context, q *question) (answer, error) {
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
// taken. A question with more to say says it on a line after its first,
// under its row.
func (f *SetupFace) Choose(ctx context.Context, step, text string, choices []look.Choice) (int, error) {
	a, err := f.ask(ctx, &question{step: step, kind: choosing, text: text, choices: choices})
	return a.chosen, err
}

// Text asks for text, typed and shown in step's row, typed to start with.
func (f *SetupFace) Text(ctx context.Context, step, text, typed string) (string, error) {
	a, err := f.ask(ctx, &question{step: step, kind: naming, text: text, typed: []rune(typed)})
	return a.text, err
}

// Tick asks which of ticks to keep, in step's row, each ticked to start
// with as it says: whether each is kept.
func (f *SetupFace) Tick(ctx context.Context, step, text string, ticks []look.Tick) ([]bool, error) {
	a, err := f.ask(ctx, &question{step: step, kind: ticking, text: text, ticks: slices.Clone(ticks)})
	return a.ticked, err
}

// Pick asks the first time which of keys, the ways to sign in, to take, in
// step's row, each way what its key does, and what that is; the way chosen
// then takes the whole window. After, it waits for one of keys, to change
// the way, the keys at the foot when the window is given back.
func (f *SetupFace) Pick(ctx context.Context, step string, keys []boot.Key) (string, error) {
	f.mu.Lock()
	picked := f.picked
	f.picked = true
	f.mu.Unlock()
	if picked {
		hints := make([]look.Key, len(keys))
		for i, k := range keys {
			hints[i] = look.Key{Key: k.Key, Does: k.Does}
		}
		a, err := f.ask(ctx, &question{step: step, kind: picking, keys: hints})
		return a.text, err
	}
	choices := make([]look.Choice, len(keys))
	for i, k := range keys {
		label := []rune(cmp.Or(k.Does, k.Key))
		if len(label) > 0 {
			label[0] = []rune(strings.ToUpper(string(label[0])))[0]
		}
		choices[i] = look.Choice{Label: string(label), Does: k.About}
	}
	a, err := f.ask(ctx, &question{step: step, kind: choosing, text: "not signed in · how will you sign in?", choices: choices})
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	f.mode, f.signing = keys[a.chosen].Key, step
	f.mu.Unlock()
	return keys[a.chosen].Key, nil
}
