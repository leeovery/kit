package config

import (
	"errors"
	"strings"
)

// splitComment splits a line into what's before its comment and the comment:
// a # at the line's start, or after a space or tab. Both are trimmed.
func splitComment(line string) (text, comment string) {
	for i, r := range line {
		if r == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		}
	}
	return strings.TrimSpace(line), ""
}

// splitCommand splits a command's line into its name, its options and its
// note: a # that starts a word outside quotes starts the note.
func splitCommand(line string) (name, value, note string, err error) {
	text := line
	quote := rune(0)
	escaped := false
	for i, r := range line {
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
		case r == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			text, note = line[:i], strings.TrimSpace(line[i+1:])
		}
		if text != line {
			break
		}
	}
	text = strings.TrimSpace(text)
	name, value, _ = strings.Cut(text, " ")
	value = strings.TrimSpace(value)
	if _, err := Words(value); err != nil {
		return "", "", "", err
	}
	return name, value, note, nil
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
