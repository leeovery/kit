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

// npmAuth matches the value of one of npm's credentials, given as a
// setting, as in --//registry.npmjs.org/:_authToken=value; userinfo, an
// address's userinfo, as in https://user:password@host or
// https://token@host.
var (
	npmAuth  = regexp.MustCompile(`(?i)\b(_authToken|_auth|_password)=(['"]?)[^\s'"]+`)
	userinfo = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)([^/\s@]+)@`)
)

// Text returns text with each of secrets, anything shaped like a token,
// npm's credentials and an address's password or token replaced by
// Placeholder.
func Text(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, Placeholder)
		}
	}
	text = npmAuth.ReplaceAllString(text, "${1}=${2}"+Placeholder)
	text = userinfo.ReplaceAllStringFunc(text, hideUserinfo)
	return tokenShaped.ReplaceAllStringFunc(text, func(match string) string {
		if prefix := bearerPrefix(match); prefix != "" {
			return prefix + Placeholder
		}
		return Placeholder
	})
}

// hideUserinfo is match, an address up to its userinfo's @, its password
// hidden, its user kept; or, a userinfo of one part, that part hidden, as
// a token given as the user, unless it's git, which names no one.
func hideUserinfo(match string) string {
	m := userinfo.FindStringSubmatch(match)
	scheme, info := m[1], m[2]
	if user, _, ok := strings.Cut(info, ":"); ok {
		return scheme + user + ":" + Placeholder + "@"
	}
	if strings.EqualFold(info, "git") {
		return match
	}
	return scheme + Placeholder + "@"
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
