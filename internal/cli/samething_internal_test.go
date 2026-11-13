package cli

import "testing"

// A thing named by its full name is that thing; a tap's thing is also its
// own short name, but two full names never match by their last parts.
func TestSameThing(t *testing.T) {
	for _, tt := range []struct {
		name, thing string
		same        bool
	}{
		{"ripgrep", "ripgrep", true},
		{"owner/tap/tool", "tool", true},
		{"tool", "owner/tap/tool", true},
		{"owner/tap/tool", "owner/tap/tool", true},
		{"owner/tap/tool", "other/tap/tool", false},
		{"@a/cli", "@b/cli", false},
		{"example.com/x/tool", "other.org/tool", false},
		{"tool", "other", false},
	} {
		if got := sameThing(tt.name, tt.thing); got != tt.same {
			t.Errorf("sameThing(%q, %q) = %t, want %t", tt.name, tt.thing, got, tt.same)
		}
	}
}
