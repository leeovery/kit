package brew

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/runner"
)

// githubToken is the secret Homebrew downloads a private GitHub
// repository's release with: its own variable, which a tap's download
// reads.
const githubToken = "HOMEBREW_GITHUB_API_TOKEN"

// githubHosts are where GitHub answers for a repository's downloads, its
// releases' storage among them: the token opens these alone.
var githubHosts = []string{"github.com", "api.github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com", "github-releases.githubusercontent.com"}

// NeedsSecret is the GitHub token, for a formula of a tap other than
// Homebrew's own whose download is private (needsToken).
func (f formulae) NeedsSecret(ctx context.Context, name string) (kind.Need, error) {
	return f.needsToken(ctx, f.Name(), "--formula", name)
}

// NeedsSecret is the GitHub token, for a cask of a tap other than
// Homebrew's own whose download is private (needsToken).
func (c casks) NeedsSecret(ctx context.Context, name string) (kind.Need, error) {
	return c.needsToken(ctx, c.Name(), "--cask", name)
}

// needsToken is githubToken, and why, for name, of the kind named kindName,
// a formula or a cask as which says, when brew info finds it's from a tap
// other than Homebrew's own, by its full name, and any of its downloads
// answers as a private one does (private): a formula's source, and its
// bottle. A tap named that isn't added yet is added first, as brew info
// can't describe what's in it, the Need's Undo taking it out again.
func (h *Homebrew) needsToken(ctx context.Context, kindName, which, name string) (kind.Need, error) {
	var need kind.Need
	if _, ok := tapOf(name); ok {
		added, err := h.tap(ctx, kindName, []string{name})
		if len(added) > 0 {
			need.Undo = func(ctx context.Context) error { return h.untap(ctx, kindName, added) }
		}
		if err != nil {
			return need, err
		}
	}
	full, downloads, err := h.downloads(ctx, which, name)
	if _, ok := tapOf(full); err != nil || !ok {
		return need, err
	}
	if len(downloads) == 0 {
		return need, fmt.Errorf("brew info names no download of %s", name)
	}
	event.Say(ctx, event.Doing{Says: "checking whether its download is private"})
	var errs []error
	for _, d := range downloads {
		private, err := h.private(ctx, d)
		if private {
			need.Secret, need.Why = githubToken, "its download is private"
			return need, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return need, kind.Joined(errs)
}

// untap takes the taps out, the kind named kindName's, in Homebrew's turn to
// change things.
func (h *Homebrew) untap(ctx context.Context, kindName string, taps []string) error {
	defer h.take(ctx, kindName)()
	var errs []error
	for _, tap := range taps {
		if err := h.watched(ctx, "untapping "+tap, h.batched(runner.Command{Name: "brew", Args: []string{"untap", tap}})); err != nil {
			errs = append(errs, fmt.Errorf("couldn't untap %s: %w", tap, err))
		}
	}
	return errors.Join(errs...)
}

// downloads are name's full name, a formula or a cask as which says, and
// where it downloads from, as brew info says: a formula's stable source and
// one of its bottle's files (a bottle's files are kept together, so one's
// answer is all's), or a cask's.
func (h *Homebrew) downloads(ctx context.Context, which, name string) (full string, downloads []string, err error) {
	res, err := h.brew(ctx, "info", "--json=v2", which, name)
	if err != nil {
		return "", nil, err
	}
	var in info
	if err := json.Unmarshal(res.Stdout, &in); err != nil {
		return "", nil, fmt.Errorf("read brew info's answer: %w", err)
	}
	switch {
	case len(in.Formulae) > 0:
		f := in.Formulae[0]
		full, downloads = f.FullName, []string{f.URLs.Stable.URL}
		if tags := slices.Sorted(maps.Keys(f.Bottle.Stable.Files)); len(tags) > 0 {
			downloads = append(downloads, f.Bottle.Stable.Files[tags[0]].URL)
		}
	case len(in.Casks) > 0:
		full, downloads = in.Casks[0].FullToken, []string{in.Casks[0].URL}
	}
	return full, slices.DeleteFunc(downloads, func(d string) bool { return d == "" }), nil
}

// private reports whether download, asked for without a token, answers as
// a private one does: 401 or 404 from GitHub (githubHosts), once redirects
// are followed. One request, for its first byte alone, ~/.curlrc never
// read, as it could add a token. Any 2xx is open, cut off at the time
// limit or not. Anything else is an error, saying how it answered or why
// it didn't, as without the network: a token would open none of it.
func (h *Homebrew) private(ctx context.Context, download string) (bool, error) {
	res, err := h.run.Run(ctx, runner.Command{Name: "curl", Args: []string{"-q", "-sS", "-o", "/dev/null", "-w", "%{http_code} %{url_effective}", "-L", "-r", "0-0", "--max-time", "10", download}})
	said, effective, _ := strings.Cut(strings.TrimSpace(string(res.Stdout)), " ")
	code, _ := strconv.Atoi(said)
	host := hostOf(effective)
	switch {
	case code >= 200 && code < 300:
		return false, nil
	case (code == 401 || code == 404) && slices.Contains(githubHosts, host):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("its download didn't answer: %w", err)
	}
	return false, fmt.Errorf("its download answered %d, from %s", code, host)
}

// hostOf is the host address names: "" for none.
func hostOf(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
