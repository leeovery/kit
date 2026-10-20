package kind_test

import (
	"testing"

	"github.com/leeovery/kit/internal/kind"
)

// What kinds share is made once a Deps, by its key: a key of the same name,
// or of another type, keeps a thing of its own.
func TestShare(t *testing.T) {
	d := &kind.Deps{}
	first, second := kind.NewKey[*int]("tool"), kind.NewKey[*int]("tool")
	other := kind.NewKey[string]("tool")
	built := 0
	build := func() *int { built++; n := built; return &n }
	a, b := kind.Share(d, first, build), kind.Share(d, first, build)
	c := kind.Share(d, second, build)
	s := kind.Share(d, other, func() string { return "its own" })
	if a != b || *a != 1 || *c != 2 || s != "its own" || built != 2 {
		t.Errorf("shared %d, %d, %d, %q, built %d times: want the first made once, the second its own, the string its own", *a, *b, *c, s, built)
	}
	if again := kind.Share(&kind.Deps{}, first, build); *again != 3 {
		t.Errorf("another Deps shared %d, want a thing of its own", *again)
	}
}
