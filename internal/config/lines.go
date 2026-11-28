package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// splitComment splits a line into what's before its comment and the comment:
// a # at the line's start, or after a space or tab. Both are trimmed.
func splitComment(line string) (text, comment string) {
	if i := commentAt(line); i >= 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
	}
	return strings.TrimSpace(line), ""
}

// commentAt is where line's comment starts: its first # at the line's
// start, or after a space or tab; -1 when it has none.
func commentAt(line string) int {
	for i, r := range line {
		if r == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return i
		}
	}
	return -1
}

// splitPathOptions splits a line of a path and its options into the path,
// up to the first -- after a space or tab; its options, from there; and its
// note: a comment in the path, as splitComment finds one, or a # starting a
// word among the options outside quotes.
func splitPathOptions(line string) (path, options, note string, err error) {
	end := optionsAt(line)
	if i := commentAt(line[:end]); i >= 0 {
		return strings.TrimSpace(line[:i]), "", strings.TrimSpace(line[i+1:]), nil
	}
	options, note = splitNote(line[end:])
	if _, err := Words(options); err != nil {
		return "", "", "", err
	}
	return strings.TrimSpace(line[:end]), options, note, nil
}

// optionsAt is where line's options start: its first -- after a space or
// tab; its length when it has none.
func optionsAt(line string) int {
	for i := 1; i+1 < len(line); i++ {
		if line[i] == '-' && line[i+1] == '-' && (line[i-1] == ' ' || line[i-1] == '\t') {
			return i
		}
	}
	return len(line)
}

// splitCommand splits a command's line into its name, its options and its
// note: a # that starts a word outside quotes starts the note. The name is
// the line's first word, as Words reads it: a name with a space in it is
// quoted. No name holds a backslash, which Words would read away.
func splitCommand(line string) (name, value, note string, err error) {
	text, note := splitNote(line)
	end := unquotedAt(text, func(_ int, r rune) bool { return r == ' ' || r == '\t' })
	if strings.ContainsRune(text[:end], '\\') {
		return "", "", "", fmt.Errorf("%s: a name holds no backslash", text[:end])
	}
	words, err := Words(text[:end])
	if err != nil {
		return "", "", "", err
	}
	if len(words) > 0 {
		name = words[0]
	}
	value = strings.TrimSpace(text[end:])
	if _, err := Words(value); err != nil {
		return "", "", "", err
	}
	return name, value, note, nil
}

// unquotedAt is where in text the first character outside quotes, and not
// escaped, that at says is at: text's length when there's none.
func unquotedAt(text string, at func(i int, r rune) bool) int {
	return scan(text, func(i int, r rune, bare bool) bool { return bare && at(i, r) })
}

// scan gives visit each of text's characters in turn, and whether it's
// bare: outside quotes, not escaped, and not itself a quote or a backslash
// that escapes, as Words reads them. It stops where visit says to, and
// returns where: text's length when visit never does.
func scan(text string, visit func(i int, r rune, bare bool) (stop bool)) int {
	quote := rune(0)
	escaped := false
	for i, r := range text {
		bare := false
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		default:
			bare = true
		}
		if visit(i, r, bare) {
			return i
		}
	}
	return len(text)
}

// squeeze is text with each run of whitespace outside quotes one space, and
// none at its ends: what's quoted is kept as it is.
func squeeze(text string) string {
	var b strings.Builder
	space := false
	scan(text, func(_ int, r rune, bare bool) bool {
		if bare && unicode.IsSpace(r) {
			space = true
			return false
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
		return false
	})
	return b.String()
}

// lineWord is word as a line holds it, for Words to read back: as it is
// when Words reads it so, else quoted (Quote). Unlike Quote, it leaves as
// it is a word Words reads unchanged, such as a path with brackets.
func lineWord(word string) string {
	if words, err := Words(word); err == nil && len(words) == 1 && words[0] == word {
		return word
	}
	return Quote(word)
}

// Words splits text into words as a shell does, without expanding anything:
// at spaces outside quotes; double quotes keep spaces, and a backslash in them
// keeps a quote or backslash; single quotes keep everything; a backslash
// outside quotes keeps the next character.
func Words(text string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	quote := rune(0)
	escaped := false
	for _, r := range text {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			escaped, inWord = true, true
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	switch {
	case quote != 0:
		return nil, errors.New("a quote isn't closed")
	case escaped:
		return nil, errors.New("a backslash ends the line")
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// Quote is word as Words reads it back: as it is when it holds nothing a
// shell treats specially, else in double quotes.
func Quote(word string) string {
	if word != "" && strings.IndexFunc(word, func(r rune) bool { return !plain(r) }) < 0 {
		return word
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(word) + `"`
}

// plain reports whether r reads as itself in a word, unquoted.
func plain(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%+=:,./_~^${}*-", r)
}
