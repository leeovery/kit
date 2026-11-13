// Package plist reads and writes property lists in XML, as plutil -convert
// xml1 and defaults export write them, and compares their values.
package plist

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Data is a property list's data value: its bytes.
type Data []byte

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
	case int64, uint64:
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
	return Decode([]byte("<plist>" + text + "</plist>"))
}
