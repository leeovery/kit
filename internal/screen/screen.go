// Package screen says how the Mac's screen stands, as macOS's registry has
// it: whether the screen of the person kit runs as is locked. Reading it
// asks no one, and works over a remote connection.
package screen

import (
	"context"
	"os"
	"time"

	"github.com/leeovery/kit/internal/plist"
	"github.com/leeovery/kit/internal/runner"
)

// Query is the command that reads it: the registry's root, IOConsoleUsers
// among what it holds, as a property list.
func Query() runner.Command {
	return runner.Command{Name: "ioreg", Args: []string{"-n", "Root", "-d1", "-a"}, Timeout: 10 * time.Second}
}

// Locked reports whether the screen of the session kit's user has at the
// Mac is locked, as IOConsoleUsers says: false when it doesn't say, or
// can't be read.
func Locked(ctx context.Context, run runner.Runner) bool {
	res, err := run.Run(ctx, Query())
	if err != nil {
		return false
	}
	root, err := plist.Decode(res.Stdout)
	if err != nil {
		return false
	}
	uid := int64(os.Getuid())
	for _, user := range consoleUsers(root) {
		session, _ := user.(map[string]any)
		if id, _ := session["kCGSSessionUserIDKey"].(int64); id == uid && session["CGSSessionScreenIsLocked"] == true {
			return true
		}
	}
	return false
}

// consoleUsers are the sessions IOConsoleUsers lists, wherever v, what
// ioreg printed, holds it: none when it doesn't.
func consoleUsers(v any) []any {
	switch v := v.(type) {
	case map[string]any:
		if users, ok := v["IOConsoleUsers"].([]any); ok {
			return users
		}
	case []any:
		for _, each := range v {
			if users := consoleUsers(each); users != nil {
				return users
			}
		}
	}
	return nil
}
