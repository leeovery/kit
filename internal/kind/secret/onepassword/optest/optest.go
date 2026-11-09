// Package optest stands in for 1Password's CLI, op, in tests, through
// runnertest's fake.
package optest

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/runner/runnertest"
)

// reference is a reference in an op inject template, in its braces.
var reference = regexp.MustCompile(`\{\{(.+?)\}\}`)

// Holds scripts fake's op as 1Password holding values, each by its
// reference, answers it, as op's does, a reference in any case: op read,
// each value ended by a newline unless it ends with one already, or its
// bytes exactly in the file --out-file names; op inject, each reference in
// the template it reads replaced by its value, each byte that isn't UTF-8
// turned into U+FFFD, and failing whole for a reference it doesn't hold.
func Holds(fake *runnertest.Fake, values map[string]string) {
	for ref, value := range values {
		piped := value
		if !strings.HasSuffix(piped, "\n") {
			piped += "\n"
		}
		fake.OnMatch("op", func(args []string) bool {
			return len(args) == 2 && args[0] == "read" && strings.EqualFold(args[1], ref)
		}).Prints(piped)
		fake.OnMatch("op", func(args []string) bool {
			return len(args) == 7 && slices.Equal(args[:5], toFile) && strings.EqualFold(args[6], ref)
		}).Writes("--out-file", value)
	}
	fake.On("op", "inject").Filters(func(template string) (string, error) {
		var missing []string
		out := reference.ReplaceAllStringFunc(template, func(braced string) string {
			ref := strings.TrimSpace(reference.FindStringSubmatch(braced)[1])
			value, ok := held(values, ref)
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

// toFile starts op read's arguments as kit reads a value into a file, its
// path and the reference after them.
var toFile = []string{"read", "--force", "--file-mode", "0600", "--out-file"}

// held is the value of values op finds by ref, in any case.
func held(values map[string]string, ref string) (string, bool) {
	for r, v := range values {
		if strings.EqualFold(r, ref) {
			return v, true
		}
	}
	return "", false
}

// Attaches scripts fake's op item get of item, op://vault/item, as
// 1Password keeping attachments of names in it, and no fields (Keeps).
func Attaches(fake *runnertest.Fake, item string, names ...string) {
	Keeps(fake, item, nil, names)
}

// Keeps scripts fake's op item get of item, op://vault/item, as 1Password
// keeping fields and attachments of names in it, each the item's own, or
// a section's as Section/name.
func Keeps(fake *runnertest.Fake, item string, fields, files []string) {
	vault, title, _ := strings.Cut(strings.TrimPrefix(item, "op://"), "/")
	doc, _ := json.Marshal(map[string]any{"title": title, "fields": listed("field", "label", fields), "files": listed("file", "name", files)})
	fake.On("op", "item", "get", title, "--vault", vault, "--format", "json").Prints(string(doc))
}

// listed are names as op item get lists an item's fields or files, each
// with an id of kind, its name under key, and its section's id and label.
func listed(kind, key string, names []string) []map[string]any {
	out := make([]map[string]any, len(names))
	for i, name := range names {
		out[i] = map[string]any{"id": fmt.Sprint(kind, i), key: name}
		if section, own, ok := strings.Cut(name, "/"); ok {
			out[i][key], out[i]["section"] = own, map[string]string{"id": "s-" + section, "label": section}
		}
	}
	return out
}
