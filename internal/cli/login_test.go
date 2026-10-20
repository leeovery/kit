package cli_test

import (
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/kind/login"
)

// Adopting a login item declares its app's bundle id, with the app's name
// as the note, then why, when given.
func TestReconcileAdoptsALoginItemWithItsAppsName(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "macos login items", "")
	read := `[{"name":"Dropbox","path":"/Applications/Dropbox.app","id":"com.getdropbox.dropbox"}]`
	w.fake.On("osascript", "-l", "JavaScript", "-e", login.ReadScript).Prints(read)
	w.expectSync([]string{"laptop/declarations"}, "kit reconcile (laptop): adopt login-item:com.getdropbox.dropbox: syncs the files")

	out, errOut, code := w.run(t, "reconcile", "login-item:com.getdropbox.dropbox", "--adopt", "--note", "syncs the files")
	if code != 0 {
		t.Fatalf("kit reconcile printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "macos login items"); got != "com.getdropbox.dropbox   # Dropbox: syncs the files\n" {
		t.Errorf("laptop = %q", got)
	}
	if !strings.Contains(out, "declared in laptop") {
		t.Errorf("kit reconcile printed\n%s", out)
	}
}

// Added for every Mac, a login item a Mac declares keeps that Mac's note:
// the app's name is only the note when none is given or moved.
func TestAddALoginItemSharedKeepsTheMacsNote(t *testing.T) {
	w := laptopWorld(t)
	w.writeSection(t, "laptop", "macos login items", "com.getdropbox.dropbox   # syncs the files\n")
	read := `[{"name":"Dropbox","path":"/Applications/Dropbox.app","id":"com.getdropbox.dropbox"}]`
	w.fake.On("osascript", "-l", "JavaScript", "-e", login.ReadScript).Prints(read)
	w.fake.On("osascript", "-l", "JavaScript", "-e", login.IdentifyScript, "com.getdropbox.dropbox").Prints(`[{"id":"com.getdropbox.dropbox","name":"Dropbox","path":"/Applications/Dropbox.app"}]`)
	w.expectSync([]string{"laptop/declarations", "shared/declarations"}, "kit login-item add com.getdropbox.dropbox (laptop)")
	out, errOut, code := w.run(t, "login-item", "add", "com.getdropbox.dropbox", "--shared")
	if code != 0 || !strings.Contains(out, "declared in shared, out of laptop") {
		t.Fatalf("kit login-item add --shared printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "shared", "macos login items"); got != "com.getdropbox.dropbox   # syncs the files\n" {
		t.Errorf("shared = %q, want laptop's note kept", got)
	}
	if got := w.readSection(t, "laptop", "macos login items"); got != "" {
		t.Errorf("laptop = %q, want it out", got)
	}
}
