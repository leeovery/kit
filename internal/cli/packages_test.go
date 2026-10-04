package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// A package declared with a constraint is installed with it, declared as
// typed, and matched on its name from then on.
func TestAddAComposerPackageWithAConstraint(t *testing.T) {
	w := laptopWorld(t)
	show := []string{"global", "show", "--direct", "--format=json"}
	w.fake.On("composer", show...).Prints(`{"installed": []}`).
		Then().Prints(`{"installed": [{"name": "laravel/valet"}]}`)
	w.fake.On("composer", "global", "require", "--no-interaction", "laravel/valet:^4.0")
	w.expectSync([]string{"composer.laptop"}, "kit add composer laravel/valet:^4.0 (laptop): Valet 4")

	out, errOut, code := w.run(t, "add", "composer", "laravel/valet:^4.0", "--note", "Valet 4")
	if code != 0 || !strings.Contains(out, "laravel/valet:^4.0 ok installed; declared in composer.laptop (To be sorted)\n") {
		t.Fatalf("kit add printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, filepath.Join(".config", "kit", "composer.laptop")); got != "# To be sorted\nlaravel/valet:^4.0   # Valet 4\n" {
		t.Errorf("composer.laptop = %q", got)
	}
	out, _, _ = w.run(t, "status", "composer")
	if !strings.Contains(out, "composer ok 1 declared, all installed\n") {
		t.Errorf("kit status composer printed\n%s\nwant valet matched by its name", out)
	}
}

// An npm package found installed and declared nowhere is drift, adopted by
// its id.
func TestReconcileAdoptsAnNPMPackage(t *testing.T) {
	w := laptopWorld(t)
	w.write(t, filepath.Join(".config", "kit", "npm.laptop"), "intelephense\n")
	w.fake.On("npm", "ls", "--global", "--depth=0", "--json").Prints(`{"dependencies": {"intelephense": {}, "docx": {}}}`)
	w.expectSync([]string{"npm.laptop"}, "kit reconcile (laptop): adopt npm:docx: for the docs skill")

	out, errOut, code := w.run(t, "reconcile", "npm:docx", "--adopt", "--note", "for the docs skill")
	if code != 0 {
		t.Fatalf("kit reconcile printed\n%s%s exit %d", out, errOut, code)
	}
	if got := w.read(t, filepath.Join(".config", "kit", "npm.laptop")); got != "intelephense\n\n# To be sorted\ndocx   # for the docs skill\n" {
		t.Errorf("npm.laptop = %q", got)
	}
}
