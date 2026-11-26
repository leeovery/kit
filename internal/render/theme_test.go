package render_test

import (
	"bytes"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/boot"
	"github.com/leeovery/kit/internal/event"
	"github.com/leeovery/kit/internal/render"
	"github.com/leeovery/kit/internal/render/rendertest"
	"github.com/leeovery/kit/internal/setup"
)

// Words take the terminal's own colours, so kit reads on a light theme as
// on a dark one. An exact colour is kit's own alone: the wordmark and the
// bar's blocks along its gradient; its accent, pink; and a pill, its text
// dark on the pill's colour. A word in any other exact colour fails, on
// the screens kit draws most: status's report; apply's screen with a card
// up, with the to-dos, and its record; setup's screen, and its window
// signing in with the phone on a dark background and on a light one.
func TestOnlyKitsOwnHaveExactColours(t *testing.T) {
	var status bytes.Buffer
	show(t, render.NewPretty(&colorprofile.Writer{Forward: &status, Profile: colorprofile.TrueColor}, 80, false), rendertest.StatusRun())
	screens := map[string]string{
		"status":               status.String(),
		"setup":                strings.Join(setupFace(nil, rendertest.SetupDone(rendertest.At)...).View(80, 60), "\n"),
		"signing in, on dark":  strings.Join(signingIn(t, true), "\n"),
		"signing in, on light": strings.Join(signingIn(t, false), "\n"),
	}
	for name, events := range map[string][]event.Event{"a card": waitingOnYou(), "the to-dos": toDosUpTo(3), "the record": appliedRun()} {
		face, out, _ := applyIn(colorprofile.TrueColor, 160, 64, time.Minute, events)
		screens[name+", live"] = out.String()
		if err := face.Close(); err != nil {
			t.Fatal(err)
		}
		screens[name+", closed"] = out.String()
	}

	for name, raw := range screens {
		if strings.TrimSpace(ansi.Strip(raw)) == "" {
			t.Errorf("%s drew nothing", name)
		}
		for l := range strings.SplitSeq(rendertest.Styled(raw), "\n") {
			for _, run := range styledRuns.FindAllStringSubmatch(l, -1) {
				if fg, bg, text := exact(run[1], "38"), exact(run[1], "48"), run[2]; !kitsOwn(fg, bg, text) {
					t.Errorf("%s: %q drawn in %s on %s: want the terminal's colours", name, text, fg, bg)
				}
			}
		}
	}
}

// signingIn is setup's window signing in to GitHub with the phone, its QR
// code drawn for a dark background or a light one.
func signingIn(t *testing.T, dark bool) []string {
	t.Helper()
	f := render.NewSetupFace(ask.Terminal{Out: io.Discard}, dark, func() time.Time { return rendertest.At }, func() {}, func() string { return "laptop" })
	f.Emit(rendertest.SetupDone(rendertest.At)[0])
	f.Emit(event.StepStarted{Time: rendertest.At, Step: setup.GitHubStep})
	keys := []boot.Key{{Key: "enter", Does: "with your phone"}, {Key: "s", Does: "here, in your browser"}}
	picked := asking(t, f, "how will you sign in?", func() error {
		_, err := f.Pick(t.Context(), setup.GitHubStep, keys)
		return err
	})
	f.Key(ask.Key{Name: "enter"})
	if err := <-picked; err != nil {
		t.Fatal(err)
	}
	f.Emit(event.DeviceCode{Time: rendertest.At, Step: setup.GitHubStep, URI: "https://github.com/login/device", Code: "WDJB-MJHT", Expires: rendertest.At.Add(15 * time.Minute)})
	return f.Full(80, 40)
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
// pink; dark on a pill; or the art's blocks along kit's gradient.
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
	return strings.Trim(text, " █▀▄▮") == "" && onGradient(fg)
}

// onGradient reports whether rgb is on kit's gradient, from pink, 255 79
// 154, to orange, 255 162 76, a step's rounding either side.
func onGradient(rgb string) bool {
	var c [3]int
	for i, v := range strings.Split(rgb, ";") {
		c[i], _ = strconv.Atoi(v)
	}
	r, g, b := c[0], c[1], c[2]
	along := float64(g-79) / (162 - 79)
	want := 154 - along*(154-76)
	return r == 255 && along >= 0 && along <= 1 && float64(b) >= want-1 && float64(b) <= want+1
}
