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

// The screen is locked when the session kit's user has at the Mac says so,
// or that session isn't on the screen, or there's none: another user's
// switched to, or the login window. A session that doesn't say it's
// locked, a registry that doesn't list the sessions, and one that can't be
// read are taken as unlocked, as kit took every screen before it read one.
func TestLocked(t *testing.T) {
	uid := os.Getuid()
	offScreen := strings.Replace(screentest.Root(uid, false), "<key>kCGSSessionOnConsoleKey</key>\n\t\t\t<true/>", "<key>kCGSSessionOnConsoleKey</key>\n\t\t\t<false/>", 1)
	for _, c := range []struct {
		name   string
		prints string
		fails  bool
		want   bool
	}{
		{"locked", screentest.Root(uid, true), false, true},
		{"unlocked", screentest.Root(uid, false), false, false},
		{"not on the screen", offScreen, false, true},
		{"another user's alone", screentest.Root(uid+1, false), false, true},
		{"no one's", `<plist version="1.0"><dict><key>IOConsoleUsers</key><array/></dict></plist>`, false, true},
		{"not said", strings.Replace(screentest.Root(uid, true), "<key>CGSSessionScreenIsLocked</key>\n\t\t\t<true/>", "", 1), false, false},
		{"in an array", "<plist version=\"1.0\"><array>" + plistBody(screentest.Root(uid, true)) + "</array></plist>", false, true},
		{"sessions not listed", `<plist version="1.0"><dict><key>IOKitBuildVersion</key><string>x</string></dict></plist>`, false, false},
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
