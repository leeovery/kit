package steps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/engine"
)

// What applying the shell's steps does about an item.
const (
	ActionWrite   = "write"
	ActionInstall = "install"
)

// PathFileName names the file in kit's state directory that holds the
// shell's PATH, which the shell reads as it starts, running nothing.
const PathFileName = "path"

// PathName is the name of the step that writes the shell's PATH.
const PathName = "path"

// PathFile checks the file at file holds dirs, the PATH the shell puts
// ahead of the one it inherits, colon-separated; applying writes it. While
// it can't be written, new shells keep the last good list.
func PathFile(dirs []string, file string) engine.Step {
	want := strings.Join(dirs, ":") + "\n"
	return engine.Step{
		Name: PathName, Title: "Shell PATH", Area: AreaDrift,
		Check: func(context.Context) check.Result {
			data, err := os.ReadFile(file)
			it := check.Item{ID: "path:shell", Name: "the shell's PATH", Action: ActionWrite}
			switch {
			case errors.Is(err, fs.ErrNotExist):
				it.State, it.Detail = "missing", "kit hasn't written it"
			case err != nil:
				return failed(err)
			case string(data) == want:
				return check.Result{State: check.OK, Summary: fmt.Sprintf("%d %s", len(dirs), plural(len(dirs), "directory", "directories"))}
			default:
				it.State, it.Detail = "changed", "[paths] has changed since kit wrote it"
			}
			return check.Result{State: check.Attention, Summary: "not as [paths] says", Items: []check.Item{it}}
		},
		Apply: func(context.Context, check.Result) error {
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				return err
			}
			tmp := file + ".kit-new"
			if err := os.WriteFile(tmp, []byte(want), 0o644); err != nil {
				return err
			}
			if err := os.Rename(tmp, file); err != nil {
				_ = os.Remove(tmp)
				return err
			}
			return nil
		},
	}
}
