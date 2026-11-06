package cli_test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/leeovery/kit/internal/cli"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/guide"
	"github.com/leeovery/kit/internal/kinds"
	"github.com/leeovery/kit/internal/steps"
)

// guideTexts are what kit agent prints: the guide, as it prints it, and
// each topic, by the command that prints it.
func guideTexts(t *testing.T) map[string]string {
	t.Helper()
	w := newWorld(t, nil)
	out, _, code := w.run(t, "agent", "--json")
	var doc struct{ Guide string }
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 {
		t.Fatalf("kit agent --json printed\n%s\nexit %d: %v", out, code, err)
	}
	texts := map[string]string{"kit agent": doc.Guide}
	for _, topic := range guide.Topics {
		texts["kit agent "+topic.Name] = topic.Text()
	}
	return texts
}

// The guide can't drift from kit's commands: every command it names, in
// backticks, as `kit step add <name> "<what it does>"`, is one of kit's,
// taking what it's given, and every flag it names is one that command
// takes; a flag named alone, as `--secret`, is one the command named
// nearest before it in its paragraph takes, or, with none, some command.
func TestTheGuideNamesOnlyKitsCommandsAndFlags(t *testing.T) {
	root := kitCommands()
	named := 0
	for where, text := range guideTexts(t) {
		for _, paragraph := range paragraphs(text) {
			spans := commandSpans(paragraph)
			named += len(spans)
			for _, wrong := range namedWrongly(root, spans) {
				t.Errorf("%s: %s", where, wrong)
			}
		}
	}
	if named < 50 {
		t.Errorf("the guide names %d commands and flags in backticks, want them all found", named)
	}
}

// The guide can't drift from kit-config's syntax: every example it gives
// of a declarations file, indented, reads as kit reads one, each line of
// [steps] and [manual] as a step's and a step done by hand's; and every
// section header it names, in backticks, is one kit knows.
func TestTheGuidesExamplesReadAsKitReadsThem(t *testing.T) {
	found := 0
	for where, text := range guideTexts(t) {
		for _, example := range declarations(text) {
			found++
			if err := readsAsDeclarations(t, example); err != nil {
				t.Errorf("%s: the example\n%s\ndoesn't read: %v", where, example, err)
			}
		}
	}
	if found < 25 {
		t.Errorf("the guide gives %d examples and headers, want them all found", found)
	}
}

// The checks find what's wrong: a command kit hasn't, a flag a command
// doesn't take, arguments a command doesn't take, a flag no command takes
// or the command before it doesn't, and an example kit can't read; and say
// so.
func TestTheGuidesChecksFindWhatsWrong(t *testing.T) {
	root := kitCommands()
	tests := []struct{ spans, want string }{
		{"`kit step add <name> \"<what it does>\" --after cask:firefox --sudo`", ""},
		{"`kit apply --plan`", ""},
		{"`kit secret add <name> --stdin --json`", ""},
		{"`kit agent order <name>`", ""},
		{"`kit reconcile brew:jq --adopt`", ""},
		{"`kit step add --help`", ""},
		{"`--shared`", ""},
		{"`kit step add` with `--sudo`", ""},
		{"`kit stpe add`", "`kit stpe add`: kit: unknown command \"stpe\" for \"kit\""},
		{"`kit step frob <name>`", "`kit step frob <name>`: kit step has no command frob"},
		{"`kit apply --frob`", "`kit apply --frob`: kit apply has no flag --frob"},
		{"`kit agent order <name> <name>`", "`kit agent order <name> <name>`: kit agent order: accepts 1 arg(s), received 2"},
		{"`kit agent ordr <name>`", "`kit agent ordr <name>`: kit agent: no topic ordr"},
		{"`--nosuch`", "`--nosuch`: no command of kit's has the flag --nosuch"},
		{"`kit status <name>` with `--sudo`", "`--sudo`: kit status, named before it, has no flag --sudo"},
	}
	for _, tt := range tests {
		got := strings.Join(namedWrongly(root, commandSpans(tt.spans)), "\n")
		if !strings.HasPrefix(got, tt.want) || (tt.want == "") != (got == "") {
			t.Errorf("%s: found %q, want %q", tt.spans, got, tt.want)
		}
	}
	examples := []struct{ example, want string }{
		{"[homebrew casks --secret TOKEN]\n", ""},
		{"[homebrew casks --frob TOKEN]\n", "isn't one of"},
		{"[no such section]\n", "kit doesn't know the section"},
		{"[steps]\nname \"does\" --after cask:a\n", ""},
		{"[steps]\nname --after cask:a\n", "what the step does comes after its name"},
		{"[manual]\nname \"do\" --frob x\n", "isn't one of a step by hand's options"},
		{"[secrets op://vault/item --as tokens]\nTOKEN Section/token\n", ""},
	}
	for _, tt := range examples {
		err := readsAsDeclarations(t, tt.example)
		if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("%q reads with %v, want %q", tt.example, err, tt.want)
		}
	}
}

// kitCommands is kit's command tree, to look commands up in.
func kitCommands() *cobra.Command {
	return cli.NewRootCommand(cli.Deps{Version: "0.1.0", Stdout: io.Discard, Stderr: io.Discard, Getenv: func(string) string { return "" }})
}

var (
	backticked = regexp.MustCompile("`([^`]+)`")
	listItem   = regexp.MustCompile(`^(- |\d+\. )`)
)

// paragraphs are text's paragraphs, each of a list's items on its own,
// their lines joined.
func paragraphs(text string) []string {
	var out, cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, " "))
			cur = nil
		}
	}
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || listItem.MatchString(trimmed) {
			flush()
		}
		if trimmed != "" {
			cur = append(cur, trimmed)
		}
	}
	flush()
	return out
}

