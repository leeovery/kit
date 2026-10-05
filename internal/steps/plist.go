package steps

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// readPlist reads an XML property list, as plutil -convert xml1 writes one,
// into maps, slices, strings, numbers, booleans and times. Data is left out.
func readPlist(text string) (any, error) {
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
	case "integer", "real":
		s, err := text()
		if err != nil {
			return nil, err
		}
		return strconv.ParseFloat(s, 64)
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
