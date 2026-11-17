package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/leeovery/kit/internal/ask"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/look"
	"github.com/leeovery/kit/internal/render"
)

// brief is the annotation holding what a command does, briefly, as kit's
// help lists it.
const brief = "brief"

// help shows kit's help at a terminal in kit's look, and cobra's otherwise:
// kit's own page, or a command's. Help is text: with --json, it shows
// nothing, and the run fails as refused (helpRefused).
func (a *app) help(plain func(*cobra.Command, []string)) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		if a.json {
			a.helpRefused = usageError{fmt.Errorf("%s's help prints text; it takes no --json", cmd.CommandPath())}
			return
		}
		out := cmd.OutOrStdout()
		if !a.pretty(out) {
			plain(cmd, args)
			return
		}
		lines := a.commandHelp(cmd)
		if !cmd.HasParent() {
			lines = a.kitHelp(cmd)
		}
		_, _ = io.WriteString(a.colors(out), strings.Join(lines, "\n")+"\n")
	}
}

// kitHelp is kit's own page: the wordmark, what kit does, this Mac's
// commands, each with what it does, then the nouns of each group, a line
// a group, and the flags.
func (a *app) kitHelp(root *cobra.Command) []string {
	width := min(max(a.Width(root.OutOrStdout()), 40), look.Width)
	out := []string{""}
	if !a.underHome {
		out = append(append(out, look.Head(look.Meta("help", a.machineName(), a.Now().Format("Mon 2 Jan · 15:04"))...)...), "")
	}
	out = append(out, "  "+look.White("kit sets a Mac up from a config repository, and keeps it that way."), "")
	for _, g := range root.Groups() {
		var cmds []*cobra.Command
		for _, c := range root.Commands() {
			if c.GroupID == g.ID && c.IsAvailableCommand() {
				cmds = append(cmds, c)
			}
		}
		if len(cmds) == 0 {
			continue
		}
		label := strings.TrimSuffix(g.Title, ":")
		if g.ID == groupMac {
			out = append(out, look.Header(label, ""))
			for _, c := range cmds {
				out = append(out, look.Cut("  "+look.Strong(c.Name())+"  "+look.Muted(cmp.Or(c.Annotations[brief], c.Short)), width))
			}
			out = append(out, "")
			continue
		}
		out = append(out, look.Header(label, takes(cmds)))
		out = append(out, nouns(cmds, width)...)
		out = append(out, "")
	}
	out = append(out, look.Header("Flags", ""))
	return append(out, flagLines(root.PersistentFlags(), width)...)
}

// commandHelp is a command's page: how it's used and what it does, then its
// own commands, each with what it does, and its flags.
func (a *app) commandHelp(cmd *cobra.Command) []string {
	width := min(max(a.Width(cmd.OutOrStdout()), 40), look.Width)
	out := []string{"", "  " + look.Strong(cmd.UseLine()), ""}
	for para := range strings.SplitSeq(cmp.Or(cmd.Long, cmd.Short), "\n\n") {
		for l := range strings.SplitSeq(ansi.Wordwrap(strings.Join(strings.Fields(para), " "), width-2, ""), "\n") {
			out = append(out, "  "+look.Muted(l))
		}
		out = append(out, "")
	}
	var subs []*cobra.Command
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() {
			subs = append(subs, c)
		}
	}
	if len(subs) > 0 {
		out = append(out, look.Header("Commands", ""))
		for _, c := range subs {
			out = append(out, look.Cut("  "+look.Strong(c.Name())+"  "+look.Muted(c.Short), width))
		}
		out = append(out, "")
	}
	if cmd.HasAvailableLocalFlags() {
		out = append(out, look.Header("Flags", ""))
		out = append(out, flagLines(cmd.LocalFlags(), width)...)
		out = append(out, "")
	}
	return out[:len(out)-1]
}

// takes says what each of a group's commands takes, when they all take the
// same: add and remove.
func takes(cmds []*cobra.Command) string {
	for _, c := range cmds {
		var names []string
		for _, s := range c.Commands() {
			names = append(names, s.Name())
		}
		if !slices.Contains(names, "add") || !slices.Contains(names, "remove") {
			return ""
		}
	}
	return "each takes add and remove"
}

// nouns are a group's commands by name, dots between them, on as few lines
// as fit.
func nouns(cmds []*cobra.Command, width int) []string {
	var out []string
	line := "  "
	for i, c := range cmds {
		add := look.Strong(c.Name())
		if i > 0 {
			add = look.Dim(" · ") + add
		}
		if i > 0 && ansi.StringWidth(line+add) > width {
			out = append(out, line)
			line, add = "  ", look.Strong(c.Name())
		}
		line += add
	}
	return append(out, line)
}

// flagLines are flags, each with what it does.
func flagLines(flags *pflag.FlagSet, width int) []string {
	var out []string
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		out = append(out, look.Cut("  "+look.Strong("--"+f.Name)+"  "+look.Muted(f.Usage), width))
	})
	return out
}

// machineName is this Mac's name, when it has one: kit's help shows it.
func (a *app) machineName() string {
	dirs, err := a.dirs()
	if err != nil {
		return ""
	}
	name, err := config.ReadMachine(dirs.State)
	if err != nil {
		return ""
	}
	return name
}

