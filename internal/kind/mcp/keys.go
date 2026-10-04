package mcp

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// secretName is what a header, variable or flag holding a key is called.
var secretName = regexp.MustCompile(`(?i)(key|token|secret|password|authorization|bearer)`)

// placeholder reports whether a value names its key, as ${VAR}, rather than
// holding it.
func placeholder(v any) bool {
	text, ok := v.(string)
	return !ok || text == "" || strings.Contains(text, "${")
}

// PlainKeys finds where a definition holds a key in plain text rather than
// naming it as ${VAR}: a header or a variable named like a key, a token or a
// secret; such a parameter of its URL; such a flag among its arguments.
func PlainKeys(def map[string]any) []string {
	var found []string
	for _, field := range []string{"headers", "env"} {
		values, _ := def[field].(map[string]any)
		for name, v := range values {
			if secretName.MatchString(name) && !placeholder(v) {
				found = append(found, field+"."+name)
			}
		}
	}
	if text, ok := def["url"].(string); ok {
		if u, err := url.Parse(text); err == nil {
			for name, vs := range u.Query() {
				if secretName.MatchString(name) && slices.ContainsFunc(vs, func(v string) bool { return !placeholder(v) }) {
					found = append(found, "url's "+name)
				}
			}
		}
	}
	args, _ := def["args"].([]any)
	for i, a := range args {
		text, _ := a.(string)
		flag, value, joined := strings.Cut(text, "=")
		if !strings.HasPrefix(flag, "-") || !secretName.MatchString(flag) {
			continue
		}
		if !joined {
			if i+1 >= len(args) {
				continue
			}
			value, _ = args[i+1].(string)
		}
		if !placeholder(value) {
			found = append(found, fmt.Sprintf("args' %s", flag))
		}
	}
	slices.Sort(found)
	return found
}
