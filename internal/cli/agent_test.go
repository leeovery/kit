package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/guide"
)

// kit agent opens with this config's facts: kit-config's folder and its
// repository, this Mac's name and folder, the config's Macs, the blocks
// secrets are declared in as kit secret places lists them, and kit's
// version; then the guide, its topics, and the commands that check a
// change.
func TestAgentOpensWithTheFacts(t *testing.T) {
	w := placesWorld(t)
	out, _, code := w.run(t, "agent")
	want := "kit agent: how to change this Mac's set-up through kit\n" +
		"\n" +
		"This config\n" +
		"  kit-config  " + filepath.Join(w.home, ".config", "kit") + "\n" +
		"  repository  github.com:someone/kit-config.git\n" +
		"  this Mac    laptop: its own lines and files in laptop/, every Mac's in shared/\n" +
		"  Macs        laptop (MacBook Pro), studio (Mac Studio)\n" +
		"  secrets     tokens: 1Password · op://vault/Item · shared · 2 secrets\n" +
		"              [secrets]: references in full · laptop · 1 secret\n" +
		"              op://vault/Other: 1Password · laptop · 1 secret\n" +
		"  kit         0.1.0\n" +
		"\n"
	if code != 0 || !strings.HasPrefix(out, want) {
		t.Fatalf("kit agent printed\n%s\nexit %d, want it to open\n%s", out, code, want)
	}
	rest := strings.TrimPrefix(out, want)
	if !strings.HasPrefix(rest, guide.Main()) {
		t.Errorf("after the facts, kit agent printed\n%s\nwant the guide", rest)
	}
	topics := strings.Index(rest, "## Topics")
	checking := strings.Index(rest, guide.Checking())
	if topics < len(guide.Main()) || checking < topics || !strings.HasSuffix(rest, guide.Checking()) {
		t.Errorf("kit agent printed\n%s\nwant the guide, then its topics, then the commands that check a change", rest)
	}
	for _, topic := range guide.Topics {
		if !strings.Contains(rest[topics:checking], "- "+topic.Name+": ") {
			t.Errorf("kit agent's topics leave out %s", topic.Name)
		}
	}
}

// Without a config on the Mac, kit agent still prints the guide, saying so
// in the facts.
func TestAgentWithoutAConfig(t *testing.T) {
	w := newWorld(t, nil)
	out, _, code := w.run(t, "agent")
	facts, rest, _ := strings.Cut(out, guide.Main())
	config := filepath.Join(w.home, ".config", "kit")
	want := "kit agent: how to change this Mac's set-up through kit This config kit-config no config repository: no kit.toml in " +
		config + ": clone your config repository to " + config + ", or point KIT_CONFIG at it kit 0.1.0"
	if got := strings.Join(strings.Fields(facts), " "); code != 0 || got != want || !strings.HasSuffix(rest, guide.Checking()) {
		t.Errorf("kit agent printed\n%s\nexit %d, want the facts to say\n%s\nthen the guide", out, code, want)
	}
}

// kit agent is plain text, the same at a terminal as without one, and its
// lines stay within 80 columns.
func TestAgentIsTheSameAtATerminal(t *testing.T) {
	w := placesWorld(t)
	plain, _, _ := w.run(t, "agent")
	w.terminal, w.environ = true, []string{"TTY_FORCE=1", "TERM=xterm-256color"}
	pretty, _, code := w.run(t, "agent")
	if code != 0 || pretty != plain {
		t.Errorf("at a terminal kit agent printed\n%s\nexit %d, want what it prints without one\n%s", pretty, code, plain)
	}
	// The test's home is longer than a real one, and a path isn't cut.
	for i, line := range strings.Split(strings.ReplaceAll(pretty, w.home, "/Users/someone"), "\n") {
		if n := len([]rune(line)); n > 80 {
			t.Errorf("line %d is %d columns, want 80 at most: %s", i+1, n, line)
		}
	}
}

// A topic is found by the start of its name, as kit's commands are; one no
// topic's name starts with, or more than one's does, is an error naming the
// topics.
func TestAgentTopics(t *testing.T) {
	topic := func(name string) string {
		for _, t := range guide.Topics {
			if t.Name == name {
				return t.Text()
			}
		}
		return ""
	}
	const topics = "kit agent's topics are steps, secrets, packages, files, settings, prefs, new-mac and order"
	tests := []struct {
		given, want, wrong string
	}{
		{given: "steps", want: topic("steps")},
		{given: "st", want: topic("steps")},
		{given: "sec", want: topic("secrets")},
		{given: "pa", want: topic("packages")},
		{given: "f", want: topic("files")},
		{given: "set", want: topic("settings")},
		{given: "pr", want: topic("prefs")},
		{given: "new", want: topic("new-mac")},
		{given: "s", wrong: "kit: s could be steps, secrets or settings: " + topics},
		{given: "p", wrong: "kit: p could be packages or prefs: " + topics},
		{given: "nosuch", wrong: "kit: no topic nosuch: " + topics},
	}
	for _, tt := range tests {
		w := newWorld(t, nil)
		out, errOut, code := w.run(t, "agent", tt.given)
		if tt.wrong != "" {
			if code != 2 || !strings.Contains(errOut, tt.wrong+"\n") {
				t.Errorf("kit agent %s printed\n%s\nexit %d, want exit 2 and %q", tt.given, errOut, code, tt.wrong)
			}
			continue
		}
		if code != 0 || out != tt.want {
			t.Errorf("kit agent %s printed\n%s\nexit %d, want the topic", tt.given, out, code)
		}
	}
}

// kit agent is the last of the commands about this Mac in kit's help.
func TestAgentIsLastOfThisMacsCommands(t *testing.T) {
	w := newWorld(t, nil)
	out, _, _ := w.run(t, "--help")
	mac := out[strings.Index(out, "This Mac:"):]
	mac = mac[:strings.Index(mac, "\n\n")]
	lines := strings.Split(mac, "\n")
	if last := strings.Fields(lines[len(lines)-1]); last[0] != "agent" || !strings.Contains(mac, "Instructions for an agent changing this Mac's set-up through kit") {
		t.Errorf("kit's help lists this Mac's commands as\n%s\nwant kit agent last", mac)
	}
}

// What kit agent couldn't find out it says, every problem of it.
func TestAgentSaysEveryProblem(t *testing.T) {
	w := newWorld(t, map[string]string{"kit.toml": twoMacs, "shared/declarations": "[no such section]\n"})
	if err := os.MkdirAll(filepath.Join(w.home, ".local", "state", "kit", "machine"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, _, code := w.run(t, "agent")
	facts, _, _ := strings.Cut(out, guide.Main())
	said := strings.Join(strings.Fields(facts), " ")
	if code != 0 || !strings.Contains(said, " problem read this Mac's name: ") || !strings.Contains(said, "; shared/declarations:1: kit doesn't know the section [no such section] kit 0.1.0") {
		t.Errorf("kit agent printed\n%s\nexit %d, want this Mac's name unread, and the shared file's section, as its problem", facts, code)
	}
}
