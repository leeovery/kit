package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/runner"
)

// syncWithin is how long a change to the config repository may wait
// uncommitted, or a commit unpushed: kit commits and pushes its own changes
// at once, so one left over is an edit by hand, or a push that failed.
const syncWithin = time.Hour

// ConfigSync checks the config repository in dir has nothing uncommitted,
// or unpushed, for over an hour.
func ConfigSync(run runner.Runner, dir string, now func() time.Time) engine.Step {
	git := func(ctx context.Context, args ...string) (string, error) {
		return output(ctx, run, "git", append([]string{"-C", dir}, args...)...)
	}
	return engine.Step{
		Name: "config-sync", Title: "Config repository's sync", Area: AreaConfig,
		Check: func(ctx context.Context) check.Result {
			status, err := git(ctx, "status", "--porcelain")
			if err != nil {
				return failed(err)
			}
			var waiting []string
			for line := range strings.Lines(status) {
				if len(line) < 4 {
					continue
				}
				name := strings.Trim(strings.TrimSpace(line[3:]), `"`)
				if _, to, ok := strings.Cut(name, " -> "); ok {
					name = strings.Trim(to, `"`)
				}
				info, err := os.Stat(filepath.Join(dir, name))
				if err != nil || now().Sub(info.ModTime()) > syncWithin {
					waiting = append(waiting, name)
				}
			}
			var problems [][3]string
			if len(waiting) > 0 {
				problems = append(problems, [3]string{"uncommitted", fmt.Sprintf("%d changed files uncommitted for over an hour: %s", len(waiting), strings.Join(waiting, ", ")), "commit them, or discard them: git -C " + dir + " status"})
			}
			// What's committed and not pushed, oldest first: none when
			// there's no upstream to push to.
			unpushed, err := git(ctx, "log", "@{u}..HEAD", "--format=%ct")
			if err == nil {
				var oldest int64
				count := 0
				for f := range strings.FieldsSeq(unpushed) {
					t, _ := strconv.ParseInt(f, 10, 64)
					if count == 0 || t < oldest {
						oldest = t
					}
					count++
				}
				if count > 0 && now().Sub(time.Unix(oldest, 0)) > syncWithin {
					problems = append(problems, [3]string{"unpushed", fmt.Sprintf("%d commits not pushed, the oldest over an hour old", count), "push them: git -C " + dir + " push (kit pulls with rebase first when it commits)"})
				}
			}
			if len(problems) > 0 {
				return problem("config-sync", "changes waiting", problems...)
			}
			return check.Result{State: check.OK, Summary: "committed and pushed"}
		},
	}
}
