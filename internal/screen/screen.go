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

// Locked reports whether the screen of kit's user is locked, as
// IOConsoleUsers says: their session at the Mac locked, or not on the
// screen at all, as with another user's switched to, or the login window.
// False when the registry doesn't list the sessions, or can't be read.
func Locked(ctx context.Context, run runner.Runner) bool {
	res, err := run.Run(ctx, Query())
	if err != nil {
		return false
	}
	root, err := plist.Decode(res.Stdout)
	if err != nil {
		return false
	}
	users := consoleUsers(root)
	if users == nil {
		return false
	}
	uid := int64(os.Getuid())
	for _, user := range users {
		session, _ := user.(map[string]any)
		id, _ := session["kCGSSessionUserIDKey"].(int64)
		onScreen := session["kCGSSessionOnConsoleKey"] != false
		if id == uid && onScreen && session["CGSSessionScreenIsLocked"] != true {
			return false
		}
	}
	return true
}

// consoleUsers are the sessions IOConsoleUsers lists, wherever v, what
// ioreg printed, holds it: nil when it doesn't.
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
