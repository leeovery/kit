package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/kind/login"
)

// Adopting a login item declares its app's bundle id, with the app's name
// as the note, then why, when given.
func TestReconcileAdoptsALoginItemWithItsAppsName(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, filepath.Join(".config", "kit", "login.laptop"), "")
	read := `[{"name":"Dropbox","path":"/Applications/Dropbox.app","id":"com.getdropbox.dropbox"}]`
	w.fake.On("osascript", "-l", "JavaScript", "-e", login.ReadScript).Prints(read)
	w.expectSync([]string{"login.laptop"}, "kit reconcile (laptop): adopt login:com.getdropbox.dropbox: syncs the files")

	out, errOut, code := w.run(t, "reconcile", "login:com.getdropbox.dropbox", "--adopt", "--note", "syncs the files")
	if code != 0 {
		t.Fatalf("kit reconcile printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, filepath.Join(".config", "kit", "login.laptop")); got != "# To be sorted\ncom.getdropbox.dropbox   # Dropbox: syncs the files\n" {
		t.Errorf("login.laptop = %q", got)
	}
	if !strings.Contains(out, "declared in login.laptop") {
		t.Errorf("kit reconcile printed\n%s", out)
	}
}