// errOut is where kit's errors go: what went wrong shows in kit's look at a
// terminal, and as a line otherwise.
type errOut struct {
	io.Writer
	a *app
}

// usageError is a command line kit couldn't parse, as a flag it doesn't
// know: what to run about it is the command's help.
type usageError struct{ error }

func (e usageError) Unwrap() error { return e.error }

// unknownCommand is cobra's error for a command it doesn't know.
var unknownCommand = regexp.MustCompile(`^unknown command "([^"]+)" for "([^"]+)"`)

// startingWith are the commands under parent, the path to it in kit's
// command line, whose names start with typed, by name.
func startingWith(root *cobra.Command, parent, typed string) []string {
	under, _, err := root.Find(strings.Fields(parent)[1:])
	if err != nil {
		return nil
	}
	var names []string
	for _, c := range under.Commands() {
		if c.IsAvailableCommand() && strings.HasPrefix(c.Name(), typed) {
			names = append(names, c.Name())
		}
	}
	slices.Sort(names)
	return names
}

// asked reports whether args, a command line kit couldn't read, set the
// boolean flag named, as --json or --json=true, before any --: its last
// setting, as flags are read.
func asked(args []string, flag string) bool {
	set := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == flag {
			set = true
		} else if value, ok := strings.CutPrefix(arg, flag+"="); ok {
			set, _ = strconv.ParseBool(value)
		}
	}
	return set
}

// show shows err, from cmd run with args, which has kit exit with status:
// with --json, a document on standard output, unless the command wrote its
// own, then a line, as without a terminal; at a terminal, a row of its
// own, what to do about it on the line under it; else "kit:" and the
// error, then, for a command line kit couldn't read, its usage. An error
// that only needs attention shows as such. A command line naming a command
// by a start of its name that others' names start with too lists them.
func (e *errOut) show(cmd *cobra.Command, args []string, err error, status int) {
	m := unknownCommand.FindStringSubmatch(err.Error())
	var could []string
	if m != nil {
		for _, name := range startingWith(cmd.Root(), m[2], m[1]) {
			could = append(could, m[2]+" "+name)
		}
	}
	said := err.Error()
	if len(could) > 1 {
		said = fmt.Sprintf("%s %s could be %s", m[2], m[1], strings.Join(could, ", "))
	}
	// A command line kit couldn't read may not have set --json or --plain.
	asJSON := e.a.json || !e.a.parsed && asked(args, "--json")
	plain := e.a.plain || !e.a.parsed && asked(args, "--plain")
	// Standard output keeps to one document: what went wrong after the
	// command wrote its own is a line on standard error.
	if asJSON && !e.a.documented() {
		_ = render.WriteJSON(e.a.Stdout, struct {
			Schema int    `json:"schema"`
			Error  string `json:"error"`
			Exit   int    `json:"exit"`
		}{briefSchema, said, status})
		return
	}
	if asJSON || !e.a.pretty(e.Writer) || plain {
		_, _ = fmt.Fprintf(e.Writer, "kit: %s\n", said)
		if !e.a.parsed {
			_, _ = fmt.Fprintln(e.Writer, cmd.UsageString())
		}
		return
	}
	_, needsYou := errors.AsType[notFound](err)
	width := min(max(e.a.Width(e.Writer), 40), look.Width)
	state, says := look.Failed, look.Red
	switch {
	case errors.Is(err, ask.ErrCancelled):
		state, says = look.Skipped, look.Muted
	case needsYou:
		state, says = look.NeedsYou, look.Orange
	}
	text := err.Error()
	row := look.Row{State: state, Name: cmd.CommandPath()}
	if len(could) > 1 {
		row.Name, row.Says = m[2]+" "+m[1], says("more than one command starts so")
		parts := make([]string, len(could))
		for i, c := range could {
			parts[i] = look.Cmd(c)
		}
		row.Under = []string{look.Todo(parts...)}
	} else if m != nil {
		row.Name, row.Says = m[2]+" "+m[1], says("no such command")
		if s := cmd.Root().SuggestionsFor(m[1]); len(s) > 0 {
			fixed := slices.Clone(args)
			if i := slices.Index(fixed, m[1]); i >= 0 {
				fixed[i] = s[0]
			}
			row.Under = []string{look.Todo(look.Muted("did you mean ") + look.Cmd(strings.Join(append([]string{"kit"}, fixed...), " ")))}
		}
	} else {
		row.Says = says(text)
		if _, ok := errors.AsType[usageError](err); ok {
			row.Under = []string{look.Todo(look.Cmd(cmd.CommandPath() + " --help"))}
		}
		if ansi.StringWidth(look.Rows("  ", 1<<16, row)[0]) > width {
			var lines []string
			for l := range strings.SplitSeq(ansi.Wordwrap(text, width-4, " "), "\n") {
				lines = append(lines, says(l))
			}
			row.Says, row.Under = "", append(lines, row.Under...)
		}
	}
	lines := append([]string{""}, look.Timeline("  ", width, row)...)
	_, _ = io.WriteString(e.a.colors(e.Writer), strings.Join(lines, "\n")+"\n")
}
