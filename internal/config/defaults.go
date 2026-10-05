package config

import (
	"errors"
	"fmt"
	"strings"
)

// Setting is where a macOS setting is: its domain, its key, and for an
// entry of a dict, the entry's key; CurrentHost for one kept for this Mac's
// host alone.
type Setting struct {
	CurrentHost bool
	Domain, Key string
	// Entry is the dict entry's key, for a setting declared by -dict-add.
	Entry string
}

// currentHostName starts the name of a setting kept for this Mac's host.
const currentHostName = "currentHost:"

// Name is the setting's name, as kit calls it: the domain, the key and any
// entry's key, joined by colons, after currentHost: for one of this host,
// as in com.apple.dock:tilesize.
func (s Setting) Name() string {
	name := s.Domain + ":" + s.Key
	if s.Entry != "" {
		name += ":" + s.Entry
	}
	if s.CurrentHost {
		name = currentHostName + name
	}
	return name
}

// ParseSetting reads a setting's name.
func ParseSetting(name string) (Setting, error) {
	var s Setting
	if rest, ok := strings.CutPrefix(name, currentHostName); ok {
		s.CurrentHost, name = true, rest
	}
	domain, rest, ok := strings.Cut(name, ":")
	if !ok || domain == "" || rest == "" {
		return s, fmt.Errorf("%q isn't a setting's name: a domain, a colon and a key", name)
	}
	s.Domain, s.Key = domain, rest
	return s, nil
}

// defaultsLine reads a macOS setting's line, as words: -currentHost for
// one of this host, then the domain, the key and the value as defaults
// write takes it. It's the setting, and its value's words without the
// entry's key, quoted: -int 60, or -dict-add "<dict>…</dict>".
func defaultsLine(words []string) (Setting, string, error) {
	var s Setting
	if len(words) > 0 && words[0] == "-currentHost" {
		s.CurrentHost, words = true, words[1:]
	}
	if len(words) < 3 {
		return s, "", errors.New("a setting is a domain, a key and a value, as defaults write takes them")
	}
	s.Domain, s.Key, words = words[0], words[1], words[2:]
	switch words[0] {
	case "-bool", "-boolean", "-int", "-integer", "-float", "-string":
		if len(words) != 2 {
			return s, "", fmt.Errorf("%s takes one value", words[0])
		}
	case "-dict-add":
		if len(words) != 3 {
			return s, "", errors.New("-dict-add takes an entry's key and its value as XML")
		}
		s.Entry, words = words[1], []string{words[0], words[2]}
	default:
		if len(words) != 1 || !strings.HasPrefix(words[0], "<") {
			return s, "", fmt.Errorf("%q: a value is -bool, -int, -float or -string and the value, -dict-add, or a value as XML", strings.Join(words, " "))
		}
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = Quote(w)
	}
	return s, strings.Join(quoted, " "), nil
}

// defaultsText is a setting's line, from its name and its value as an
// entry carries it.
func defaultsText(name, value string) (string, error) {
	s, err := ParseSetting(name)
	if err != nil {
		return "", err
	}
	vw, err := Words(value)
	if err != nil {
		return "", err
	}
	if len(vw) > 0 && vw[0] == "-dict-add" {
		// A dict's entry: its key is the name's last part.
		colon := strings.LastIndex(s.Key, ":")
		if colon <= 0 {
			return "", fmt.Errorf("%s: -dict-add sets a dict's entry, named as in domain:key:entry", name)
		}
		s.Key, s.Entry = s.Key[:colon], s.Key[colon+1:]
	}
	var words []string
	if s.CurrentHost {
		words = append(words, "-currentHost")
	}
	words = append(words, s.Domain, s.Key)
	if s.Entry != "" {
		if len(vw) != 2 {
			return "", fmt.Errorf("%s is a dict's entry: its value is -dict-add and the entry's XML", name)
		}
		words = append(words, vw[0], s.Entry, vw[1])
	} else {
		words = append(words, vw...)
	}
	if _, _, err := defaultsLine(words); err != nil {
		return "", err
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = Quote(w)
	}
	return strings.Join(quoted, " "), nil
}

// defaultsName is the name a setting's line declares: "" when it doesn't
// read.
func defaultsName(line string) string {
	text, _ := splitNote(line)
	words, err := Words(text)
	if err != nil {
		return ""
	}
	s, _, err := defaultsLine(words)
	if err != nil {
		return ""
	}
	return s.Name()
}

// splitNote splits a line into its text and its note: a # that starts a
// word outside quotes starts the note.
func splitNote(line string) (text, note string) {
	text = line
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
			return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		}
	}
	return strings.TrimSpace(text), ""
}
