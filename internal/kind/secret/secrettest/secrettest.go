// Package secrettest stands in for 1Password in tests, through runnertest's
// fake.
package secrettest

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/leeovery/kit/internal/runner/runnertest"
)

// reference is a reference in an op inject template, in its braces.
var reference = regexp.MustCompile(`\{\{(.+?)\}\}`)

// Holds scripts fake's op as 1Password holding values, each by its
// reference, answers it: op read, each value ended by a newline; op inject,
// each reference in the template it reads replaced by its value, and failing
// whole for a reference it doesn't hold.
func Holds(fake *runnertest.Fake, values map[string]string) {
	for ref, value := range values {
		fake.On("op", "read", ref).Prints(value + "\n")
	}
	fake.On("op", "inject").Filters(func(template string) (string, error) {
		var missing []string
		out := reference.ReplaceAllStringFunc(template, func(braced string) string {
			ref := strings.TrimSpace(reference.FindStringSubmatch(braced)[1])
			value, ok := values[ref]
			if !ok {
				missing = append(missing, ref)
			}
			return value
		})
		if len(missing) > 0 {
			return "", fmt.Errorf("[ERROR] could not read secret %s", strings.Join(missing, ", "))
		}
		return out, nil
	})
}
