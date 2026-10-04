package cli

import (
	"context"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/kind"
)

// decls are where a kind's declarations are kept, and how kit changes them:
// kit's list files, or, for a kind declared in files of its own that kit
// writes, the kind itself.
type decls interface {
	// file is the file a thing's declared in: the one every Mac reads, or
	// this Mac's own.
	file(shared bool) string
	// grouped reports whether the files have groups to put things in.
	grouped() bool
	groups(file string) ([]string, error)
	// declare declares name in file, in group, with note.
	declare(ctx context.Context, file, name, group, note string) error
	undeclare(file, name string) error
}

// decls are the declarations of the kind called kindName.
func (r *run) decls(kindName string) decls {
	if w, ok := r.kindsByName[kindName].(kind.Writer); ok {
		return writerDecls{w}
	}
	return listDecls{cfg: r.cfg, kind: kindName, machine: r.machine}
}

// listDecls are a kind's declarations in kit's list files: the kind's own
// name for every Mac, and the kind and the Mac's name for one.
type listDecls struct {
	cfg           *config.Config
	kind, machine string
}

func (d listDecls) file(shared bool) string {
	if shared {
		return d.kind
	}
	return d.kind + "." + d.machine
}

func (listDecls) grouped() bool { return true }

func (d listDecls) groups(file string) ([]string, error) { return d.cfg.Groups(file) }

func (d listDecls) declare(_ context.Context, file, name, group, note string) error {
	return d.cfg.Declare(file, name, group, note)
}

func (d listDecls) undeclare(file, name string) error { return d.cfg.Undeclare(file, name) }

// writerDecls are a kind's declarations in files of its own, which the kind
// writes: they have no groups, and declaring a thing records it as it's
// installed.
type writerDecls struct{ w kind.Writer }

func (d writerDecls) file(shared bool) string { return d.w.FileFor(shared) }

func (writerDecls) grouped() bool { return false }

func (writerDecls) groups(string) ([]string, error) { return nil, nil }

func (d writerDecls) declare(ctx context.Context, file, name, _, note string) error {
	return d.w.Adopt(ctx, file, name, note)
}

func (d writerDecls) undeclare(file, name string) error { return d.w.Undeclare(file, name) }
