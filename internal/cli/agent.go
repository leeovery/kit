package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/gitrepo"
	"github.com/leeovery/kit/internal/guide"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

// agentSchema is the version of kit agent's documents, --json's.
const agentSchema = 1

// agentWidth is how wide kit agent's lines go, at most: the guide's files
// are wrapped within it, and what kit agent adds to them is wrapped to it.
const agentWidth = 80

func newAgentCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "agent [topic]",
		Short:       "Instructions for an agent changing this Mac's set-up through kit",
		Annotations: map[string]string{brief: "instructions for an agent changing this Mac's set-up"},
		Long: `Print the guide for an agent, Claude or another, about to change this Mac's
set-up through kit: this config's facts, then how to change kit-config so
that what works on this Mac works on a new one too, then the topics that go
deeper and the commands that check a change. It's plain text, at a terminal
or not; --json gives the facts as fields and the guide as text.

kit agent and a topic goes into that topic, found by the start of its name.
kit agent order <name> says where a step, or a thing as in cask:firefox,
falls in a new Mac's run.`,
		Args: topicArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.agentGuide(cmd.Context())
		},
	}
	for _, t := range guide.Topics {
		cmd.AddCommand(newTopicCommand(a, t))
	}
	cmd.AddCommand(newOrderCommand(a))
	return cmd
}

// orderTopic is kit agent order's name, among the topics.
const orderTopic = "order"

// topicArgs refuses what kit agent was given beside its own name: by then,
// a topic found by the start of its name is a command of its own, so it's
// one no topic's name starts with, or more than one's does.
func topicArgs(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	topics := append(guide.TopicNames(), orderTopic)
	var could []string
	for _, t := range topics {
		if strings.HasPrefix(t, args[0]) {
			could = append(could, t)
		}
	}
	known := "kit agent's topics are " + inWords(topics, "and")
	if len(could) > 1 {
		return usageError{fmt.Errorf("%s could be %s: %s", args[0], inWords(could, "or"), known)}
	}
	return usageError{fmt.Errorf("no topic %s: %s", args[0], known)}
}

// newTopicCommand is kit agent <topic>: the topic t, in depth.
func newTopicCommand(a *app, t guide.Topic) *cobra.Command {
	return &cobra.Command{
		Use:   t.Name,
		Short: t.About,
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if a.json {
				return render.WriteJSON(a.Stdout, struct {
					Schema int    `json:"schema"`
					Topic  string `json:"topic"`
					About  string `json:"about"`
					Text   string `json:"text"`
				}{agentSchema, t.Name, t.About, t.Text()})
			}
			_, err := io.WriteString(a.Stdout, t.Text())
			return err
		},
	}
}

// agentFacts are this config's facts, as kit agent opens with them: kit's
// version; kit-config's folder and its repository, or why it can't be read
// (Unread), which leaves the rest out; this Mac's name and folder, the
// config's Macs, and the blocks secrets are declared in, for this Mac; and
// what else couldn't be found out, if anything. Guide is what follows them.
type agentFacts struct {
	Schema     int        `json:"schema"`
	Version    string     `json:"version"`
	Config     string     `json:"config,omitempty"`
	Unread     string     `json:"unread,omitempty"`
	Problem    string     `json:"problem,omitempty"`
	Repository string     `json:"repository,omitempty"`
	Machine    string     `json:"machine,omitempty"`
	Folder     string     `json:"folder,omitempty"`
	Macs       []agentMac `json:"macs,omitempty"`
	Secrets    []place    `json:"secrets,omitempty"`
	Guide      string     `json:"guide"`
}

// agentMac is one of the config's Macs, as kit agent lists them.
type agentMac struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// agentGuide prints the guide, this config's facts first: as one document
// with --json, else as plain text, at a terminal or not.
func (a *app) agentGuide(ctx context.Context) error {
	f := a.agentFacts(ctx)
	f.Guide = guideText()
	if a.json {
		return render.WriteJSON(a.Stdout, f)
	}
	_, err := io.WriteString(a.Stdout, strings.Join(f.lines(), "\n")+"\n\n"+f.Guide)
	return err
}

// guideText is the guide as kit agent prints it after the facts: the main
// guide, the topics, and the commands that check a change.
func guideText() string {
	lines := []string{"## Topics", ""}
	lines = append(lines, look.Wrap("Each goes deeper than the guide, as `kit agent steps` does; a topic is found by the start of its name.", agentWidth-2)...)
	lines = append(lines, "")
	for _, t := range guide.Topics {
		lines = append(lines, bullet(t.Name+": "+t.About)...)
	}
	lines = append(lines, bullet("order: `kit agent order <name>` says where a step, or a thing as in cask:firefox, falls in a new Mac's run.")...)
	return guide.Main() + "\n" + strings.Join(lines, "\n") + "\n\n" + guide.Checking()
}

