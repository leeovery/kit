package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/leeovery/kit/internal/boot"
)

// Without a terminal, the boot's questions fail, naming the flag that
// answers each: which repository is kit-config, with the question, and
// that a new one is made at a terminal or by kit setup; this Mac's name.
func TestBootWithoutATerminal(t *testing.T) {
	ask := &plainAsker{}
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"which repository", func() error {
			_, err := ask.Choose(context.Background(), boot.GitHubStep, "someone has no repository named kit-config", nil)
			return err
		}(), "no terminal to ask at: someone has no repository named kit-config: name your config repository with --config owner/name; a new one is made at a terminal, or with kit setup (the github step)"},
		{"its name, typed", func() error {
			_, err := ask.Text(context.Background(), boot.GitHubStep, "its name, as owner/name", "")
			return err
		}(), "no terminal to ask at: its name, as owner/name: name your config repository with --config owner/name; a new one is made at a terminal, or with kit setup (the github step)"},
		{"this Mac", func() error {
			_, err := ask.Choose(context.Background(), boot.MacStep, "which of your Macs is this?", nil)
			return err
		}(), "no terminal to ask at: give this Mac's name with --mac (the mac step)"},
	} {
		if !errors.Is(tt.err, errNoTerminal) || tt.err.Error() != tt.want {
			t.Errorf("%s: %v; want %q", tt.name, tt.err, tt.want)
		}
	}
}
