package kind

import (
	"context"

	"github.com/leeovery/kit/internal/config"
)

// Declared is what k declares for the Mac named mac: its sections in cfg,
// their values read by a kind whose lines carry them (Valued), or their
// patterns expanded (Expander). What doesn't read is unread: the kind's
// check fails, saying so, rather than kit.
func Declared(cfg *config.Config, k Kind, mac string) (list config.List, unread, err error) {
	if list, err = cfg.List(k.Name(), mac); err != nil {
		return list, nil, err
	}
	if v, ok := k.(Valued); ok {
		read, unread := v.Values(list)
		return read, unread, nil
	}
	if e, ok := k.(Expander); ok {
		expanded, unread := e.Expand(list)
		return expanded, unread, nil
	}
	return list, nil, nil
}

// Line is the line declaring name, one of k's things, adding or adopting it
// declares: its value, for a kind whose lines have one (Valued), as it's
// installed; note, after what the kind says it is (Describer), or that
// alone as the note kit makes up (DefaultNote).
func Line(ctx context.Context, k Kind, name, note string) (config.Entry, error) {
	e := config.Entry{Name: name, Note: note}
	if d, ok := k.(Describer); ok {
		if what := d.Describe(ctx, name); what != "" && note != "" {
			e.Note = what + ": " + note
		} else {
			e.DefaultNote = what
		}
	}
	if v, ok := k.(Valued); ok {
		var err error
		if e.Value, err = v.Value(ctx, name); err != nil {
			return e, err
		}
	}
	return e, nil
}
