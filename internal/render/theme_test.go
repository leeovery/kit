package render_test

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/render/rendertest"
)

// Words take the terminal's own colours, so kit reads on a light theme as
// on a dark one. An exact colour is kit's own alone: on its art, the
// wordmark and the bar along its gradient; in its accent, pink; and on a
// pill, its text dark on the pill's colour. A word in any other exact
// colour fails, on the screens kit draws most: status's report, apply's
// screen with a card up and with the to-dos, and the run's record.
func TestOnlyKitsOwnHaveExactColours(t *testing.T) {
	var status bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &status, Profile: colorprofile.TrueColor}, 80, false), rendertest.StatusRun())
	screens := map[string]string{"status": status.String()}

	card, cardOut, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, waitingOnYou())
	screens["a card"] = cardOut.String()
	_ = card.Close()
	toDos, toDosOut, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, toDosUpTo(3))
	screens["the to-dos"] = toDosOut.String()
	_ = toDos.Close()
	record, recordOut, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, appliedRun())
	if err := record.Close(); err != nil {
		t.Fatal(err)
	}
	screens["the record"] = recordOut.String()

	for name, raw := range screens {
		for l := range strings.SplitSeq(rendertest.Styled(raw), "\n") {
			for _, run := range styledRuns.FindAllStringSubmatch(l, -1) {
				if fg, bg, text := exact(run[1], "38"), exact(run[1], "48"), run[2]; !kitsOwn(fg, bg, text) {
					t.Errorf("%s: %q drawn in %s on %s: want the terminal's colours", name, text, fg, bg)
				}
			}
		}
	}
}

// styledRuns are a styled line's runs: the style each starts with, and its
// text.
var styledRuns = regexp.MustCompile(`\x1b\[([0-9;]*)m([^\x1b]*)`)

// exact is the exact colour a style's parameters set, after the foreground's
// 38 or the background's 48: its red, green and blue; empty when it sets
// none.
func exact(params, which string) string {
	ps := strings.Split(params, ";")
	for i := 0; i+4 < len(ps); i++ {
		if ps[i] == which && ps[i+1] == "2" {
			return strings.Join(ps[i+2:i+5], ";")
		}
	}
	return ""
}

// kitsOwn reports whether text drawn in fg on bg, each an exact colour or
// empty, is kit's own to colour: blank; in the terminal's colours; in kit's
// pink; dark on a pill; or the art's blocks.
func kitsOwn(fg, bg, text string) bool {
	const pink, onPill = "255;79;154", "27;29;36"
	switch {
	case strings.TrimSpace(text) == "":
		return true
	case bg != "":
		return fg == onPill
	case fg == "", fg == pink:
		return true
	}
	return strings.Trim(text, " █▀▄▮") == ""
}
