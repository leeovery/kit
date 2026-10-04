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
	w.expectSync([]string{"laptop"}, "kit reconcile (laptop): adopt login:com.getdropbox.dropbox: syncs the files")

	out, errOut, code := w.run(t, "reconcile", "login:com.getdropbox.dropbox", "--adopt", "--note", "syncs the files")
	if code != 0 {
		t.Fatalf("kit reconcile printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.readSection(t, "laptop", "macos login items"); got != "# To be sorted\ncom.getdropbox.dropbox   # Dropbox: syncs the files\n" {
		t.Errorf("laptop = %q", got)
	}
	if !strings.Contains(out, "declared in laptop") {
		t.Errorf("kit reconcile printed\n%s", out)
	}
}
