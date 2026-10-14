package check

import "testing"

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
