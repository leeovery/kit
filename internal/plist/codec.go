package plist

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// header begins an XML property list, as Apple's tools and Python's plistlib
// write it.
const header = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
`

// UID is a binary property list's UID, which archives use: read, but never
// written.
type UID uint64

// Decode reads a property list, XML or binary, exactly: a string as it is,
// whitespace and all; an integer as int64, or uint64 past int64's range; a
// real as float64; data as Data; a date as a UTC time.
func Decode(data []byte) (any, error) {
	if bytes.HasPrefix(data, []byte("bplist00")) {
		v, err := decodeBinary(data)
		if err != nil {
			return nil, fmt.Errorf("read the property list: %w", err)
		}
		return v, nil
	}
	v, err := decodeXML(data)
	if err != nil {
		return nil, fmt.Errorf("read the property list: %w", err)
	}
	return v, nil
}

// errEnd is the end of the element holding the values being read.
var errEnd = errors.New("end of element")

func decodeXML(data []byte) (any, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "plist" {
			return nil, fmt.Errorf("<%s> where <plist> should be", start.Name.Local)
		}
		v, err := nextValue(d)
		if errors.Is(err, errEnd) {
			return nil, errors.New("it holds nothing")
		}
		return v, err
	}
}

// key is a dict's key, told apart from a string value.
type key string

// nextValue reads the next value, skipping text, comments and elements a
// property list doesn't have; at the end of the enclosing element it
// returns errEnd.
func nextValue(d *xml.Decoder) (any, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil, errEnd
		case xml.StartElement:
			v, known, err := element(d, t)
			if err != nil || known {
				return v, err
			}
		}
	}
}

// element reads the value start begins; known is false for an element a
// property list doesn't have, which is skipped.
func element(d *xml.Decoder, start xml.StartElement) (v any, known bool, err error) {
	name := start.Name.Local
	switch name {
	case "dict":
		m := map[string]any{}
		for {
			k, err := nextValue(d)
			if errors.Is(err, errEnd) {
				return m, true, nil
			}
			if err != nil {
				return nil, true, err
			}
			s, ok := k.(key)
			if !ok {
				return nil, true, errors.New("a dict's value without its key")
			}
			v, err := nextValue(d)
			if errors.Is(err, errEnd) {
				return nil, true, fmt.Errorf("no value for the key %q", string(s))
			}
			if err != nil {
				return nil, true, err
			}
			m[string(s)] = v
		}
	case "array":
		a := []any{}
		for {
			v, err := nextValue(d)
			if errors.Is(err, errEnd) {
				return a, true, nil
			}
			if err != nil {
				return nil, true, err
			}
			a = append(a, v)
		}
	case "true", "false":
		return name == "true", true, d.Skip()
	case "key", "string", "integer", "real", "data", "date":
		text, err := leafText(d)
		if err != nil {
			return nil, true, err
		}
		v, err := leaf(name, text)
		return v, true, err
	}
	return nil, false, d.Skip()
}

// leafText is the text of the element just begun, to its end.
func leafText(d *xml.Decoder) (string, error) {
	var b strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			return "", fmt.Errorf("<%s> inside a value", t.Name.Local)
		case xml.EndElement:
			return b.String(), nil
		}
	}
}

// dateText is a property list's date: a year, then as much of the rest as
// it gives, then Z.
var dateText = regexp.MustCompile(`^(\d{4})(?:-(\d\d)(?:-(\d\d)(?:T(\d\d)(?::(\d\d)(?::(\d\d))?)?)?)?)?Z`)

// leaf reads a value element's text, as Python's plistlib does.
func leaf(name, text string) (any, error) {
	switch name {
	case "key":
		return key(text), nil
	case "string":
		return text, nil
	case "integer":
		return parseInteger(text)
	case "real":
		f, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil {
			return nil, fmt.Errorf("%q isn't a real", text)
		}
		return f, nil
	case "data":
		clean := strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=' {
				return r
			}
			return -1
		}, text)
		b, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			return nil, fmt.Errorf("data that isn't base64: %w", err)
		}
		return Data(b), nil
	}
	m := dateText.FindStringSubmatch(text)
	if m == nil || m[3] == "" {
		return nil, fmt.Errorf("%q isn't a date", text)
	}
	var n [6]int
	for i := range n {
		if m[i+1] != "" {
			n[i], _ = strconv.Atoi(m[i+1])
		}
	}
	t := time.Date(n[0], time.Month(n[1]), n[2], n[3], n[4], n[5], 0, time.UTC)
	if t.Month() != time.Month(n[1]) || t.Day() != n[2] || n[3] > 23 || n[4] > 59 || n[5] > 59 {
		return nil, fmt.Errorf("%q isn't a date", text)
	}
	return t, nil
}

// parseInteger reads an integer, decimal or 0x hexadecimal: int64, or uint64
// past int64's range.
func parseInteger(text string) (any, error) {
	base, digits := 10, strings.TrimSpace(text)
	if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X") {
		base, digits = 16, strings.TrimSpace(text[2:])
	}
	if n, err := strconv.ParseInt(digits, base, 64); err == nil {
		return n, nil
	}
	if n, err := strconv.ParseUint(digits, base, 64); err == nil {
		return n, nil
	}
	return nil, fmt.Errorf("%q isn't an integer a property list holds", text)
}

// bplist is a binary property list being read: its bytes, where each object
// starts, and how wide a reference to one is.
type bplist struct {
	data    []byte
	offsets []uint64
	refSize int
}

// epoch2001 is when a binary property list's dates count from, in Unix
// seconds: 2001-01-01 00:00 UTC.
const epoch2001 = 978307200

func decodeBinary(data []byte) (any, error) {
	if len(data) < 8+32 {
		return nil, errors.New("too short for a binary property list")
	}
	t := data[len(data)-32:]
	offsetSize, refSize := int(t[6]), int(t[7])
	count, top, table := binary.BigEndian.Uint64(t[8:]), binary.BigEndian.Uint64(t[16:]), binary.BigEndian.Uint64(t[24:])
	if offsetSize < 1 || offsetSize > 8 || refSize < 1 || refSize > 8 || count == 0 || top >= count ||
		table < 8 || table > uint64(len(data)) || count > (uint64(len(data))-table)/uint64(offsetSize) {
		return nil, errors.New("a binary property list's trailer that doesn't read")
	}
	b := &bplist{data: data, refSize: refSize, offsets: make([]uint64, count)}
	for i := range b.offsets {
		at := table + uint64(i*offsetSize)
		b.offsets[i] = sized(data[at : at+uint64(offsetSize)])
	}
	return b.object(top, 0)
}

// sized is a big-endian unsigned integer of up to 8 bytes.
func sized(b []byte) uint64 {
	var n uint64
	for _, c := range b {
		n = n<<8 | uint64(c)
	}
	return n
}

// span is n bytes from at, or an error when they run past the end.
func (b *bplist) span(at, n uint64) ([]byte, error) {
	if at > uint64(len(b.data)) || n > uint64(len(b.data))-at {
		return nil, errors.New("an object that runs past the end")
	}
	return b.data[at : at+n], nil
}

func (b *bplist) object(ref uint64, depth int) (any, error) {
	if ref >= uint64(len(b.offsets)) || depth > 64 {
		return nil, errors.New("a reference that doesn't lead anywhere")
	}
	at := b.offsets[ref]
	head, err := b.span(at, 1)
	if err != nil {
		return nil, err
	}
	kind, low := head[0]>>4, head[0]&0x0f
	at++
	switch kind {
	case 0x0:
		switch low {
		case 0x8:
			return false, nil
		case 0x9:
			return true, nil
		}
		return nil, fmt.Errorf("an object kit doesn't read (0x%02x)", head[0])
	case 0x1:
		raw, err := b.span(at, 1<<low)
		if err != nil {
			return nil, err
		}
		return intObject(raw)
	case 0x2:
		raw, err := b.span(at, 1<<low)
		if err != nil {
			return nil, err
		}
		switch len(raw) {
		case 4:
			return float64(math.Float32frombits(binary.BigEndian.Uint32(raw))), nil
		case 8:
			return math.Float64frombits(binary.BigEndian.Uint64(raw)), nil
		}
		return nil, errors.New("a real of a size kit doesn't read")
	case 0x3:
		raw, err := b.span(at, 8)
		if err != nil {
			return nil, err
		}
		secs := math.Float64frombits(binary.BigEndian.Uint64(raw))
		// As Python does: to the nearest microsecond, halves to even. In
		// seconds and microseconds, as a Duration can't reach the year 1
		// or 4001, where distant pasts and futures are kept.
		micros := math.RoundToEven(secs * 1e6)
		whole := math.Floor(micros / 1e6)
		return time.Unix(epoch2001+int64(whole), int64(micros-whole*1e6)*1000).UTC(), nil
	case 0x8:
		raw, err := b.span(at, uint64(low)+1)
		if err != nil {
			return nil, err
		}
		return UID(sized(raw)), nil
	}
	n, at, err := b.length(low, at)
	if err != nil {
		return nil, err
	}
	switch kind {
	case 0x4:
		raw, err := b.span(at, n)
		if err != nil {
			return nil, err
		}
		return Data(bytes.Clone(raw)), nil
	case 0x5:
		raw, err := b.span(at, n)
		if err != nil {
			return nil, err
		}
		return string(raw), nil
	case 0x6:
		raw, err := b.span(at, n*2)
		if err != nil {
			return nil, err
		}
		units := make([]uint16, n)
		for i := range units {
			units[i] = binary.BigEndian.Uint16(raw[i*2:])
		}
		return string(utf16.Decode(units)), nil
	case 0xa:
		refs, err := b.refs(at, n)
		if err != nil {
			return nil, err
		}
		a := make([]any, 0, len(refs))
		for _, r := range refs {
			v, err := b.object(r, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		return a, nil
	case 0xd:
		refs, err := b.refs(at, n*2)
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, n)
		for i := range n {
			k, err := b.object(refs[i], depth+1)
			if err != nil {
				return nil, err
			}
			s, ok := k.(string)
			if !ok {
				return nil, errors.New("a dict's key that isn't a string")
			}
			v, err := b.object(refs[n+i], depth+1)
			if err != nil {
				return nil, err
			}
			m[s] = v
		}
		return m, nil
	}
	return nil, fmt.Errorf("an object kit doesn't read (0x%02x)", head[0])
}

// intObject is a binary property list's integer: 1, 2 or 4 bytes unsigned,
// 8 signed, or 16 that fit in int64 or uint64.
func intObject(raw []byte) (any, error) {
	switch len(raw) {
	case 1, 2, 4:
		return int64(sized(raw)), nil
	case 8:
		return int64(binary.BigEndian.Uint64(raw)), nil
	case 16:
		hi, lo := binary.BigEndian.Uint64(raw), binary.BigEndian.Uint64(raw[8:])
		switch {
		case hi == 0 && lo <= math.MaxInt64:
			return int64(lo), nil
		case hi == 0:
			return lo, nil
		case hi == math.MaxUint64 && lo > math.MaxInt64:
			return int64(lo), nil
		}
	}
	return nil, errors.New("an integer too big to read")
}

// length is a data, string, array or dict object's length: its marker's
// low nibble, or, at 0xf, the integer object that follows; and where the
// object's contents start.
func (b *bplist) length(low byte, at uint64) (uint64, uint64, error) {
	if low != 0xf {
		return uint64(low), at, nil
	}
	head, err := b.span(at, 1)
	if err != nil {
		return 0, 0, err
	}
	if head[0]>>4 != 0x1 {
		return 0, 0, errors.New("a length that isn't an integer")
	}
	raw, err := b.span(at+1, 1<<(head[0]&0x0f))
	if err != nil {
		return 0, 0, err
	}
	n, err := intObject(raw)
	if err != nil {
		return 0, 0, err
	}
	size, ok := n.(int64)
	if !ok || size < 0 {
		return 0, 0, errors.New("a length that doesn't read")
	}
	return uint64(size), at + 1 + uint64(len(raw)), nil
}

// refs are n object references starting at at.
func (b *bplist) refs(at, n uint64) ([]uint64, error) {
	raw, err := b.span(at, n*uint64(b.refSize))
	if err != nil {
		return nil, err
	}
	out := make([]uint64, n)
	for i := range out {
		out[i] = sized(raw[i*b.refSize : (i+1)*b.refSize])
	}
	return out, nil
}

// Encode writes v as an XML property list exactly as Python's plistlib
// writes one, keys sorted: the form prefsync's store keeps, so the same
// settings write the same bytes. A tab an indent; data in base64 lines that
// stop short of column 76; reals as Python's repr; dates to the second.
// Strings holding control characters other than tab and newlines are
// refused, as plistlib refuses them.
func Encode(v any) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(header)
	w := writer{b: &b}
	w.line(0, `<plist version="1.0">`)
	if err := w.value(0, v); err != nil {
		return nil, err
	}
	w.line(0, "</plist>")
	return b.Bytes(), nil
}

type writer struct {
	b *bytes.Buffer
}

func (w writer) line(level int, s string) {
	w.b.WriteString(strings.Repeat("\t", level))
	w.b.WriteString(s)
	w.b.WriteByte('\n')
}

func (w writer) value(level int, v any) error {
	switch x := v.(type) {
	case string:
		e, err := escape(x)
		if err != nil {
			return err
		}
		w.line(level, "<string>"+e+"</string>")
	case bool:
		if x {
			w.line(level, "<true/>")
		} else {
			w.line(level, "<false/>")
		}
	case int:
		w.line(level, "<integer>"+strconv.Itoa(x)+"</integer>")
	case int64:
		w.line(level, "<integer>"+strconv.FormatInt(x, 10)+"</integer>")
	case uint64:
		w.line(level, "<integer>"+strconv.FormatUint(x, 10)+"</integer>")
	case float64:
		w.line(level, "<real>"+pyRepr(x)+"</real>")
	case time.Time:
		w.line(level, "<date>"+x.UTC().Format("2006-01-02T15:04:05Z")+"</date>")
	case Data:
		w.line(level, "<data>")
		width := max(16, 76-8*level)
		chunk := width / 4 * 3
		for i := 0; i < len(x); i += chunk {
			w.line(level, base64.StdEncoding.EncodeToString(x[i:min(i+chunk, len(x))]))
		}
		w.line(level, "</data>")
	case []any:
		if len(x) == 0 {
			w.line(level, "<array/>")
			return nil
		}
		w.line(level, "<array>")
		for _, e := range x {
			if err := w.value(level+1, e); err != nil {
				return err
			}
		}
		w.line(level, "</array>")
	case map[string]any:
		if len(x) == 0 {
			w.line(level, "<dict/>")
			return nil
		}
		w.line(level, "<dict>")
		for _, k := range slices.Sorted(maps.Keys(x)) {
			e, err := escape(k)
			if err != nil {
				return err
			}
			w.line(level+1, "<key>"+e+"</key>")
			if err := w.value(level+1, x[k]); err != nil {
				return err
			}
		}
		w.line(level, "</dict>")
	default:
		return fmt.Errorf("a %T can't be written to a property list", v)
	}
	return nil
}

// escape is a string as plistlib writes it: refused when it holds a control
// character other than a tab or a newline; carriage returns made newlines;
// &, < and > escaped.
func escape(s string) (string, error) {
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return "", errors.New("strings can't contain control characters")
		}
	}
	return strings.NewReplacer("\r\n", "\n", "\r", "\n", "&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s), nil
}

// pyRepr is a real as Python's repr writes it: the shortest digits that read
// back the same; plain from 0.0001 up to 16 digits before the point, with
// .0 on a whole number; otherwise in exponent form, as 1e-05 or 1e+16.
func pyRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	sign := ""
	if s[0] == '-' {
		sign, s = "-", s[1:]
	}
	mantissa, exponent, _ := strings.Cut(s, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	e, _ := strconv.Atoi(exponent)
	point := e + 1
	switch {
	case point <= -4 || point > 16:
		out := digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		return sign + out + fmt.Sprintf("e%+03d", e)
	case point <= 0:
		return sign + "0." + strings.Repeat("0", -point) + digits
	case point >= len(digits):
		return sign + digits + strings.Repeat("0", point-len(digits)) + ".0"
	}
	return sign + digits[:point] + "." + digits[point:]
}
