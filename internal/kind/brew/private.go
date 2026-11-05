package brew

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/leeovery/kit/internal/runner"
)

// githubToken is the secret Homebrew downloads a private GitHub
// repository's release with: its own variable, which a tap's download
// reads.
const githubToken = "HOMEBREW_GITHUB_API_TOKEN"

// privateAnswers are how a download answers, asked for without a token,
// when it's private: GitHub says a private repository's is missing (404),
// and other hosts refuse it (401, 403).
var privateAnswers = []int{401, 403, 404}

// NeedsSecret is the GitHub token, for a formula of a tap other than
// Homebrew's own whose download is private (needsToken).
func (f formulae) NeedsSecret(ctx context.Context, name string) (string, string, error) {
	return f.needsToken(ctx, f.Name(), "--formula", name)
}

// NeedsSecret is the GitHub token, for a cask of a tap other than
// Homebrew's own whose download is private (needsToken).
func (c casks) NeedsSecret(ctx context.Context, name string) (string, string, error) {
	return c.needsToken(ctx, c.Name(), "--cask", name)
}

// needsToken is githubToken, and why, for name, of the kind named kindName,
// a formula or a cask as which says, when it's from a tap other than
// Homebrew's own and its download answers as a private one does (private):
// none for Homebrew's own, as a name without its tap is, a tap's being
// declared by its full name, the one its tap's added for on a new Mac. Its
// tap's added first, should it not be: brew info can't describe what's in
// a tap not added.
func (h *Homebrew) needsToken(ctx context.Context, kindName, which, name string) (secret, why string, err error) {
	if _, ok := tapOf(name); !ok {
		return "", "", nil
	}
	if err := h.ready(ctx, kindName, []string{name}); err != nil {
		return "", "", err
	}
	url, err := h.download(ctx, which, name)
	if err != nil {
		return "", "", err
	}
	if private, err := h.private(ctx, url); err != nil || !private {
		return "", "", err
	}
	return githubToken, "its download is private", nil
}

// download is where name, a formula or a cask as which says, downloads
// from, as brew info says: a formula's stable release, or a cask's.
func (h *Homebrew) download(ctx context.Context, which, name string) (string, error) {
	res, err := h.brew(ctx, "info", "--json=v2", which, name)
	if err != nil {
		return "", err
	}
	var in info
	if err := json.Unmarshal(res.Stdout, &in); err != nil {
		return "", fmt.Errorf("read brew info's answer: %w", err)
	}
	var url string
	switch {
	case len(in.Formulae) > 0:
		url = in.Formulae[0].URLs.Stable.URL
	case len(in.Casks) > 0:
		url = in.Casks[0].URL
	}
	if url == "" {
		return "", fmt.Errorf("brew info names no download of %s", name)
	}
	return url, nil
}

// private reports whether url, asked for without a token, answers as a
// private download does (privateAnswers): one request, for its first byte
// alone, redirects followed. One cut off once it's answered has answered.
// An error says it didn't answer, as without the network, or answered
// otherwise.
func (h *Homebrew) private(ctx context.Context, url string) (bool, error) {
	res, err := h.run.Run(ctx, runner.Command{Name: "curl", Args: []string{"-sS", "-o", "/dev/null", "-w", "%{http_code}", "-L", "-r", "0-0", "--max-time", "30", url}})
	code, _ := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	switch {
	case slices.Contains(privateAnswers, code):
		return true, nil
	case code >= 200 && code < 400:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("its download didn't answer: %w", err)
	}
	return false, fmt.Errorf("its download answered %d", code)
}
