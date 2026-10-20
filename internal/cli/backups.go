package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/backup"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/steps"
)

// backups are the backup tools this Mac uses, as its [backups] lines say,
// the shared file's then its own: a tool kit doesn't know, or an option a
// line doesn't take, is refused, saying where.
func (r *run) backups() ([]steps.BackupTool, error) {
	list, err := r.cfg.List(config.BackupsKind, r.machine)
	if err != nil {
		return nil, err
	}
	tools := make([]steps.BackupTool, len(list.Entries))
	for i, e := range list.Entries {
		if tools[i], err = steps.ParseBackup(e); err != nil {
			return nil, err
		}
	}
	return tools, nil
}

// backupNames are the IDs of tools.
func backupNames(tools []steps.BackupTool) []string {
	names := make([]string, len(tools))
	for i, b := range tools {
		names[i] = b.Driver.About().ID
	}
	return names
}

// placeBackups places the step of each tool among all, the run's steps, as
// its line says: after the steps it names, waiting for the things, checking
// once they're in, as a step of your own does, and before the steps it
// names. One placed after or before what isn't a step fails, saying so.
func (r *run) placeBackups(all []engine.Step, tools []steps.BackupTool) {
	names := make(map[string]bool, len(all))
	for _, s := range all {
		names[s.Name] = true
	}
	for _, b := range tools {
		i := slices.IndexFunc(all, func(s engine.Step) bool { return s.Name == b.Driver.About().ID })
		if i < 0 {
			continue
		}
		after, waits, before, err := r.place(b.After, b.Before, names)
		if err != nil {
			broken(&all[i], fmt.Sprintf("%s: %s", b.Pos, err))
			continue
		}
		all[i].After = append(all[i].After, after...)
		all[i].Waits = append(all[i].Waits, waits...)
		all[i].Before = append(all[i].Before, before...)
	}
}

// notAFeature is the error of naming a backup tool as a feature, as it
// goes in [backups] now: nil for any other name.
func notAFeature(name string) error {
	if _, ok := backup.Named(name); !ok {
		return nil
	}
	return fmt.Errorf("%s is a backup tool, so it goes in [backups] now, not [features]", name)
}

// movedToBackups is the error of a [features] line naming a backup tool,
// saying where it is and what its line in [backups] is: nil for any other.
func movedToBackups(e config.Entry) error {
	err := notAFeature(e.Name)
	if err == nil {
		return nil
	}
	line := []string{e.Name}
	for _, after := range e.After() {
		line = append(line, "--after", config.Quote(after))
	}
	return fmt.Errorf("%s: %w: its line there is %s", e.Pos(), err, strings.Join(line, " "))
}
