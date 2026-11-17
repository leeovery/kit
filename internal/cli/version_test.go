package cli_test

import "testing"

func TestVersion(t *testing.T) {
	out, errOut, status := newWorld(t, nil).run(t, "--version")
	if out != "kit version 0.1.0\n" || errOut != "" || status != 0 {
		t.Errorf("kit --version printed %q, %q, exit %d; want the version", out, errOut, status)
	}
	out, errOut, status = newWorld(t, nil).run(t, "--version", "--json")
	if out != "{\n  \"schema\": 1,\n  \"version\": \"0.1.0\"\n}\n" || errOut != "" || status != 0 {
		t.Errorf("kit --version --json printed %q, %q, exit %d; want the version as a document", out, errOut, status)
	}
}

func TestAnUnknownCommandFails(t *testing.T) {
	_, errOut, status := newWorld(t, nil).run(t, "frobnicate")
	if status != 2 || errOut == "" {
		t.Errorf("kit frobnicate printed %q, exit %d; want an error, exit 2", errOut, status)
	}
}
