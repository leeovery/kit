// Package screentest stands in for the Mac's screen in tests, through
// runnertest's fake: ioreg saying whether it's locked.
package screentest

import (
	"fmt"
	"os"

	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/screen"
)

// Shows scripts fake's ioreg as the Mac's registry, the screen of the
// session kit's user has at the Mac locked or not, as macOS says it.
func Shows(fake *runnertest.Fake, locked bool) {
	q := screen.Query()
	fake.On(q.Name, q.Args...).Prints(Root(os.Getuid(), locked))
}

// Root is the registry's root as ioreg prints it, its one console user's
// session that of the user uid, its screen locked or not.
func Root(uid int, locked bool) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>IOConsoleUsers</key>
	<array>
		<dict>
			<key>CGSSessionScreenIsLocked</key>
			<%t/>
			<key>kCGSSessionOnConsoleKey</key>
			<true/>
			<key>kCGSSessionUserIDKey</key>
			<integer>%d</integer>
			<key>kCGSSessionUserNameKey</key>
			<string>someone</string>
		</dict>
	</array>
	<key>IOKitBuildVersion</key>
	<string>Darwin Kernel Version 25.0.0</string>
</dict>
</plist>
`, locked, uid)
}
