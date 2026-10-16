package cli

import (
	"context"

	"github.com/leeovery/kit/internal/steps"
)

// AskingNotice is what watching for macOS asking does with notices, for a
// test: changed is told what macOS is asking, each time that changes, and
// end is told the watch has ended.
func AskingNotice(notices steps.Notifier) (changed func(ctx context.Context, what string), end func(ctx context.Context)) {
	n := &askingNotice{notices: notices}
	return n.changed, n.end
}
