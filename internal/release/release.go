// Package release is kit's newest release, as GitHub says: asked by the
// scheduled run at most once a day and recorded in kit's state, and the
// check that reads the record, saying when a newer kit is out than the one
// this Mac runs. Nothing upgrades kit by itself: the person does.
package release

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/engine"
	"github.com/leeovery/kit/internal/state"
	"github.com/leeovery/kit/internal/steps"
	"github.com/leeovery/kit/internal/whole"
)

// StepName names the step checking this Mac runs kit's newest release.
const StepName = "kit-version"

// repo is kit's repository on GitHub, whose releases install.sh downloads.
const repo = "leeovery/kit"

// file is the record of kit's newest release, in its state directory.
const file = "release.json"

// How often GitHub is asked, and how long an ask may take.
const (
	askEvery  = 24 * time.Hour
	askWithin = 10 * time.Second
)

// Record is kit's newest release, as GitHub last said, and when it was
// asked.
type Record struct {
	Newest string    `json:"newest,omitempty"`
	Asked  time.Time `json:"asked,omitzero"`
}

// Load reads the record in the state directory: an empty one when there's
// none, or one that won't read, as the next answer replaces it.
func Load(stateDir string) Record {
	rec, err := state.Load[Record](stateDir, file)
	if err != nil {
		return Record{}
	}
	return rec
}

// Ask asks GitHub, at site, for kit's newest release, and records it in
// stateDir with now: unless it was asked in the day before now. A failed
// ask records nothing, so the last answer stands, and the next run asks
// again. An answer replaces the record whole, never reading it, so one that
// won't read goes.
func Ask(ctx context.Context, client *http.Client, site, stateDir string, now time.Time) error {
	if since := now.Sub(Load(stateDir).Asked); since >= 0 && since < askEvery {
		return nil
	}
	newest, err := latest(ctx, client, site)
	if err != nil {
		return fmt.Errorf("ask GitHub for kit's newest release: %w", err)
	}
	data, err := json.MarshalIndent(Record{Newest: newest, Asked: now}, "", "  ")
	if err == nil {
		err = os.MkdirAll(stateDir, 0o700)
	}
	if err == nil {
		err = whole.Write(filepath.Join(stateDir, file), append(data, '\n'), 0o600)
	}
	if err != nil {
		return fmt.Errorf("record kit's newest release: %w", err)
	}
	return nil
}

// latest is the version of kit's newest release, as the tag GitHub's latest
// release redirects to names, as install.sh finds it: the redirect read,
// never followed, with no token and no API.
func latest(ctx context.Context, client *http.Client, site string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, askWithin)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, site+"/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	asking := *client
	asking.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := asking.Do(req)
	if err != nil {
		return "", err
	}
	_ = res.Body.Close()
	loc, err := res.Location()
	if err != nil {
		return "", fmt.Errorf("GitHub answered %s, with no release named", res.Status)
	}
	dir, tag := path.Split(loc.Path)
	version := strings.TrimPrefix(tag, "v")
	if !strings.HasSuffix(dir, "/releases/tag/") || !config.IsRelease(version) {
		return "", fmt.Errorf("GitHub named no release's tag: %s", loc)
	}
	return version, nil
}

// Check is the step checking this Mac runs kit's newest release, as the
// record in stateDir says, without asking: running older than the newest
// needs the person to upgrade it, through formula, its Homebrew formula. A
// kit that isn't a release's, as a build from source, or a Mac with no
// record yet, or one that won't read, claims nothing.
func Check(running, formula, stateDir string) engine.Step {
	return engine.Step{
		Name: StepName, Title: "kit version", Area: steps.AreaMac,
		Check: func(context.Context) check.Result {
			rec := Load(stateDir)
			if !config.Older(running, rec.Newest) {
				return check.Result{State: check.OK, Summary: running, Glance: running}
			}
			return check.Result{State: check.Attention, Summary: running, Items: []check.Item{{
				ID:     StepName + ":out",
				Name:   fmt.Sprintf("kit %s is out; this Mac runs %s", rec.Newest, running),
				State:  check.Problem,
				Detail: "brew upgrade " + formula + " installs it",
			}}}
		},
	}
}
