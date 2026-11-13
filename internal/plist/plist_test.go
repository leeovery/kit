package plist_test

import (
	"math"
	"testing"

	"github.com/leeovery/kit/internal/plist"
)

const doc = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>size</key><integer>60</integer>
	<key>speed</key><real>0.5</real>
	<key>on</key><true/>
	<key>name</key><string>a &amp; b</string>
	<key>blob</key><data>aGk=</data>
	<key>list</key><array><string>x</string><integer>2</integer></array>
</dict>
</plist>`

func TestEqual(t *testing.T) {
	for _, c := range []struct {
		a, b any
		want bool
	}{
		{true, int64(1), true},
		{false, int64(0), true},
		{int64(60), 60.0, true},
		{0.001, 0.0010000000474974513, true},
		{0.001, 0.002, false},
		{int64(60), int64(61), false},
		{"60", int64(60), false},
		{map[string]any{"a": true}, map[string]any{"a": int64(1)}, true},
		{map[string]any{"a": true}, map[string]any{"a": true, "b": true}, false},
		{[]any{"x"}, []any{"x"}, true},
		{plist.Data("hi"), plist.Data("hi"), true},
	} {
		if got := plist.Equal(c.a, c.b); got != c.want {
			t.Errorf("Equal(%#v, %#v) = %v", c.a, c.b, got)
		}
	}
}

func TestXMLReadsBack(t *testing.T) {
	v, _ := plist.Decode([]byte(doc))
	text := plist.XML(v)
	if want := `<dict><key>blob</key><data>aGk=</data><key>list</key><array><string>x</string><integer>2</integer></array><key>name</key><string>a &amp; b</string><key>on</key><true/><key>size</key><integer>60</integer><key>speed</key><real>0.5</real></dict>`; text != want {
		t.Errorf("XML() = %s", text)
	}
	back, err := plist.ReadValue(text)
	if err != nil || !plist.Equal(back, v) {
		t.Errorf("ReadValue(XML()) = %#v, %v", back, err)
	}
}

// An integer past int64's range, which Decode reads as uint64, is written
// as the integer it is.
func TestXMLWritesAnIntegerPastInt64(t *testing.T) {
	if got := plist.XML(uint64(math.MaxUint64)); got != "<integer>18446744073709551615</integer>" {
		t.Errorf("XML() = %s", got)
	}
}
