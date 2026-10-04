package mcp

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/leeovery/kit/internal/config"
)

// off is kit's own option, first on a server's line: declared, but not to
// be installed.
const off = "--off"

// readValue reads a server's line after its name: claude mcp add's options
// (--transport and a URL, --header; or --env, then -- and the command), or
// JSON for what they can't say; either after --off, when it's declared off.
func readValue(value string) (map[string]any, bool, error) {
	isOff := false
	if rest, ok := strings.CutPrefix(value, off); ok && (rest == "" || rest[0] == ' ') {
		isOff, value = true, strings.TrimSpace(rest)
	}
	if strings.HasPrefix(value, "{") {
		var def map[string]any
		d := json.NewDecoder(strings.NewReader(value))
		d.UseNumber()
		if err := d.Decode(&def); err != nil || d.More() {
			return nil, false, fmt.Errorf("its JSON doesn't read: %v", err)
		}
		return def, isOff, nil
	}
	words, err := config.Words(value)
	if err != nil {
		return nil, false, err
	}
	var transport, url, command string
	var args []any
	headers, env := map[string]any{}, map[string]any{}
	for i := 0; i < len(words); i++ {
		w := words[i]
		next := func() (string, error) {
			if i+1 >= len(words) {
				return "", fmt.Errorf("%s needs a value after it", w)
			}
			i++
			return words[i], nil
		}
		switch w {
		case "--transport", "-t":
			if transport, err = next(); err != nil {
				return nil, false, err
			}
		case "--header", "-H":
			h, err := next()
			if err != nil {
				return nil, false, err
			}
			k, v, ok := strings.Cut(h, ":")
			if !ok || strings.TrimSpace(k) == "" {
				return nil, false, fmt.Errorf("the header %q isn't a name, a colon and a value", h)
			}
			headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		case "--env", "-e":
			e, err := next()
			if err != nil {
				return nil, false, err
			}
			k, v, ok := strings.Cut(e, "=")
			if !ok || k == "" {
				return nil, false, fmt.Errorf("the variable %q isn't a name, = and a value", e)
			}
			env[k] = v
		case "--":
			if i+1 >= len(words) {
				return nil, false, errors.New("-- needs the command after it")
			}
			command = words[i+1]
			for _, a := range words[i+2:] {
				args = append(args, a)
			}
			i = len(words)
		default:
			if strings.HasPrefix(w, "-") {
				return nil, false, fmt.Errorf("kit doesn't know the option %s", w)
			}
			if url != "" {
				return nil, false, fmt.Errorf("%s and %s: one URL, or a command after --", url, w)
			}
			url = w
		}
	}
	def := map[string]any{}
	switch {
	case command != "" && url != "":
		return nil, false, errors.New("a URL and a command: one or the other")
	case command != "":
		if transport != "" && transport != "stdio" {
			return nil, false, fmt.Errorf("a command runs over stdio, not %s", transport)
		}
		if len(headers) > 0 {
			return nil, false, errors.New("headers go to a URL's server, not a command's")
		}
		def["type"], def["command"] = "stdio", command
		if len(args) > 0 {
			def["args"] = args
		}
		if len(env) > 0 {
			def["env"] = env
		}
	case url != "":
		if len(env) > 0 {
			return nil, false, errors.New("variables go to a command's server, not a URL's")
		}
		def["type"], def["url"] = cmp.Or(transport, "http"), url
		if len(headers) > 0 {
			def["headers"] = headers
		}
	default:
		return nil, false, errors.New("neither a URL nor a command after --")
	}
	return def, isOff, nil
}

// writeValue is a server's definition as its line has it after its name:
// claude mcp add's options when they say all of it, else JSON; after --off
// when it's declared off.
func writeValue(def map[string]any, isOff bool) (string, error) {
	var words []string
	if isOff {
		words = append(words, off)
	}
	if opts, ok := options(def); ok {
		return strings.Join(append(words, opts...), " "), nil
	}
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(def); err != nil {
		return "", err
	}
	return strings.Join(append(words, strings.TrimSuffix(b.String(), "\n")), " "), nil
}

// options are def as claude mcp add's options, each word quoted as it
// needs: none, and false, when they can't say all of it.
func options(def map[string]any) ([]string, bool) {
	text := func(k string) (string, bool) { s, ok := def[k].(string); return s, ok }
	stringMap := func(k string) (map[string]string, bool) {
		out := map[string]string{}
		m, ok := def[k].(map[string]any)
		if def[k] != nil && !ok {
			return nil, false
		}
		for name, v := range m {
			s, ok := v.(string)
			if !ok {
				return nil, false
			}
			out[name] = s
		}
		return out, true
	}
	known := map[string]bool{"type": true, "url": true, "headers": true, "command": true, "args": true, "env": true}
	for k := range def {
		if !known[k] {
			return nil, false
		}
	}
	typ, _ := text("type")
	var words []string
	if command, ok := text("command"); ok && (typ == "" || typ == "stdio") && def["url"] == nil && def["headers"] == nil {
		env, ok := stringMap("env")
		if !ok {
			return nil, false
		}
		for _, k := range slices.Sorted(maps.Keys(env)) {
			words = append(words, "--env", config.Quote(k+"="+env[k]))
		}
		words = append(words, "--", config.Quote(command))
		args, _ := def["args"].([]any)
		if def["args"] != nil && args == nil {
			return nil, false
		}
		for _, a := range args {
			s, ok := a.(string)
			if !ok {
				return nil, false
			}
			words = append(words, config.Quote(s))
		}
		return words, true
	}
	url, ok := text("url")
	if !ok || (typ != "http" && typ != "sse") || def["command"] != nil || def["args"] != nil || def["env"] != nil {
		return nil, false
	}
	headers, ok := stringMap("headers")
	if !ok {
		return nil, false
	}
	words = append(words, "--transport", typ, config.Quote(url))
	for _, k := range slices.Sorted(maps.Keys(headers)) {
		words = append(words, "--header", config.Quote(k+": "+headers[k]))
	}
	return words, true
}

// SetOff is a server's line after its name, declared off, or on.
func SetOff(value string, isOff bool) (string, error) {
	def, _, err := readValue(value)
	if err != nil {
		return "", err
	}
	return writeValue(def, isOff)
}
