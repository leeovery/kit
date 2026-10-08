package steps_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/prefs"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

func TestPrefs(t *testing.T) {
	recent, old := now().Add(-2*time.Hour).Format(time.RFC3339), now().Add(-40*time.Hour).Format(time.RFC3339)
	for _, tt := range []struct {
		name, record string
		state        check.State
		text, id     string
	}{
		{"captured", `{"on": "started fresh", "captured": "` + recent + `", "summary": "57 domains, 2 changed; 1056 files, 0 changed"}`, check.OK, "saved 10:00", ""},
		{"never switched on", `{}`, check.Attention, "paused: capture isn't switched on for this Mac", "prefs:paused"},
		{"not captured yet", `{"on": "started fresh"}`, check.Attention, "not captured yet", "prefs:never"},
		{"stale", `{"on": "started fresh", "captured": "` + old + `"}`, check.Attention, "last capture", "prefs:stale"},
		{"errors", `{"on": "started fresh", "captured": "` + recent + `", "summary": "57 domains; 1 errors", "errors": 1}`, check.Attention, "57 domains; 1 errors", "prefs:errors"},
		{"unpushed for a day", `{"on": "started fresh", "captured": "` + recent + `", "unpushed": "` + old + `", "push_error": "offline"}`, check.Attention, "not pushed since", "prefs:unpushed"},
		{"another Mac's folder", `{"on": "restored", "not_owner": "the store's laptop folder belongs to another Mac"}`, check.Attention, "another Mac owns this Mac's folder", "prefs:not-owner"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := t.TempDir()
			if err := os.WriteFile(filepath.Join(state, "prefs.json"), []byte(tt.record), 0o600); err != nil {
				t.Fatal(err)
			}
			p := prefs.New(runnertest.New(t), t.TempDir(), state, t.TempDir(), "", "laptop", prefs.Lists{}, now)
			res := steps.Prefs(p, now).Check(t.Context())
			if res.State != tt.state || !strings.Contains(res.Summary, tt.text) || (tt.id != "" && (len(res.Items) != 1 || res.Items[0].ID != tt.id)) {
				t.Errorf("result = %+v", res)
			}
		})
	}
}
