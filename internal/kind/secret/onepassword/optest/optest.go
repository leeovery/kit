// Package optest stands in for 1Password's CLI, op, in tests, through
// runnertest's fake.
package optest

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/leeovery/kit/internal/runner/runnertest"
)

// reference is a reference in an op inject template, in its braces.
var reference = regexp.MustCompile(`\{\{(.+?)\}\}`)

// Holds scripts fake's op as 1Password holding values, each by its
// reference, answers it, as op's does: op read, each value ended by a
// newline unless it ends with one already, or its bytes exactly in the file
// --out-file names, in the folder op runs in; op inject, each reference in
// the template it reads replaced by its value, each byte that isn't UTF-8
// turned into U+FFFD, and failing whole for a reference it doesn't hold.
func Holds(fake *runnertest.Fake, values map[string]string) {
	for ref, value := range values {
		piped := value
		if !strings.HasSuffix(piped, "\n") {
			piped += "\n"
		}
		fake.On("op", "read", ref).Prints(piped)
		fake.On("op", "read", "--force", "--file-mode", "0600", "--out-file", "value", ref).Writes("value", value)
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
		return string([]rune(out)), nil
	})
}

// Attaches scripts fake's op item get of item, op://vault/item, as
// 1Password keeping attachments of names in it, each the item's own, or a
// section's as Section/name, and no fields.
func Attaches(fake *runnertest.Fake, item string, names ...string) {
	vault, title, _ := strings.Cut(strings.TrimPrefix(item, "op://"), "/")
	files := make([]map[string]any, len(names))
	for i, name := range names {
		files[i] = map[string]any{"id": fmt.Sprint("file", i), "name": name}
		if section, own, ok := strings.Cut(name, "/"); ok {
			files[i]["name"], files[i]["section"] = own, map[string]string{"id": "s-" + section, "label": section}
		}
	}
	doc, _ := json.Marshal(map[string]any{"title": title, "fields": []any{}, "files": files})
	fake.On("op", "item", "get", title, "--vault", vault, "--format", "json").Prints(string(doc))
}
