package brew_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/kind/brew"
	"github.com/leeovery/kit/internal/runner/runnertest"
)

// appInfo is what brew info says of owner/tap/app, a cask downloading from
// GitHub's API.
const appInfo = `{"formulae": [], "casks": [{"token": "app", "full_token": "owner/tap/app", "url": "https://api.github.com/repos/owner/app/releases/assets/1"}]}`

// appDownload is where owner/tap/app downloads from.
const appDownload = "https://api.github.com/repos/owner/app/releases/assets/1"

// asking is the request for a download's first byte, asked for without a
// token or ~/.curlrc, its answer and where it ended printed.
func asking(url string) []string {
	return []string{"-q", "-sS", "-o", "/dev/null", "-w", "%{http_code} %{url_effective}", "-L", "-r", "0-0", "--max-time", "10", url}
}

// needsSecret is what k, Homebrew's formulae or casks, run with fake, says
// name needs, and what it said it was doing meanwhile.
func needsSecret(t *testing.T, fake *runnertest.Fake, k func(*brew.Homebrew) kind.Kind, name string) (kind.Need, []string, error) {
	t.Helper()
	p, ok := k(brew.New(fake)).(kind.Private)
	if !ok {
		t.Fatal("the kind doesn't say what secret its things need")
	}
	var mu sync.Mutex
	var said []string
	ctx := event.WithSaying(context.Background(), func(d event.Doing) {
		mu.Lock()
		defer mu.Unlock()
		said = append(said, d.Says)
	})
	need, err := p.NeedsSecret(ctx, name)
	return need, said, err
}

// A tap's download needs Homebrew's GitHub token when GitHub answers it,
// asked for without one, as missing or unauthorised, redirects followed;
// it's open when it answers at all, cut off at the time limit or not. Any
// other answer, or none, can't say: a token opens GitHub alone, and GitHub
// refuses (403) once a Mac's asked too often. The step says what it's
// doing, and the one request carries nothing set.
func TestATapsPrivateDownloadNeedsTheToken(t *testing.T) {
	tests := []struct {
		name    string
		answer  string
		exit    int
		stderr  string
		want    string
		wantErr string
	}{
		{name: "missing, as GitHub's API says a private repository's is", answer: "404 " + appDownload, want: "HOMEBREW_GITHUB_API_TOKEN"},
		{name: "unauthorised, by GitHub", answer: "401 https://github.com/owner/app/releases/download/v1/app.zip", want: "HOMEBREW_GITHUB_API_TOKEN"},
		{name: "missing, from GitHub's release storage", answer: "404 https://release-assets.githubusercontent.com/app.zip", want: "HOMEBREW_GITHUB_API_TOKEN"},
		{name: "found", answer: "200 https://release-assets.githubusercontent.com/app.zip"},
		{name: "its first byte", answer: "206 https://release-assets.githubusercontent.com/app.zip"},
		{name: "cut off once it answered", answer: "200 https://example.com/app.zip", exit: 28, stderr: "curl: (28) Operation timed out\n"},
		{name: "missing, elsewhere", answer: "404 https://example.com/app.zip", wantErr: "its download answered 404, from example.com"},
		{name: "refused by GitHub, as when it's asked too often", answer: "403 " + appDownload, wantErr: "its download answered 403, from api.github.com"},
		{name: "too many requests", answer: "429 " + appDownload, wantErr: "its download answered 429, from api.github.com"},
		{name: "a server's error", answer: "500 " + appDownload, wantErr: "its download answered 500, from api.github.com"},
		{name: "redirected, the redirect followed no further", answer: "302 https://example.com/app.zip", wantErr: "its download answered 302, from example.com"},
		{name: "redirected somewhere unreachable", answer: "302 https://example.com/app.zip", exit: 7, stderr: "curl: (7) Failed to connect to example.com port 443\n", wantErr: "its download didn't answer: "},
		{name: "no network", answer: "000 " + appDownload, exit: 6, stderr: "curl: (6) Could not resolve host: api.github.com\n", wantErr: "its download didn't answer: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			fake.On("brew", "tap").Prints("owner/tap\n")
			fake.On("brew", "info", "--json=v2", "--cask", "owner/tap/app").Prints(appInfo)
			fake.On("curl", asking(appDownload)...).Prints(tt.answer).Exits(tt.exit).PrintsToStderr(tt.stderr)

			need, said, err := needsSecret(t, fake, (*brew.Homebrew).Casks, "owner/tap/app")
			switch {
			case tt.wantErr != "" && (err == nil || !strings.HasPrefix(err.Error(), tt.wantErr)):
				t.Errorf("NeedsSecret() error = %v, want one starting %q", err, tt.wantErr)
			case tt.wantErr == "" && err != nil:
				t.Errorf("NeedsSecret() error = %v", err)
			}
			if need.Secret != tt.want || need.Undo != nil {
				t.Errorf("NeedsSecret() = %+v, want %q, nothing to undo", need, tt.want)
			}
			if tt.want != "" && need.Why != "its download is private" {
				t.Errorf("why = %q, want its download is private", need.Why)
			}
			if !slices.Equal(said, []string{"checking whether its download is private"}) {
				t.Errorf("said %q, want what it was checking", said)
			}
			for _, c := range fake.Commands() {
				if c.Name == "curl" && len(c.Env) > 0 {
					t.Errorf("curl was run with %q, want nothing set", c.Env)
				}
			}
		})
	}
}

