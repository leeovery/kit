package backup_test

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/backup"
	"github.com/leeovery/kit/internal/backup/arq"
	"github.com/leeovery/kit/internal/backup/timemachine"
	"github.com/leeovery/kit/internal/backup/tool"
)

// Every tool kit knows is in the list, each once by its ID, complete: a
// name, its set-up as a to-do says it, and its exclusions, its own or those
// of a tool it follows that keeps its own.
func TestToolsComplete(t *testing.T) {
	if got := backup.IDs(); !slices.Equal(got, []string{"arq", "time-machine"}) {
		t.Errorf("IDs() = %q, want every tool kit has, sorted", got)
	}
	seen := map[string]bool{}
	for _, d := range backup.Tools {
		a := d.About()
		if seen[a.ID] {
			t.Errorf("%s is listed twice", a.ID)
		}
		seen[a.ID] = true
		if named, ok := backup.Named(a.ID); !ok || named.About().ID != a.ID {
			t.Errorf("Named(%q) = %v, %v", a.ID, named, ok)
		}
		s := a.SetUp
		if a.Name == "" || s.Title == "" || s.Why == "" || len(s.Steps) == 0 || s.Watch == "" || s.Do == "" {
			t.Errorf("%s: About() = %+v, want its name and its set-up whole", a.ID, a)
		}
		switch keeper, ok := backup.Named(a.Follows); {
		case d.Excluder() != nil && a.Follows != "":
			t.Errorf("%s keeps exclusions of its own and follows %s's too", a.ID, a.Follows)
		case d.Excluder() == nil && (!ok || keeper.Excluder() == nil):
			t.Errorf("%s keeps no exclusions, and follows %q, which isn't a tool keeping its own", a.ID, a.Follows)
		}
	}
	if _, ok := backup.Named("time-capsule"); ok {
		t.Error("Named() knows a tool kit hasn't")
	}
}

// The exclusions are written by the tools a Mac uses: Time Machine's own,
// listed alone or with Arq, once; Arq's, alone, by Time Machine's list, as
// it follows it; none for no tool, or one kit doesn't know.
func TestExcluders(t *testing.T) {
	tm := timemachine.Driver{}.Excluder()
	for _, tt := range []struct {
		name string
		ids  []string
		want []tool.Excluder
	}{
		{"Time Machine", []string{timemachine.ID}, []tool.Excluder{tm}},
		{"Arq alone", []string{arq.ID}, []tool.Excluder{tm}},
		{"both", []string{arq.ID, timemachine.ID}, []tool.Excluder{tm}},
		{"none", nil, nil},
		{"one kit doesn't know", []string{"time-capsule"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := backup.Excluders(tt.ids); !slices.Equal(got, tt.want) {
				t.Errorf("Excluders(%q) = %v, want %v", tt.ids, got, tt.want)
			}
		})
	}
}
