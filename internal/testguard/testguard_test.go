package testguard_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// These tests run this test binary again as a child, through the guard as
// every run is, doing what the test names: the guard around the child must
// catch it, and the one around this process stays clean.
const (
	// childDoes names what TestInChild does in the child.
	childDoes = "TESTGUARD_CHILD_DOES"
	// childHome is the child's real home, which the guard moves HOME away from.
	childHome = "TESTGUARD_CHILD_HOME"
)

// seeded are variables a child starts with that the guard must clear.
var seeded = []string{
	"KIT_CONFIG=/nonexistent/kit-config",
	"KIT_MACHINE=laptop",
	"HOMEBREW_GITHUB_API_TOKEN=test-token-brew",
	"OP_SESSION_example=test-session",
	"OP_SERVICE_ACCOUNT_TOKEN=test-token-op",
	"GH_TOKEN=test-token-gh",
	"GITHUB_TOKEN=test-token-github",
	"GIT_DIR=/nonexistent/git",
	"SSH_AUTH_SOCK=/nonexistent/agent.sock",
	"CLAUDE_CODE_OAUTH_TOKEN=test-token-oauth",
	"ANTHROPIC_API_KEY=test-key",
	"TMUX=/nonexistent/tmux,1,0",
	"TMUX_PANE=%1",
	"HTTPS_PROXY=http://127.0.0.1:9",
	"http_proxy=http://127.0.0.1:9",
	"ALL_PROXY=socks5://127.0.0.1:9",
}

// outsideSocket is a socket outside the temporary directory, as the SSH
// agent's is: nothing's there, but the guard must block the dial all the
// same.
const outsideSocket = "/nonexistent/ssh/agent.sock"

func TestEscapesFailTheRunThoughEveryTestPasses(t *testing.T) {
	tests := []struct {
		does string
		want string
	}{
		{does: "run-a-stub", want: "ran brew list --formula"},
		{does: "dial-off-the-machine", want: "blocked dial to 192.0.2.1:80"},
		{does: "dial-a-socket-outside-the-temporary-directory", want: "blocked dial to " + outsideSocket},
	}
	for _, tt := range tests {
		t.Run(tt.does, func(t *testing.T) {
			out, err := runChild(t, tt.does, t.TempDir())
			if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 {
				t.Errorf("child: error = %v, want exit status 1", err)
			}
			passed := slices.Contains(strings.Split(out, "\n"), "PASS")
			if !passed || !strings.Contains(out, "testguard: the tests reached past their isolation") || !strings.Contains(out, tt.want) {
				t.Errorf("child printed\n%s\nwant its test to pass, and the guard to fail the run as %q", out, tt.want)
			}
		})
	}
}

func TestWhatTheGuardLetsBePasses(t *testing.T) {
	out, err := runChild(t, "stay-in-isolation", t.TempDir())
	if err != nil || strings.Contains(out, "testguard") {
		t.Errorf("child: error = %v, printing\n%s\nwant it to pass, with nothing from the guard", err, out)
	}
}

func TestTheEnvironmentIsIsolated(t *testing.T) {
	out, err := runChild(t, "check-the-environment", t.TempDir(), append(seeded, "TESTGUARD_KEPT=kept")...)
	if err != nil {
		t.Errorf("child: error = %v, printing\n%s", err, out)
	}
}

// TestInChild does what childDoes names, when a test here runs it in a child.
func TestInChild(t *testing.T) {
	does := os.Getenv(childDoes)
	if does == "" {
		t.Skip("runs only in a child")
	}
	switch does {
	case "run-a-stub":
		err := exec.Command("brew", "list", "--formula").Run()
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 {
			t.Errorf("brew: error = %v, want the stub's exit status 1", err)
		}
	case "dial-off-the-machine":
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://192.0.2.1/")
		if err == nil {
			_ = resp.Body.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "testguard: blocked dial to 192.0.2.1:80") {
			t.Errorf("GET http://192.0.2.1/: error = %v, want the dial blocked", err)
		}
	case "dial-a-socket-outside-the-temporary-directory":
		resp, err := overSocket(outsideSocket).Get("http://agent/")
		if err == nil {
			_ = resp.Body.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "testguard: blocked dial to "+outsideSocket) {
			t.Errorf("GET / over %s: error = %v, want the dial blocked", outsideSocket, err)
		}
	case "stay-in-isolation":
		stayInIsolation(t)
	case "check-the-environment":
		checkEnvironment(t, os.Getenv(childHome))
	default:
		t.Fatalf("%s=%s names nothing to do", childDoes, does)
	}
}