// A formula installs from its bottle, so its bottle's checked beside its
// source, either private holding it; one that can't be checked can't say.
func TestAFormulasBottleIsCheckedToo(t *testing.T) {
	const source, bottle = "https://github.com/owner/tool/archive/v1.tar.gz", "https://github.com/owner/homebrew-tap/releases/download/tool-1/tool-1.arm64_sequoia.bottle.tar.gz"
	info := `{"formulae": [{"name": "tool", "full_name": "owner/tap/tool", "urls": {"stable": {"url": "` + source + `"}},
		"bottle": {"stable": {"root_url": "https://github.com/owner/homebrew-tap/releases/download/tool-1", "files": {
			"sonoma": {"url": "https://github.com/owner/homebrew-tap/releases/download/tool-1/tool-1.sonoma.bottle.tar.gz"},
			"arm64_sequoia": {"url": "` + bottle + `"}}}}}], "casks": []}`
	tests := []struct {
		name, source, bottle, want, wantErr string
	}{
		{name: "the bottle private", source: "200 https://codeload.github.com/owner/tool", bottle: "404 https://github.com/x", want: "HOMEBREW_GITHUB_API_TOKEN"},
		{name: "the source private", source: "404 https://github.com/x", bottle: "200 https://release-assets.githubusercontent.com/x", want: "HOMEBREW_GITHUB_API_TOKEN"},
		{name: "both open", source: "200 https://codeload.github.com/owner/tool", bottle: "200 https://release-assets.githubusercontent.com/x"},
		{name: "the bottle can't say", source: "200 https://codeload.github.com/owner/tool", bottle: "503 https://github.com/x", wantErr: "its download answered 503, from github.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			fake.On("brew", "tap").Prints("owner/tap\n")
			fake.On("brew", "info", "--json=v2", "--formula", "owner/tap/tool").Prints(info)
			fake.On("curl", asking(source)...).Prints(tt.source)
			fake.On("curl", asking(bottle)...).Prints(tt.bottle)

			need, _, err := needsSecret(t, fake, (*brew.Homebrew).Formulae, "owner/tap/tool")
			if need.Secret != tt.want || tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr) {
				t.Errorf("NeedsSecret() = %q, %v; want %q, error %q", need.Secret, err, tt.want, tt.wantErr)
			}
		})
	}
}

