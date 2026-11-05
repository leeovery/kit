package brew_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// appInfo is what brew info says of owner/tap/app, a cask downloading from
// example.com.
const appInfo = `{"formulae": [], "casks": [{"token": "app", "full_token": "owner/tap/app", "url": "https://example.com/app.zip"}]}`

// asking is the request for a download's first byte, asked for without a
// token, its answer printed.
func asking(url string) []string {
	return []string{"-sS", "-o", "/dev/null", "-w", "%{http_code}", "-L", "-r", "0-0", "--max-time", "30", url}
}

// needsSecret is what k, Homebrew's formulae or casks, run with fake, says
// name needs.
func needsSecret(t *testing.T, fake *runnertest.Fake, k func(*brew.Homebrew) kind.Kind, name string) (secret, why string, err error) {
	t.Helper()
	p, ok := k(brew.New(fake)).(kind.Private)
	if !ok {
		t.Fatal("the kind doesn't say what secret its things need")
	}
	return p.NeedsSecret(context.Background(), name)
}

// A tap's download that answers, without a token, as a private one does
// needs Homebrew's GitHub token; one that answers needs nothing, as does
// one cut off once it's answered. The one request carries no token.
func TestATapsPrivateDownloadNeedsTheToken(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		exit   int
		want   string
	}{
		{name: "missing, as GitHub says a private repository's is", answer: "404", want: "HOMEBREW_GITHUB_API_TOKEN"},
		{name: "unauthorised", answer: "401", want: "HOMEBREW_GITHUB_API_TOKEN"},
		{name: "forbidden", answer: "403", want: "HOMEBREW_GITHUB_API_TOKEN"},
		{name: "found", answer: "200"},
		{name: "its first byte", answer: "206"},
		{name: "cut off once it answered", answer: "200", exit: 28},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			fake.On("brew", "tap").Prints("owner/tap\n")
			fake.On("brew", "info", "--json=v2", "--cask", "owner/tap/app").Prints(appInfo)
			fake.On("curl", asking("https://example.com/app.zip")...).Prints(tt.answer).Exits(tt.exit)

			secret, why, err := needsSecret(t, fake, (*brew.Homebrew).Casks, "owner/tap/app")
			if err != nil || secret != tt.want {
				t.Fatalf("NeedsSecret() = %q, %q, %v; want %q", secret, why, err, tt.want)
			}
			if tt.want != "" && why != "its download is private" {
				t.Errorf("why = %q, want its download is private", why)
			}
			for _, c := range fake.Commands() {
				if c.Name == "curl" && len(c.Env) > 0 {
					t.Errorf("curl was run with %q, want nothing set", c.Env)
				}
			}
		})
	}
}

// A formula downloads from its stable release.
func TestAFormulasDownloadIsItsStableRelease(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "tap").Prints("owner/tap\n")
	fake.On("brew", "info", "--json=v2", "--formula", "owner/tap/tool").Prints(`{"formulae": [{"name": "tool", "full_name": "owner/tap/tool", "urls": {"stable": {"url": "https://example.com/tool.tar.gz"}}}], "casks": []}`)
	fake.On("curl", asking("https://example.com/tool.tar.gz")...).Prints("404")

	if secret, _, err := needsSecret(t, fake, (*brew.Homebrew).Formulae, "owner/tap/tool"); err != nil || secret != "HOMEBREW_GITHUB_API_TOKEN" {
		t.Errorf("NeedsSecret() = %q, %v; want HOMEBREW_GITHUB_API_TOKEN", secret, err)
	}
}

// Homebrew's own need no secret, nothing asked: a name without its tap is
// one of them.
func TestHomebrewsOwnNeedNoSecret(t *testing.T) {
	for _, tt := range []struct {
		k    func(*brew.Homebrew) kind.Kind
		name string
	}{
		{(*brew.Homebrew).Formulae, "jq"},
		{(*brew.Homebrew).Formulae, "homebrew/core/jq"},
		{(*brew.Homebrew).Casks, "zoom"},
		{(*brew.Homebrew).Casks, "homebrew/cask/zoom"},
	} {
		if secret, why, err := needsSecret(t, runnertest.New(t), tt.k, tt.name); secret != "" || why != "" || err != nil {
			t.Errorf("NeedsSecret(%s) = %q, %q, %v; want none", tt.name, secret, why, err)
		}
	}
}

// A tap not added yet is added first, as brew info can't describe what's
// in it.
func TestATapNotAddedIsTappedToCheck(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "tap").Prints("homebrew/cask\n")
	fake.On("brew", "tap", "owner/tap")
	fake.On("brew", "info", "--json=v2", "--cask", "owner/tap/app").Prints(appInfo)
	fake.On("curl", asking("https://example.com/app.zip")...).Prints("200")

	if secret, _, err := needsSecret(t, fake, (*brew.Homebrew).Casks, "owner/tap/app"); err != nil || secret != "" {
		t.Fatalf("NeedsSecret() = %q, %v; want none", secret, err)
	}
	calls := fake.Calls()
	if tapped, asked := slices.Index(calls, "brew tap owner/tap"), slices.Index(calls, "brew info --json=v2 --cask owner/tap/app"); tapped < 0 || asked < tapped {
		t.Errorf("ran %q, want the tap added before brew info's asked", calls)
	}
}

// A download that can't be checked says why: one that doesn't answer, as
// without the network, or answers otherwise, and brew info naming none.
func TestADownloadThatCantBeCheckedSaysWhy(t *testing.T) {
	tests := []struct {
		name   string
		script func(*runnertest.Fake)
		want   string
	}{
		{
			name: "no network",
			script: func(f *runnertest.Fake) {
				f.On("curl", asking("https://example.com/app.zip")...).Prints("000").Exits(6).PrintsToStderr("curl: (6) Could not resolve host: example.com\n")
			},
			want: "its download didn't answer: ",
		},
		{
			name:   "answered otherwise",
			script: func(f *runnertest.Fake) { f.On("curl", asking("https://example.com/app.zip")...).Prints("500") },
			want:   "its download answered 500",
		},
		{
			name: "no download",
			script: func(f *runnertest.Fake) {
				f.On("brew", "info", "--json=v2", "--cask", "owner/tap/app").Prints(`{"formulae": [], "casks": [{"token": "app", "full_token": "owner/tap/app"}]}`)
			},
			want: "brew info names no download of owner/tap/app",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			fake.On("brew", "tap").Prints("owner/tap\n")
			fake.On("brew", "info", "--json=v2", "--cask", "owner/tap/app").Prints(appInfo)
			tt.script(fake)

			secret, _, err := needsSecret(t, fake, (*brew.Homebrew).Casks, "owner/tap/app")
			if err == nil || secret != "" || !strings.HasPrefix(err.Error(), tt.want) {
				t.Errorf("NeedsSecret() = %q, %v; want an error starting %q", secret, err, tt.want)
			}
		})
	}
}