// agentFacts finds this config's facts. What it can't find, it says, and
// leaves out what follows from it: the guide is printed anyway.
func (a *app) agentFacts(ctx context.Context) agentFacts {
	f := agentFacts{Schema: agentSchema, Version: a.Version}
	dirs, err := a.dirs()
	if err != nil {
		f.Unread = err.Error()
		return f
	}
	f.Config = dirs.Config
	cfg, err := a.loadConfig(dirs)
	if err != nil {
		f.Unread = err.Error()
		return f
	}
	for _, name := range cfg.MacNames() {
		f.Macs = append(f.Macs, agentMac{Name: name, Description: cfg.Macs[name].Description})
	}
	var mac string
	var problems []string
	if f.Machine, err = config.ReadMachine(dirs.State); err != nil {
		problems = append(problems, err.Error())
	} else if cfg.Knows(f.Machine) {
		mac, f.Folder = f.Machine, f.Machine+"/"
	}
	if f.Secrets, err = secretsPlaces(cfg, mac); err != nil {
		problems = append(problems, err.Error())
	}
	f.Problem = strings.Join(problems, "; ")
	f.Repository = a.configRepository(ctx, cfg, dirs.Config, mac)
	return f
}

// configRepository is where kit-config is fetched from, as kit names a
// remote, run on mac's PATH: "" when it has no origin, or git can't say.
func (a *app) configRepository(ctx context.Context, cfg *config.Config, dir, mac string) string {
	home, err := a.HomeDir()
	if err != nil {
		return ""
	}
	path, err := cfg.SearchPath(home, mac)
	if err != nil {
		return ""
	}
	repo := gitrepo.Repo{Dir: dir, Run: a.Runner(path, childEnv(a.Getenv, home, path)), Home: home}
	url, err := repo.URL(ctx, "origin")
	if err != nil || url == "" {
		return ""
	}
	return gitrepo.Shown(url)
}

// lines are the facts as kit agent prints them: a heading, then a fact a
// line, each wrapped under its value.
func (f agentFacts) lines() []string {
	lines := []string{"kit agent: how to change this Mac's set-up through kit", "", "This config"}
	fact := func(label string, values ...string) {
		for i, v := range values {
			if i > 0 {
				label = ""
			}
			lines = append(lines, hanging(fmt.Sprintf("  %-12s", label), v)...)
		}
	}
	if f.Unread != "" {
		fact("kit-config", f.Unread)
		fact("kit", f.Version)
		return lines
	}
	fact("kit-config", f.Config)
	fact("repository", cmp.Or(f.Repository, "none: kit-config has no origin, or git couldn't say"))
	fact("this Mac", f.machine())
	macs := make([]string, len(f.Macs))
	for i, m := range f.Macs {
		macs[i] = m.Name
		if m.Description != "" {
			macs[i] += " (" + m.Description + ")"
		}
	}
	fact("Macs", strings.Join(macs, ", "))
	secrets := make([]string, len(f.Secrets))
	for i, p := range f.Secrets {
		secrets[i] = p.title() + ": " + strings.Join(p.says(), " · ")
	}
	if len(secrets) == 0 {
		secrets = []string{"no secrets blocks for this Mac"}
	}
	fact("secrets", secrets...)
	if f.Problem != "" {
		fact("problem", f.Problem)
	}
	fact("kit", f.Version)
	return lines
}

// machine is this Mac's fact: its name and folder, or why there's none.
func (f agentFacts) machine() string {
	switch {
	case f.Folder != "":
		return f.Machine + ": its own lines and files in " + f.Folder + ", every Mac's in " + config.Shared + "/"
	case f.Machine != "":
		return "named " + f.Machine + ", which " + config.File + " doesn't know"
	}
	return "no name yet: kit machine <name> names it"
}

// hanging is text wrapped within kit agent's width after lead, its lines
// after the first indented under the first's text.
func hanging(lead, text string) []string {
	wrapped := look.Wrap(text, max(agentWidth-len(lead), 20))
	for i := range wrapped {
		if i == 0 {
			wrapped[i] = lead + wrapped[i]
			continue
		}
		wrapped[i] = strings.Repeat(" ", len(lead)) + wrapped[i]
	}
	return wrapped
}

// bullet is text as an item of a list, as the guide writes them: wrapped
// within kit agent's width, after a dash, its lines after the first
// indented under its text.
func bullet(text string) []string {
	wrapped := look.Wrap(text, agentWidth-2)
	for i := range wrapped {
		lead := "  "
		if i == 0 {
			lead = "- "
		}
		wrapped[i] = lead + wrapped[i]
	}
	return wrapped
}

// inWords lists names as words do: a, b and c, or a, b or c, as last says.
func inWords(names []string, last string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " " + last + " " + names[len(names)-1]
}
