package nightly

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/state"
	"github.com/leeovery/kit/internal/status"
)

// alertsFile remembers the drift digest, in kit's state directory.
const alertsFile = "alerts.json"

// digestEvery is how often, at most, the drift digest changes.
const digestEvery = 24 * time.Hour

// driftArea is the area of the kinds' steps, whose items are drift.
const driftArea = "Drift"

// Alert is something to notify about: id stays the same while it's the same
// problem, so a notification is posted once and cleared when its alert goes.
type Alert struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// digest is the drift digest last given.
type digest struct {
	ID   string    `json:"id"`
	Body string    `json:"body"`
	At   time.Time `json:"at"`
}

// alertsRecord is what kit remembers of its alerts.
type alertsRecord struct {
	Digest digest `json:"digest,omitzero"`
}

// Alerts are what doc's run should notify about: each problem a check found,
// and each step that failed or was deferred, at once; and the drift that
// needs attention as one digest, changing at most once a day. Jobs' own
// steps are left to the check on the runs, which says how each went.
func Alerts(doc status.Document, stateDir string, now time.Time) ([]Alert, error) {
	var alerts []Alert
	var drift []string
	for _, s := range doc.Steps {
		if s.Area == Area && s.ID != CheckName {
			continue
		}
		switch s.State {
		case check.Failed, check.Deferred:
			alerts = append(alerts, Alert{ID: s.ID, Title: s.Title, Body: s.Reason})
			continue
		}
		for _, it := range s.Items {
			switch {
			case it.Quiet != "":
			case s.Area == driftArea:
				drift = append(drift, s.ID+" "+it.Name)
			default:
				alerts = append(alerts, Alert{ID: it.ID, Title: s.Title, Body: it.Name})
			}
		}
	}
	var given digest
	err := state.Update(stateDir, alertsFile, func(r *alertsRecord) {
		given = r.Digest
		if len(drift) == 0 {
			r.Digest = digest{}
			given = digest{}
			return
		}
		id := digestID(drift)
		switch {
		case given.ID == id:
		case given.ID == "" || now.Sub(given.At) >= digestEvery:
			r.Digest = digest{ID: id, Body: digestBody(drift), At: now}
		}
		given = r.Digest
	})
	if err != nil {
		return nil, err
	}
	if given.ID != "" {
		alerts = append(alerts, Alert{ID: given.ID, Title: "Drift", Body: given.Body})
	}
	return alerts, nil
}

// digestID names a list of drift: the same list, the same id.
func digestID(drift []string) string {
	sorted := slices.Sorted(slices.Values(drift))
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return "drift-" + hex.EncodeToString(sum[:])[:12]
}

// digestBody says what drift needs attention: the first few, and how many
// more.
func digestBody(drift []string) string {
	const shown = 3
	sorted := slices.Sorted(slices.Values(drift))
	things := "things differ"
	if len(sorted) == 1 {
		things = "thing differs"
	}
	list := strings.Join(sorted[:min(shown, len(sorted))], ", ")
	if len(sorted) > shown {
		list += fmt.Sprintf(" and %d more", len(sorted)-shown)
	}
	return fmt.Sprintf("%d %s from the config: %s → kit reconcile", len(sorted), things, list)
}
