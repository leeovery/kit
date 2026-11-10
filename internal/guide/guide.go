// Package guide is what kit agent prints: the guide for an agent about to
// change a Mac's set-up through kit, and its topics, each in depth. It's
// text built into kit, from the files in text, so it's always right for
// the kit installed: a change to how kit works changes them in the same
// commit.
package guide

import (
	"embed"
	"fmt"
)

//go:embed text/*.md
var files embed.FS

// Topic is a subject the guide goes into in depth: its name, as kit agent
// takes it, what it's about, and its text.
type Topic struct {
	Name, About string
}

// Topics are the guide's topics, in order, each in its file, text/<name>.md.
var Topics = []Topic{
	{"steps", "Steps of your own: the contract, the environment, writing one"},
	{"secrets", "Secrets: adding one, what uses one, a new Mac's sync"},
	{"packages", "Packages: the kinds, adding and removing, held sections"},
	{"files", "Linked files, what never to link, and the PATH"},
	{"settings", "Settings: macOS's, power, git's, exclusions and features"},
	{"prefs", "Apps' settings: what kit prefs keeps, and restoring them"},
	{"new-mac", "A new Mac: the boot, the run's order, its data, what isn't there"},
}

// Main is the guide, read in one go: kit and kit-config, steps of your own,
// and a new Mac.
func Main() string { return read("main") }

// Checking is the guide's end: the commands that check a change.
func Checking() string { return read("checking") }

// Text is the topic's text.
func (t Topic) Text() string { return read(t.Name) }

// TopicNames are the topics' names, in order.
func TopicNames() []string {
	names := make([]string, len(Topics))
	for i, t := range Topics {
		names[i] = t.Name
	}
	return names
}

// read is the file text/<name>.md, which the package's tests prove is
// there for every name read.
func read(name string) string {
	data, err := files.ReadFile("text/" + name + ".md")
	if err != nil {
		panic(fmt.Sprintf("guide: %s isn't built in: %v", name, err))
	}
	return string(data)
}
