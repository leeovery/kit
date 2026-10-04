package testguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDialGuardAllows(t *testing.T) {
	root := t.TempDir()
	tmp := filepath.Join(root, "tmp")
	// elsewhere stands for a socket outside the temporary directory, such as
	// the SSH agent's; link is a link to it from inside, and linkDir one to
	// its directory.
	elsewhere := filepath.Join(root, "elsewhere", "agent.sock")
	link := filepath.Join(tmp, "link.sock")
	linkDir := filepath.Join(tmp, "agent")
	for _, dir := range []string{tmp, filepath.Dir(elsewhere)} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(elsewhere, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for path, target := range map[string]string{link: elsewhere, linkDir: filepath.Dir(elsewhere)} {
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	guard := newDialGuard(tmp)
	tests := []struct {
		network string
		addr    string
		want    bool
	}{
		{network: "tcp", addr: "127.0.0.1:8080", want: true},
		{network: "tcp", addr: "127.8.9.10:80", want: true},
		{network: "tcp4", addr: "127.0.0.1:0", want: true},
		{network: "tcp", addr: "[::1]:443", want: true},
		{network: "tcp6", addr: "[::1%lo0]:443", want: true},
		{network: "tcp", addr: "localhost:8080", want: true},
		{network: "tcp", addr: "LocalHost:8080", want: true},
		{network: "unix", addr: filepath.Join(tmp, "kit", "test.sock"), want: true},
		{network: "unixgram", addr: filepath.Join(tmp, "log.sock"), want: true},
		{network: "tcp", addr: "192.0.2.1:443", want: false},
		{network: "tcp", addr: "api.github.com:443", want: false},
		{network: "tcp", addr: "localhost.example.com:80", want: false},
		{network: "tcp", addr: "[2001:db8::1]:443", want: false},
		{network: "tcp", addr: "0.0.0.0:80", want: false},
		{network: "udp", addr: "192.0.2.1:53", want: false},
		{network: "tcp", addr: "127.0.0.1", want: false},
		{network: "unix", addr: "/Users/someone/.ssh/agent.sock", want: false},
		{network: "unix", addr: elsewhere, want: false},
		{network: "unix", addr: link, want: false},
		{network: "unix", addr: filepath.Join(linkDir, "agent.sock"), want: false},
		{network: "unix", addr: linkDir + "/../missing.sock", want: false},
		{network: "unix", addr: tmp + "/../elsewhere/agent.sock", want: false},
		{network: "unix", addr: tmp + "x/agent.sock", want: false},
		{network: "unixpacket", addr: "agent.sock", want: false},
	}
	for _, tt := range tests {
		if got := guard.allows(tt.network, tt.addr); got != tt.want {
			t.Errorf("allows(%q, %q) = %v, want %v", tt.network, tt.addr, got, tt.want)
		}
	}
}

func TestDialGuardBlocksDialsOffTheMachine(t *testing.T) {
	var dialled []string
	tmp := t.TempDir()
	guard := newDialGuard(tmp)
	dial := guard.wrap(func(_ context.Context, _, addr string) (net.Conn, error) {
		dialled = append(dialled, addr)
		return nil, errors.New("a stand-in never connects")
	})

	_, err := dial(t.Context(), "tcp", "192.0.2.1:443")
	if want := "testguard: blocked dial to 192.0.2.1:443"; err == nil || err.Error() != want {
		t.Errorf("dial to 192.0.2.1:443: error = %v, want %q", err, want)
	}
	_, _ = dial(t.Context(), "tcp", "api.github.com:443")
	_, _ = dial(t.Context(), "tcp", "192.0.2.1:443")
	_, _ = dial(t.Context(), "tcp", "127.0.0.1:8080")
	_, _ = dial(t.Context(), "unix", "/nonexistent/agent.sock")
	_, _ = dial(t.Context(), "unix", filepath.Join(tmp, "test.sock"))

	if want := []string{"127.0.0.1:8080", filepath.Join(tmp, "test.sock")}; !slices.Equal(dialled, want) {
		t.Errorf("dials passed on = %q, want %q alone", dialled, want)
	}
	want := []string{
		"blocked dial to /nonexistent/agent.sock",
		"blocked dial to 192.0.2.1:443 (2 times)",
		"blocked dial to api.github.com:443",
	}
	if got := guard.escapes(); !slices.Equal(got, want) {
		t.Errorf("escapes() = %q, want %q", got, want)
	}
}

func TestDialGuardHoldsForTransportsClonedAfter(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	// A directory of the test's own short enough to hold a unix socket:
	// t.TempDir's can be too long on macOS.
	tmp, err := os.MkdirTemp("", "kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	socket, err := net.Listen("unix", filepath.Join(tmp, "test.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	guard := newDialGuard(tmp)
	transport := &http.Transport{}
	if err := guard.install(transport); err != nil {
		t.Fatalf("install() error = %v", err)
	}

	for name, tr := range map[string]*http.Transport{"the transport": transport, "its clone": transport.Clone()} {
		for _, blocked := range [][2]string{{"tcp", "192.0.2.1:443"}, {"unix", "/nonexistent/agent.sock"}} {
			if _, err := tr.DialContext(t.Context(), blocked[0], blocked[1]); err == nil || !strings.Contains(err.Error(), "blocked dial") {
				t.Errorf("%s dialled %s %s: error = %v, want it blocked", name, blocked[0], blocked[1], err)
			}
		}
		for _, allowed := range []net.Addr{ln.Addr(), socket.Addr()} {
			conn, err := tr.DialContext(t.Context(), allowed.Network(), allowed.String())
			if err != nil {
				t.Errorf("%s dialled %s %s: error = %v, want a connection", name, allowed.Network(), allowed, err)
				continue
			}
			_ = conn.Close()
		}
	}
}

func TestDialGuardNeedsAnHTTPTransport(t *testing.T) {
	if err := newDialGuard(t.TempDir()).install(roundTripper(nil)); err == nil {
		t.Error("install() of a RoundTripper that isn't an *http.Transport: no error, want one")
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
