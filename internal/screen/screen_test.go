package screen_test

import (
	"os"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/screen"
	"github.com/leeovery/kit/internal/screen/screentest"
)

// The screen is locked when the session kit's user has at the Mac says so;
// another user's locked screen, a session that doesn't say, and a registry
// that can't be read are taken as unlocked, as kit took every screen before
// it read one.
func TestLocked(t *testing.T) {
	uid := os.Getuid()
	for _, c := range []struct {
		name   string
		prints string
		fails  bool
		want   bool
	}{
		{"locked", screentest.Root(uid, true), false, true},
		{"unlocked", screentest.Root(uid, false), false, false},
		{"another user's locked", screentest.Root(uid+1, true), false, false},
		{"not said", strings.Replace(screentest.Root(uid, true), "<key>CGSSessionScreenIsLocked</key>\n\t\t\t<true/>", "", 1), false, false},
		{"in an array", "<plist version=\"1.0\"><array>" + plistBody(screentest.Root(uid, true)) + "</array></plist>", false, true},
		{"no console users", `<plist version="1.0"><dict><key>IOKitBuildVersion</key><string>x</string></dict></plist>`, false, false},
		{"not a property list", "IOConsoleUsers locked", false, false},
		{"ioreg fails", "", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			fake := runnertest.New(t)
			q := screen.Query()
			s := fake.On(q.Name, q.Args...).Prints(c.prints)
			if c.fails {
				s.Fails(runner.ErrNotFound)
			}
			if got := screen.Locked(t.Context(), fake); got != c.want {
				t.Errorf("Locked() = %v, want %v", got, c.want)
			}
		})
	}
}

// plistBody is the value a property list holds, without its header.
func plistBody(doc string) string {
	_, body, _ := strings.Cut(doc, `<plist version="1.0">`)
	body, _, _ = strings.Cut(body, "</plist>")
	return body
}