// A tap's thing known by its name alone, its tap added, is checked by the
// full name brew info gives it.
func TestATapsThingByItsShortNameIsChecked(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "info", "--json=v2", "--cask", "app").Prints(appInfo)
	fake.On("curl", asking(appDownload)...).Prints("404 " + appDownload)

	if need, _, err := needsSecret(t, fake, (*brew.Homebrew).Casks, "app"); err != nil || need.Secret != "HOMEBREW_GITHUB_API_TOKEN" {
		t.Errorf("NeedsSecret() = %+v, %v; want HOMEBREW_GITHUB_API_TOKEN", need, err)
	}
}

// Homebrew's own need no secret, nothing asked of their downloads, nor
// said: by its name alone or in full.
func TestHomebrewsOwnNeedNoSecret(t *testing.T) {
	jq := `{"formulae": [{"name": "jq", "full_name": "jq", "urls": {"stable": {"url": "https://example.com/jq.tar.gz"}}}], "casks": []}`
	for _, tt := range []struct {
		k           func(*brew.Homebrew) kind.Kind
		which, name string
		info        string
	}{
		{(*brew.Homebrew).Formulae, "--formula", "jq", jq},
		{(*brew.Homebrew).Formulae, "--formula", "homebrew/core/jq", jq},
		{(*brew.Homebrew).Casks, "--cask", "zoom", `{"formulae": [], "casks": [{"token": "zoom", "full_token": "zoom", "url": "https://example.com/zoom.pkg"}]}`},
	} {
		fake := runnertest.New(t)
		fake.On("brew", "info", "--json=v2", tt.which, tt.name).Prints(tt.info)
		if need, said, err := needsSecret(t, fake, tt.k, tt.name); need.Secret != "" || need.Undo != nil || said != nil || err != nil {
			t.Errorf("NeedsSecret(%s) = %+v, saying %q, %v; want none", tt.name, need, said, err)
		}
	}
}

// A tap not added yet is added to look in, as brew info can't describe
// what's in it, and taken out again by the Need's Undo.
func TestATapAddedToCheckIsUndone(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "tap").Prints("homebrew/cask\n")
	fake.On("brew", "tap", "owner/tap")
	fake.On("brew", "info", "--json=v2", "--cask", "owner/tap/app").Prints(appInfo)
	fake.On("curl", asking(appDownload)...).Prints("200 " + appDownload)
	fake.On("brew", "untap", "owner/tap")

	need, _, err := needsSecret(t, fake, (*brew.Homebrew).Casks, "owner/tap/app")
	if err != nil || need.Secret != "" || need.Undo == nil {
		t.Fatalf("NeedsSecret() = %+v, %v; want none, and the tap to undo", need, err)
	}
	calls := fake.Calls()
	if tapped, asked := slices.Index(calls, "brew tap owner/tap"), slices.Index(calls, "brew info --json=v2 --cask owner/tap/app"); tapped < 0 || asked < tapped || slices.Contains(calls, "brew untap owner/tap") {
		t.Errorf("ran %q, want the tap added before brew info's asked, and left", calls)
	}
	if err := need.Undo(context.Background()); err != nil || !slices.Contains(fake.Calls(), "brew untap owner/tap") {
		t.Errorf("Undo() = %v, running %q; want the tap taken out", err, fake.Calls())
	}
}

// brew info naming no download can't say.
func TestNoDownloadCantBeChecked(t *testing.T) {
	fake := runnertest.New(t)
	fake.On("brew", "tap").Prints("owner/tap\n")
	fake.On("brew", "info", "--json=v2", "--cask", "owner/tap/app").Prints(`{"formulae": [], "casks": [{"token": "app", "full_token": "owner/tap/app"}]}`)
	if need, _, err := needsSecret(t, fake, (*brew.Homebrew).Casks, "owner/tap/app"); need.Secret != "" || err == nil || err.Error() != "brew info names no download of owner/tap/app" {
		t.Errorf("NeedsSecret() = %+v, %v; want brew info naming no download", need, err)
	}
}
