// Package redact hides secrets in what kit shows and logs: values it was
// handed, and anything shaped like a token of the services kit reaches.
package redact

import (
	"regexp"
	"strings"
)

// Placeholder stands in for each secret hidden.
const Placeholder = "[redacted]"

// tokenShaped matches the tokens of the services kit's programs reach:
// GitHub's (ghp_, gho_, ghu_, ghs_, ghr_ and fine-grained github_pat_),
// Claude's (sk-ant-), 1Password service accounts' (ops_), and any bearer
// token in a header.
var tokenShaped = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-ant-[A-Za-z0-9_-]+|ops_[A-Za-z0-9_-]{20,})|(?i:(bearer|token)\s+)[A-Za-z0-9._~+/=-]{16,}`)

// Text returns text with each of secrets, and anything shaped like a token,
// replaced by Placeholder.
func Text(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, Placeholder)
		}
	}
	return tokenShaped.ReplaceAllStringFunc(text, func(match string) string {
		if prefix := bearerPrefix(match); prefix != "" {
			return prefix + Placeholder
		}
		return Placeholder
	})
}

// bearerPrefix is the "Bearer " or "token " a match starts with, kept so the
// header still reads as one: "" for a token matched by its shape.
func bearerPrefix(match string) string {
	lower := strings.ToLower(match)
	for _, word := range []string{"bearer", "token"} {
		if strings.HasPrefix(lower, word) {
			rest := match[len(word):]
			return match[:len(word)] + rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))]
		}
	}
	return ""
}
