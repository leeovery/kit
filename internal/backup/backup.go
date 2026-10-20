// Package backup is the backup tools kit knows, each a driver in a folder
// of its own keeping the contract in tool: a tool comes and goes with its
// line in Tools. A Mac's [backups] section names the tools it uses, and
// only those are checked.
package backup

import (
	"slices"

	"github.com/leeovery/kit/internal/backup/arq"
	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/backup/tool"
)

// Tools are the backup tools kit knows.
var Tools = []tool.Driver{
	timemachine.Driver{},
	arq.Driver{},
}

// Named is the tool whose ID is id, and whether kit knows one.
func Named(id string) (tool.Driver, bool) {
	i := slices.IndexFunc(Tools, func(d tool.Driver) bool { return d.About().ID == id })
	if i < 0 {
		return nil, false
	}
	return Tools[i], true
}

// IDs are the IDs of the tools kit knows, sorted.
func IDs() []string {
	ids := make([]string, len(Tools))
	for i, d := range Tools {
		ids[i] = d.About().ID
	}
	slices.Sort(ids)
	return ids
}

// Excluders keep [backup exclusions] out of the backups of the tools named
// by their IDs: each one's own, or, for one keeping none of its own, the
// list of the tool it follows, whether or not that tool's named; each
// once.
func Excluders(ids []string) []tool.Excluder {
	var out []tool.Excluder
	var keepers []string
	for _, id := range ids {
		d, ok := Named(id)
		if ok && d.Excluder() == nil {
			d, ok = Named(d.About().Follows)
		}
		if !ok || d.Excluder() == nil || slices.Contains(keepers, d.About().ID) {
			continue
		}
		keepers = append(keepers, d.About().ID)
		out = append(out, d.Excluder())
	}
	return out
}
