package check

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestToDo(t *testing.T) {
	for _, c := range []struct {
		name string
		r    Result
		want bool
	}{
		{"a disk to add", Result{State: Attention, Items: []Item{{State: Problem}}}, true},
		{"by hand", Result{State: Attention, Items: []Item{{State: ByHand}, {State: Problem}}}, true},
		{"a package missing too", Result{State: Attention, Items: []Item{{State: Problem}, {State: "missing"}}}, false},
		{"nothing said", Result{State: Attention}, false},
		{"done", Result{State: OK, Items: []Item{{State: Problem}}}, false},
	} {
		if got := c.r.ToDo(); got != c.want {
			t.Errorf("%s: ToDo() = %v, want %v", c.name, got, c.want)
		}
	}
}

// A summary said in parts keeps each whole, what a check printed included,
// semicolons and all: its Summary the parts joined, for what reads it
// whole, and its parts there to show apart; a part that says nothing isn't
// one, and a summary said whole is one part, as is one written since
// without its parts, so none of what it says is lost.
func TestSaying(t *testing.T) {
	dig := ";; connection timed out; no servers could be reached"
	rewritten := Result{}.Saying("committed", "no remote to push to")
	rewritten.Summary = "3 declared · " + rewritten.Summary
	for _, c := range []struct {
		name    string
		r       Result
		summary string
		says    []string
	}{
		{"parts", Result{}.Saying("declared in laptop", "it fails now: "+dig), "declared in laptop; it fails now: " + dig, []string{"declared in laptop", "it fails now: " + dig}},
		{"one part", Result{}.Saying(dig), dig, []string{dig}},
		{"a part saying nothing", Result{}.Saying("installed", ""), "installed", []string{"installed"}},
		{"nothing", Result{}.Saying(), "", nil},
		{"said whole", Result{Summary: dig}, dig, []string{dig}},
		{"added to one said whole", Result{Summary: dig}.Adding("2 changed"), dig + "; 2 changed", []string{dig, "2 changed"}},
		{"added to parts", Result{}.Saying("a", "b").Adding("c", ""), "a; b; c", []string{"a", "b", "c"}},
		{"added to nothing", Result{}.Adding("pushed"), "pushed", []string{"pushed"}},
		{"written since without its parts", rewritten, "3 declared · committed; no remote to push to", []string{"3 declared · committed; no remote to push to"}},
	} {
		if c.r.Summary != c.summary || !slices.Equal(c.r.Says(), c.says) {
			t.Errorf("%s: Summary %q, Says() %q; want %q, %q", c.name, c.r.Summary, c.r.Says(), c.summary, c.says)
		}
	}
}

// A summary's parts go through JSON, as a run's log carries them, and come
// back as they went.
func TestSayingThroughJSON(t *testing.T) {
	r := Result{State: OK}.Saying("declared in laptop", ";; timed out; no servers")
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back Result
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(back.Says(), r.Says()) || back.Summary != r.Summary {
		t.Errorf("%s came back as %+v; want %+v", b, back, r)
	}
}
