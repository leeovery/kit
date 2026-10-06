// Package plist reads and writes property lists in XML, as plutil -convert
// xml1 and defaults export write them, and compares their values.
package plist

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Data is a property list's data value: its bytes.
type Data []byte

// Read reads an XML property list into maps, slices, strings, integers
// (int64), reals (float64), booleans, times and Data.
func Read(text string) (any, error) {
	d := xml.NewDecoder(strings.NewReader(text))
	d.Strict = false
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("read the property list: %w", err)
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "plist" {
			return plistValue(d)
		}
	}
}

// plistValue reads the next value in the property list, skipping what isn't
// one; at the end of an enclosing element it returns io.EOF.
func plistValue(d *xml.Decoder) (any, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil, io.EOF
		case xml.StartElement:
			return plistElement(d, t)
		}
	}
}

func plistElement(d *xml.Decoder, start xml.StartElement) (any, error) {
	text := func() (string, error) {
		var s string
		err := d.DecodeElement(&s, &start)
		return strings.TrimSpace(s), err
	}
	switch start.Name.Local {
	case "dict":
		m := make(map[string]any)
		for {
			key, err := plistValue(d)
			if errors.Is(err, io.EOF) {
				return m, nil
			}
			if err != nil {
				return nil, err
			}
			k, ok := key.(plistKey)
			if !ok {
				return nil, errors.New("read the property list: a dict's value without a key")
			}
			v, err := plistValue(d)
			if err != nil {
				return nil, err
			}
			m[string(k)] = v
		}
	case "array":
		var a []any
		for {
			v, err := plistValue(d)
			if errors.Is(err, io.EOF) {
				return a, nil
			}
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
	case "key":
		s, err := text()
		return plistKey(s), err
	case "string":
		return text()
	case "integer":
		s, err := text()
		if err != nil {
			return nil, err
		}
		return strconv.ParseInt(s, 10, 64)
	case "real":
		s, err := text()
		if err != nil {
			return nil, err
		}
		return strconv.ParseFloat(s, 64)
	case "data":
		s, err := text()
		if err != nil {
			return nil, err
		}
		b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
		return Data(b), err
	case "date":
		s, err := text()
		if err != nil {
			return nil, err
		}
		return time.Parse(time.RFC3339, s)
	case "true", "false":
		if err := d.Skip(); err != nil {
			return nil, err
		}
		return start.Name.Local == "true", nil
	default:
		return nil, d.Skip()
	}
}

// plistKey is a dict's key, told apart from a string value.
type plistKey string

// Equal reports whether a and b are the same value: a boolean and the
// integer 1 or 0 count the same, as macOS reads either, and an integer and
// a real of the same value, to single precision; dicts and arrays compare
// by their contents.
func Equal(a, b any) bool {
	if x, ok := number(a); ok {
		y, ok := number(b)
		// defaults write -float stores single precision: 0.001 reads back
		// as 0.0010000000474974513.
		return ok && math.Abs(x-y) <= 1e-6*max(1, math.Abs(x), math.Abs(y))
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && x == y
	case time.Time:
		y, ok := b.(time.Time)
		return ok && x.Equal(y)
	case Data:
		y, ok := b.(Data)
		return ok && bytes.Equal(x, y)
	case []any:
		y, ok := b.([]any)
		return ok && slices.EqualFunc(x, y, Equal)
	case map[string]any:
		y, ok := b.(map[string]any)
		return ok && maps.EqualFunc(x, y, Equal)
	}
	return a == nil && b == nil
}

// number is a boolean, an integer or a real as a number.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// XML is a value as an XML property list's element, on one line, a dict's
// keys in order: what defaults write takes as a value.
func XML(v any) string {
	var b strings.Builder
	writeXML(&b, v)
	return b.String()
}

func writeXML(b *strings.Builder, v any) {
	esc := func(s string) string {
		var e bytes.Buffer
		_ = xml.EscapeText(&e, []byte(s))
		return e.String()
	}
	switch x := v.(type) {
	case bool:
		if x {
			b.WriteString("<true/>")
		} else {
			b.WriteString("<false/>")
		}
	case int64:
		fmt.Fprintf(b, "<integer>%d</integer>", x)
	case float64:
		fmt.Fprintf(b, "<real>%s</real>", strconv.FormatFloat(x, 'g', -1, 64))
	case string:
		b.WriteString("<string>" + esc(x) + "</string>")
	case time.Time:
		b.WriteString("<date>" + x.UTC().Format(time.RFC3339) + "</date>")
	case Data:
		b.WriteString("<data>" + base64.StdEncoding.EncodeToString(x) + "</data>")
	case []any:
		b.WriteString("<array>")
		for _, e := range x {
			writeXML(b, e)
		}
		b.WriteString("</array>")
	case map[string]any:
		b.WriteString("<dict>")
		for _, k := range slices.Sorted(maps.Keys(x)) {
			b.WriteString("<key>" + esc(k) + "</key>")
			writeXML(b, x[k])
		}
		b.WriteString("</dict>")
	}
}

// ReadValue reads one value given as XML, as defaults write takes it: an
// element such as <dict>…</dict>, alone.
func ReadValue(text string) (any, error) {
	return Read("<plist>" + text + "</plist>")
}
