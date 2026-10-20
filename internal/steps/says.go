package steps

import (
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/config"
	"github.com/leeovery/kit/internal/runner"
)

// Readings are the config's own lines' readings, each as its reader reads
// it, in parts (config.Section.Says), so lines saying the same, however
// written, compare alike.
var Readings = []config.Section{
	{Kind: config.StepsKind, Says: scriptSays},
	{Kind: config.ManualKind, Says: manualSays},
	{Kind: config.ChecksKind, Says: ownSays},
	{Kind: config.HourlyKind, Says: ownSays},
	{Kind: config.NightlyKind, Says: ownSays},
	{Kind: config.BackupsKind, Says: backupSays},
}

// scriptSays is what a [steps] line says: what it does; each step it comes
// after and before; each secret it needs; and --sudo.
func scriptSays(e config.Entry) ([]string, error) {
	s, err := ParseScript("", e)
	if err != nil {
		return nil, err
	}
	parts := slices.Concat([]string{config.Quote(s.Does)}, options("--after", s.After), options("--before", s.Before), options("--secret", s.Secrets))
	if s.Admin {
		parts = append(parts, "--sudo")
	}
	return parts, nil
}

// manualSays is what a [manual] line says: what to do; each step it comes
// after and before; its group; and the command saying whether it's done.
func manualSays(e config.Entry) ([]string, error) {
	m, err := ParseManual("~", e)
	if err != nil {
		return nil, err
	}
	parts := slices.Concat([]string{config.Quote(m.Do)}, options("--after", m.After), options("--before", m.Before))
	if m.Group != "" {
		parts = append(parts, "--group "+config.Quote(m.Group))
	}
	if m.Command != nil {
		parts = append(parts, "--test "+commandSaid(*m.Command))
	}
	return parts, nil
}

// ownSays is what a check's or a job's line says: its command.
func ownSays(e config.Entry) ([]string, error) {
	cmd, err := OwnCommand("~", e.Value, 0)
	if err != nil {
		return nil, err
	}
	return []string{"-- " + commandSaid(cmd)}, nil
}

// backupSays is what a backup tool's line says: each step its check comes
// after and before.
func backupSays(e config.Entry) ([]string, error) {
	b, err := ReadBackup(e.Name, e.Value)
	if err != nil {
		return nil, err
	}
	return slices.Concat(options("--after", b.After), options("--before", b.Before)), nil
}

// options are flag and each of values, a part each.
func options(flag string, values []string) []string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = flag + " " + config.Quote(v)
	}
	return parts
}

// commandSaid is cmd as its line has it, each word quoted.
func commandSaid(cmd runner.Command) string {
	words := make([]string, 0, len(cmd.Args)+1)
	for _, w := range append([]string{cmd.Name}, cmd.Args...) {
		words = append(words, config.Quote(w))
	}
	return strings.Join(words, " ")
}