// stayInIsolation does what the guard allows: it dials loopback and a unix
// socket in the temporary directory.
func stayInIsolation(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	srv := httptest.NewServer(ok)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET %s: %v", srv.URL, err)
	}
	_ = resp.Body.Close()
	socket := serveOnASocket(t, ok)
	resp, err = overSocket(socket).Get("http://kit/")
	if err != nil {
		t.Fatalf("GET / over %s: %v", socket, err)
	}
	_ = resp.Body.Close()
}

// serveOnASocket serves h on a unix socket in the temporary directory until
// the test ends, and returns the socket's path.
func serveOnASocket(t *testing.T, h http.Handler) string {
	t.Helper()
	// t.TempDir's can be too long for a unix socket on macOS.
	dir, err := os.MkdirTemp("", "kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "test.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return path
}

// overSocket returns a client whose every request goes to the unix socket at
// path, over a transport cloned from http.DefaultTransport, which the guard
// guards.
func overSocket(path string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dial(ctx, "unix", path)
	}
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

// checkEnvironment checks that the guard cleared the seeded variables, kept
// the rest, and moved HOME, XDG's base directories and PATH off the real ones.
// It names a variable that's wrong, never its value.
func checkEnvironment(t *testing.T, realHome string) {
	for _, variable := range seeded {
		name, _, _ := strings.Cut(variable, "=")
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("%s is set, want it cleared", name)
		}
	}
	if os.Getenv("TESTGUARD_KEPT") != "kept" {
		t.Error("TESTGUARD_KEPT isn't as the child started with it, want it kept")
	}

	home := os.Getenv("HOME")
	if info, err := os.Stat(home); home == realHome || err != nil || !info.IsDir() {
		t.Errorf("HOME is the real home or no directory (%v), want a throwaway one", err)
	}
	for name, want := range map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
	} {
		if os.Getenv(name) != want {
			t.Errorf("%s isn't in the throwaway home", name)
		}
	}

	path := os.Getenv("PATH")
	entries, err := os.ReadDir(path)
	if err != nil || strings.Contains(path, string(os.PathListSeparator)) {
		t.Fatalf("PATH isn't one directory (%v), want the stubs' alone", err)
	}
	var stubs []string
	for _, e := range entries {
		stubs = append(stubs, e.Name())
	}
	want := []string{"agntc", "brew", "bun", "claude", "codesign", "composer", "curl", "defaults", "diskutil", "dns-sd", "gh", "git", "go", "ioreg", "killall", "launchctl", "mas", "mdfind", "mdutil", "mount", "mount_apfs", "npm", "op", "open", "osascript", "pmset", "pnpm", "rsync", "ssh", "sudo", "swiftc", "sysctl", "systemsetup", "tmutil", "tmux", "umount", "uv", "yarn"}
	if !slices.Equal(stubs, want) {
		t.Errorf("PATH holds %q, want the stubs %q alone", stubs, want)
	}
}

// runChild runs TestInChild in a child of this test binary, doing does, with
// home as its home and env added to the environment, and returns what it
// printed.
func runChild(t *testing.T, does, home string, env ...string) (string, error) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestInChild$", "-test.timeout=1m")
	// Under the race detector, a child that exits 0 would first sleep for its
	// default atexit_sleep_ms: measured, a second a child.
	race := strings.TrimSpace(os.Getenv("GORACE") + " atexit_sleep_ms=0")
	cmd.Env = slices.Concat(os.Environ(), env, []string{"HOME=" + home, "GORACE=" + race, childDoes + "=" + does, childHome + "=" + home})
	out, err := cmd.CombinedOutput()
	return string(out), err
}
