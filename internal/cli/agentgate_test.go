package cli_test

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/leeovery/kit/internal/cli"
	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/guide"
)

// The guide can't drift from kit's commands: every command it names, in
// backticks, as `kit step add <name> "<what it does>"`, is one of kit's,
// taking what it's given, and every flag it names is one that command
// takes, or, named alone, as `--secret`, one some command takes.
func TestTheGuideNamesOnlyKitsCommandsAndFlags(t *testing.T) {
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
	root := kitCommands()
	named := 0
	for where, text := range texts {
		spans := commandSpans(text)
		named += len(spans)
		for _, wrong := range namedWrongly(root, spans) {
			t.Errorf("%s: %s", where, wrong)
		}
	}
	if named < 50 {
		t.Errorf("the guide names %d commands and flags in backticks, want them all found", named)
	}
}

// The check finds what's wrong: a command kit hasn't, a flag a command
// doesn't take, arguments a command doesn't take, a flag no command takes,
// and says so.
func TestTheGuidesCheckFindsWhatsWrong(t *testing.T) {
	root := kitCommands()
	tests := []struct{ span, want string }{
		{"kit step add <name> \"<what it does>\" --after cask:firefox --sudo", ""},
		{"kit apply --plan", ""},
		{"kit secret add <name> --stdin --json", ""},
		{"kit agent order <name>", ""},
		{"kit reconcile brew:jq --adopt", ""},
		{"kit step add --help", ""},
		{"--shared", ""},
		{"kit stpe add", "`kit stpe add`: kit: unknown command \"stpe\" for \"kit\""},
		{"kit step frob <name>", "`kit step frob <name>`: kit step has no command frob"},
		{"kit apply --frob", "`kit apply --frob`: kit apply has no flag --frob"},
		{"kit agent order <name> <name>", "`kit agent order <name> <name>`: kit agent order: accepts 1 arg(s), received 2"},
		{"kit agent ordr <name>", "`kit agent ordr <name>`: kit agent: no topic ordr"},
		{"--nosuch", "`--nosuch`: no command of kit's has the flag --nosuch"},
	}
	for _, tt := range tests {
		got := strings.Join(namedWrongly(root, []string{tt.span}), "\n")
		if !strings.HasPrefix(got, tt.want) || (tt.want == "") != (got == "") {
			t.Errorf("%s: found %q, want %q", tt.span, got, tt.want)
		}
	}
}

// kitCommands is kit's command tree, to look commands up in.
func kitCommands() *cobra.Command {
	return cli.NewRootCommand(cli.Deps{Version: "0.1.0", Stdout: io.Discard, Stderr: io.Discard, Getenv: func(string) string { return "" }})
}

var backticked = regexp.MustCompile("`([^`]+)`")

// commandSpans are what text has in backticks that names kit's commands or
// flags: a span starting kit and a space, or a flag. A span broken across
// lines is joined.
func commandSpans(text string) []string {
	joined := regexp.MustCompile(`\n\s*`).ReplaceAllString(text, " ")
	var spans []string
	for _, m := range backticked.FindAllStringSubmatch(joined, -1) {
		if strings.HasPrefix(m[1], "kit ") || strings.HasPrefix(m[1], "--") {
			spans = append(spans, m[1])
		}
	}
	return spans
}

// namedWrongly is what's wrong with spans, against root, a line each.
func namedWrongly(root *cobra.Command, spans []string) []string {
	var wrong []string
	for _, span := range spans {
		if err := checkSpan(root, span); err != nil {
			wrong = append(wrong, fmt.Sprintf("`%s`: %v", span, err))
		}
	}
	return wrong
}

// checkSpan checks one span: a flag alone must be some command's; a command
// is its words up to the first that isn't the name of a command under the
// one before, which must take the words after them, but its flags, each
// of which it must take.
func checkSpan(root *cobra.Command, span string) error {
	words, err := config.Words(span)
	if err != nil {
		return err
	}
	if name, ok := strings.CutPrefix(words[0], "--"); ok {
		if !anyHas(root, name) {
			return fmt.Errorf("no command of kit's has the flag --%s", name)
		}
		return nil
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
			return fmt.Errorf("%s has no flag --%s", cmd.CommandPath(), name)
		}
		helped = helped || name == "help"
		// A flag taking a value, given none after =, takes the next word.
		if f.Value.Type() != "bool" && !given {
			i++
		}
	}
	switch {
	case helped || len(args) == 0:
		return nil
	case cmd.HasSubCommands() && !cmd.Runnable():
		return fmt.Errorf("%s has no command %s", cmd.CommandPath(), args[0])
	case cmd.Args != nil:
		if err := cmd.Args(cmd, args); err != nil {
			return fmt.Errorf("%s: %w", cmd.CommandPath(), err)
		}
	}
	return nil
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
