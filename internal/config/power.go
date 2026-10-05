package config

import (
	"errors"
	"fmt"
	"strings"
)

// PowerSources are pmset's power sources, by its flag: the name a power
// setting's name starts with.
var PowerSources = map[string]string{"-a": "all", "-b": "battery", "-c": "charger", "-u": "ups"}

// powerLine reads a power setting's line, as words: its name (the source
// and the setting, as in charger:sleep) and its value.
func powerLine(words []string) (name, value string, err error) {
	if len(words) != 3 {
		return "", "", errors.New("a power setting is the power source, the setting and its value, as pmset takes them: -c sleep 0")
	}
	source, ok := PowerSources[words[0]]
	if !ok {
		return "", "", fmt.Errorf("%q isn't a power source: -a every one, -b the battery, -c the charger, -u a UPS", words[0])
	}
	return source + ":" + words[1], words[2], nil
}

// powerText is a power setting's line, from its name and value.
func powerText(name, value string) (string, error) {
	source, setting, ok := strings.Cut(name, ":")
	if !ok {
		return "", fmt.Errorf("%q isn't a power setting's name: the source, a colon and the setting", name)
	}
	for flag, s := range PowerSources {
		if s == source {
			if _, _, err := powerLine([]string{flag, setting, value}); err != nil {
				return "", err
			}
			return flag + " " + Quote(setting) + " " + Quote(value), nil
		}
	}
	return "", fmt.Errorf("%q isn't a power source: all, battery, charger or ups", source)
}

// powerName is the name a power setting's line declares: "" when it
// doesn't read.
func powerName(line string) string {
	text, _ := splitNote(line)
	words, err := Words(text)
	if err != nil {
		return ""
	}
	name, _, err := powerLine(words)
	if err != nil {
		return ""
	}
	return name
}