// commandSpans are what a paragraph has in backticks that names kit's
// commands or flags, in order: a span starting kit and a space, or a flag.
func commandSpans(paragraph string) []string {
	var spans []string
	for _, m := range backticked.FindAllStringSubmatch(paragraph, -1) {
		if strings.HasPrefix(m[1], "kit ") || strings.HasPrefix(m[1], "--") {
			spans = append(spans, m[1])
		}
	}
	return spans
}

// namedWrongly is what's wrong with a paragraph's spans, against root, a
// line each.
func namedWrongly(root *cobra.Command, spans []string) []string {
	var wrong []string
	var near *cobra.Command
	for _, span := range spans {
		cmd, err := checkSpan(root, near, span)
		if err != nil {
			wrong = append(wrong, fmt.Sprintf("`%s`: %v", span, err))
		}
		if cmd != nil {
			near = cmd
		}
	}
	return wrong
}

// checkSpan checks one span, returning the command it names, if it names
// one. A flag alone must be near's, the command named before it, or, with
// none, some command's. A command is its words up to the first that isn't
// the name of a command under the one before, which must take the words
// after them, but its flags, each of which it must take.
func checkSpan(root, near *cobra.Command, span string) (*cobra.Command, error) {
	words, err := config.Words(span)
	if err != nil {
		return nil, err
	}
	if name, ok := strings.CutPrefix(words[0], "--"); ok {
		switch {
		case near != nil && flagOf(near, name) == nil:
			return nil, fmt.Errorf("%s, named before it, has no flag --%s", near.CommandPath(), name)
		case near == nil && !anyHas(root, name):
			return nil, fmt.Errorf("no command of kit's has the flag --%s", name)
		}
		return nil, nil
	}
	cmd, i := root, 1
	for ; i < len(words); i++ {
		sub := subcommand(cmd, words[i])
		if sub == nil {
			break
		}
		cmd = sub
	}
	var args []string
	helped := false
	for ; i < len(words); i++ {
		word := words[i]
		if word == "--" {
			args = append(args, words[i+1:]...)
			break
		}
		if !strings.HasPrefix(word, "--") {
			args = append(args, word)
			continue
		}
		name, _, given := strings.Cut(strings.TrimPrefix(word, "--"), "=")
		f := flagOf(cmd, name)
		if f == nil {
			return cmd, fmt.Errorf("%s has no flag --%s", cmd.CommandPath(), name)
		}
		helped = helped || name == "help"
		// A flag taking a value, given none after =, takes the next word.
		if f.Value.Type() != "bool" && !given {
			i++
		}
	}
	switch {
	case helped || len(args) == 0:
		return cmd, nil
	case cmd.HasSubCommands() && !cmd.Runnable():
		return cmd, fmt.Errorf("%s has no command %s", cmd.CommandPath(), args[0])
	case cmd.Args != nil:
		if err := cmd.Args(cmd, args); err != nil {
			return cmd, fmt.Errorf("%s: %w", cmd.CommandPath(), err)
		}
	}
	return cmd, nil
}

// subcommand is cmd's command called name, by its name or an alias, in
// full: nil when it has none.
func subcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

// flagOf is cmd's flag called name: its own, a global one, or help.
func flagOf(cmd *cobra.Command, name string) *pflag.Flag {
	cmd.InitDefaultHelpFlag()
	for _, flags := range []*pflag.FlagSet{cmd.Flags(), cmd.PersistentFlags(), cmd.InheritedFlags()} {
		if f := flags.Lookup(name); f != nil {
			return f
		}
	}
	return nil
}

// anyHas reports whether cmd, or a command under it, has the flag called
// name.
func anyHas(cmd *cobra.Command, name string) bool {
	if flagOf(cmd, name) != nil {
		return true
	}
	for _, c := range cmd.Commands() {
		if anyHas(c, name) {
			return true
		}
	}
	return false
}

// declarations are the examples of a declarations file text gives: each
// indented block, blank lines within it kept, and each section header it
// names in backticks, as in `[homebrew casks]`, on its own.
func declarations(text string) []string {
	var out, block []string
	end := func() {
		if block != nil {
			out = append(out, strings.TrimSpace(strings.Join(block, "\n"))+"\n")
			block = nil
		}
	}
	for line := range strings.SplitSeq(text, "\n") {
		indented, ok := strings.CutPrefix(line, "    ")
		switch {
		case ok:
			block = append(block, indented)
		case strings.TrimSpace(line) == "" && block != nil:
			block = append(block, "")
		default:
			end()
		}
	}
	end()
	for _, m := range backticked.FindAllStringSubmatch(text, -1) {
		if strings.HasPrefix(m[1], "[") && strings.HasSuffix(m[1], "]") {
			out = append(out, m[1]+"\n")
		}
	}
	return out
}

// readsAsDeclarations reads example as a Mac's declarations file, as kit
// reads kit-config's, its steps' and steps by hand's lines as theirs: what
// doesn't read is the error.
func readsAsDeclarations(t *testing.T, example string) error {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{config.File: twoMacs, config.DeclFile("laptop"): example} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(dir, kinds.ConfigSections())
	if err != nil {
		return err
	}
	own, err := cfg.List(config.StepsKind, "laptop")
	if err != nil {
		return err
	}
	for _, e := range own.Entries {
		if _, err := steps.ParseScript(dir, e); err != nil {
			return err
		}
	}
	byHand, err := cfg.List(config.ManualKind, "laptop")
	if err != nil {
		return err
	}
	for _, e := range byHand.Entries {
		if _, err := steps.ParseManual(dir, e); err != nil {
			return err
		}
	}
	return nil
}
