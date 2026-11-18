package render

import (
	"time"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/kind"
	"github.com/leeovery/kit/internal/look"
)

// Drifted is a thing kit reconcile asks about: its name and kind, what's
// wrong with it, in its kind's words, more on it, if anything, and since
// when, if known.
type Drifted struct {
	Name, Kind, What, Detail string
	Since                    time.Time
}

// Answer is a thing that can be done about something drifted: its words,
// and whether it's done for every Mac.
type Answer struct {
	Words  kind.Words
	Shared bool
}

// ReconcileQuestion is kit reconcile's question about d, at now: a row
// saying its kind, what's wrong and for how long, then the answers, each by
// its label, one done for every Mac said apart, and Skip last.
func ReconcileQuestion(d Drifted, answers []Answer, now time.Time) ask.Question {
	choices := make([]look.Choice, 0, len(answers)+1)
	for _, a := range answers {
		label := a.Words.Label
		if a.Shared {
			label += " everywhere"
		}
		choices = append(choices, look.Choice{Label: label, Does: a.Words.Does})
	}
	choices = append(choices, look.Choice{Label: "Skip", Does: "not now"})
	return ask.Question{About: look.Row{State: look.NeedsYou, Name: d.Name, Says: driftSays(d, now)}, Answers: choices}
}

// driftSays says what d is, as its question's row says it: its kind,
// what's wrong, and for how long.
func driftSays(d Drifted, now time.Time) string {
	says := []string{look.Muted(d.Kind), look.Orange(d.What)}
	if d.Detail != "" {
		says = append(says, look.Muted(d.Detail))
	}
	if !d.Since.IsZero() {
		says = append(says, look.Muted(since(now.Sub(d.Since))))
	}
	return look.Says(says...)
}
