// Package wildmatch matches a pattern against a path as rsync does, so kit
// reads a pattern one way wherever it takes one: rsync's filter rules, and
// the globs of the paths prefs saves.
package wildmatch

import "strings"

// How a pattern matched text, as rsync's wildmatch says, its aborts letting
// a * stop trying further along: one past a / can't match, and one past
// the text's end, nothing can.
const (
	noMatch = iota
	matched
	abortAll
	abortToStarStar
)

// Match reports whether text matches pattern as rsync matches one: *
// any run of characters but /, ** any run at all, ? any character but /,
// [...] one of a class ([!...] or [^...] none of it; ranges and [:alpha:]
// and the like in it), and \ the next character as itself. It's rsync's
// wildmatch.c, a byte at a time, as rsync matches in the C locale.
func Match(pattern, text string) bool {
	return dowild(pattern, text) == matched
}

// dowild is how p, a pattern, matches text, as rsync's dowild says.
func dowild(p, text string) int {
	for len(p) > 0 {
		if text == "" && p[0] != '*' {
			return abortAll
		}
		switch p[0] {
		case '\\':
			if len(p) < 2 || text[0] != p[1] {
				return noMatch
			}
			p, text = p[2:], text[1:]
		case '?':
			if text[0] == '/' {
				return noMatch
			}
			p, text = p[1:], text[1:]
		case '*':
			return star(p, text)
		case '[':
			rest, result := class(p, text[0])
			if result != matched {
				return result
			}
			p, text = rest, text[1:]
		default:
			if text[0] != p[0] {
				return noMatch
			}
			p, text = p[1:], text[1:]
		}
	}
	if text != "" {
		return noMatch
	}
	return matched
}

// star matches p, starting with a * or a **, against text: the rest of the
// pattern tried at each place in text, a * never past a /.
func star(p, text string) int {
	rest := strings.TrimLeft(p, "*")
	deep := len(p)-len(rest) > 1
	if rest == "" {
		if !deep && strings.Contains(text, "/") {
			return noMatch
		}
		return matched
	}
	for ; text != ""; text = text[1:] {
		result := dowild(rest, text)
		switch {
		case result != noMatch && (!deep || result != abortToStarStar):
			return result
		case result == noMatch && !deep && text[0] == '/':
			return abortToStarStar
		}
	}
	return abortAll
}

// class matches c against the class p starts with, at its [: what of p
// follows the class, and matched when c is one of it, else noMatch. A class
// left open, or naming a class C doesn't have, aborts the match, as rsync's
// does.
func class(p string, c byte) (string, int) {
	p = p[1:]
	negated := p != "" && (p[0] == '!' || p[0] == '^')
	if negated {
		p = p[1:]
	}
	in := false
	var prev byte
	for first := true; first || p == "" || p[0] != ']'; first = false {
		if p == "" {
			return "", abortAll
		}
		ch := p[0]
		switch {
		case ch == '\\':
			if len(p) < 2 {
				return "", abortAll
			}
			p = p[1:]
			ch = p[0]
			in = in || c == ch
		case ch == '-' && prev != 0 && len(p) > 1 && p[1] != ']':
			p = p[1:]
			if p[0] == '\\' {
				if len(p) < 2 {
					return "", abortAll
				}
				p = p[1:]
			}
			in = in || prev <= c && c <= p[0]
			ch = 0
		case ch == '[' && len(p) > 1 && p[1] == ':':
			end := strings.IndexByte(p[2:], ']')
			if end < 0 {
				return "", abortAll
			}
			name, ok := strings.CutSuffix(p[2:2+end], ":")
			if !ok {
				in = in || c == '['
				break
			}
			is, known := classes[name]
			if !known {
				return "", abortAll
			}
			in = in || is(c)
			p = p[2+end:]
			ch = 0
		default:
			in = in || c == ch
		}
		prev = ch
		p = p[1:]
	}
	if in == negated || c == '/' {
		return "", noMatch
	}
	return p[1:], matched
}

// classes are the named classes a class may hold, as in [[:digit:]], as C's
// locale has them.
var classes = map[string]func(byte) bool{
	"alnum":  func(c byte) bool { return isAlpha(c) || isDigit(c) },
	"alpha":  isAlpha,
	"blank":  func(c byte) bool { return c == ' ' || c == '\t' },
	"cntrl":  func(c byte) bool { return c < 0x20 || c == 0x7f },
	"digit":  isDigit,
	"graph":  func(c byte) bool { return c > ' ' && c < 0x7f },
	"lower":  func(c byte) bool { return c >= 'a' && c <= 'z' },
	"print":  func(c byte) bool { return c >= ' ' && c < 0x7f },
	"punct":  func(c byte) bool { return c > ' ' && c < 0x7f && !isAlpha(c) && !isDigit(c) },
	"space":  func(c byte) bool { return c == ' ' || c >= '\t' && c <= '\r' },
	"upper":  func(c byte) bool { return c >= 'A' && c <= 'Z' },
	"xdigit": func(c byte) bool { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' },
}

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
