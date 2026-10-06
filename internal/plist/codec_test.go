package plist_test

import (
	"encoding/hex"
	"math"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/plist"
)

// written is Python's plistlib's XML for value(), made-up values of every
// kind: what Encode must write, byte for byte.
const written = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>B upper</key>
	<integer>1</integer>
	<key>Z</key>
	<integer>4</integer>
	<key>_under</key>
	<integer>3</integer>
	<key>a lower</key>
	<integer>2</integer>
	<key>big</key>
	<integer>18446744073709551615</integer>
	<key>data empty</key>
	<data>
	</data>
	<key>data long</key>
	<data>
	AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEy
	MzQ1Njc4OTo7PD0+P0BBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWltcXV5fYGFiY2Rl
	ZmdoaWprbG1ub3BxcnN0dXZ3eHl6e3x9fn+AgYKDhIWGh4iJiouMjY6PkJGSk5SVlpeY
	mZqbnJ2en6ChoqOkpaanqKmqq6ytrq+wsbKztLW2t7i5uru8vb6/wMHCw8TFxsfIycrL
	zM3Oz9DR0tPU1dbX2Nna29zd3t/g4eLj5OXm5+jp6uvs7e7v8PHy8/T19vf4+fr7/P3+
	/wABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4fICEiIyQlJicoKSorLC0uLzAx
	MjM0NTY3ODk6Ozw9Pj9AQUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVpbXF1eX2BhYmNk
	ZWZnaGlqa2xtbm9wcXJzdHV2d3h5ent8fX5/gIGCg4SFhoeIiYqLjI2Oj5CRkpOUlZaX
	mJmam5ydnp+goaKjpKWmp6ipqqusra6vsLGys7S1tre4ubq7vL2+v8DBwsPExcbHyMnK
	y8zNzs/Q0dLT1NXW19jZ2tvc3d7f4OHi4+Tl5ufo6err7O3u7/Dx8vP09fb3+Pn6+/z9
	/v8=
	</data>
	<key>data short</key>
	<data>
	AAECa2l0
	</data>
	<key>date</key>
	<date>2026-09-28T17:00:05Z</date>
	<key>empty string</key>
	<string></string>
	<key>escaped</key>
	<string>a &amp; b &lt; c &gt; d</string>
	<key>false</key>
	<false/>
	<key>int</key>
	<integer>42</integer>
	<key>int64 min</key>
	<integer>-9223372036854775808</integer>
	<key>lines</key>
	<string>one
two
three
four</string>
	<key>negative</key>
	<integer>-7</integer>
	<key>nested</key>
	<dict>
		<key>deeper</key>
		<dict>
			<key>data</key>
			<data>
			AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUm
			JygpKissLS4vMDEyMzQ1Njc4OTo7PD0+P0BBQkNERUZHSElKS0xN
			Tk9QUVJTVFVWV1hZWltcXV5fYGFiYw==
			</data>
			<key>empty dict</key>
			<dict/>
			<key>empty list</key>
			<array/>
			<key>list</key>
			<array>
				<integer>1</integer>
				<string>two</string>
				<dict>
					<key>three</key>
					<integer>3</integer>
				</dict>
			</array>
		</dict>
	</dict>
	<key>reals</key>
	<array>
		<real>0.1</real>
		<real>100.0</real>
		<real>1e-05</real>
		<real>0.0001</real>
		<real>1e+16</real>
		<real>1234567890123456.0</real>
		<real>-0.0</real>
		<real>3.14159</real>
		<real>2.5e-07</real>
		<real>1e+22</real>
		<real>1.5</real>
		<real>123456789.125</real>
		<real>0.30000000000000004</real>
	</array>
	<key>spaces</key>
	<string>  padded  </string>
	<key>string</key>
	<string>plain</string>
	<key>true</key>
	<true/>
	<key>unicode</key>
	<string>café — 😀</string>
</dict>
</plist>
`

// value is the property list written holds, as Go values.
func value() map[string]any {
	counting := func(n int) plist.Data {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i)
		}
		return b
	}
	return map[string]any{
		"string":       "plain",
		"escaped":      "a & b < c > d",
		"lines":        "one\r\ntwo\rthree\nfour",
		"unicode":      "café — \U0001F600",
		"spaces":       "  padded  ",
		"empty string": "",
		"true":         true,
		"false":        false,
		"int":          int64(42),
		"negative":     int64(-7),
		"big":          uint64(math.MaxUint64),
		"int64 min":    int64(math.MinInt64),
		"reals":        []any{0.1, 100.0, 1e-05, 0.0001, 1e16, 1234567890123456.0, math.Copysign(0, -1), 3.14159, 2.5e-07, 1e22, 1.5, 123456789.125, 0.30000000000000004},
		"date":         time.Date(2026, 9, 28, 17, 0, 5, 0, time.UTC),
		"data empty":   plist.Data{},
		"data short":   plist.Data("\x00\x01\x02kit"),
		"data long":    append(counting(256), counting(256)...),
		"nested": map[string]any{"deeper": map[string]any{
			"data": counting(100), "list": []any{int64(1), "two", map[string]any{"three": int64(3)}},
			"empty dict": map[string]any{}, "empty list": []any{},
		}},
		"B upper": int64(1), "a lower": int64(2), "_under": int64(3), "Z": int64(4),
	}
}

func TestEncodeWritesWhatPlistlibWrites(t *testing.T) {
	got, err := plist.Encode(value())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != written {
		t.Errorf("Encode() =\n%s\nwant\n%s", got, written)
	}
}

// Read back and written again, a property list comes out the same: whitespace
// kept, carriage returns made newlines once, as plistlib makes them.
func TestDecodeThenEncodeIsTheSame(t *testing.T) {
	v, err := plist.Decode([]byte(written))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := v.(map[string]any)
	if m["spaces"] != "  padded  " || m["big"] != uint64(math.MaxUint64) || m["int64 min"] != int64(math.MinInt64) || m["lines"] != "one\ntwo\nthree\nfour" {
		t.Errorf("Decode() = %v", m)
	}
	again, err := plist.Encode(v)
	if err != nil || string(again) != written {
		t.Errorf("Encode(Decode()) =\n%s\n%v", again, err)
	}
	want := value()
	want["lines"] = "one\ntwo\nthree\nfour"
	if !plist.Equal(v, want) {
		t.Error("Decode() differs from the values written")
	}
}

// Python's repr of each, from Python.
func TestRealsAsPythonWritesThem(t *testing.T) {
	for _, c := range []struct {
		f    float64
		want string
	}{
		{0.0, "0.0"}, {math.Copysign(0, -1), "-0.0"}, {1.0, "1.0"}, {-1.0, "-1.0"}, {0.5, "0.5"},
		{0.1, "0.1"}, {0.2, "0.2"}, {0.3, "0.3"}, {0.3333333333333333, "0.3333333333333333"},
		{0.6666666666666666, "0.6666666666666666"}, {10.0, "10.0"}, {100.0, "100.0"},
		{1000000000000000.0, "1000000000000000.0"}, {1e+16, "1e+16"}, {1e+17, "1e+17"},
		{9999999999999998.0, "9999999999999998.0"}, {1234567890123456.8, "1234567890123456.8"},
		{0.001, "0.001"}, {0.0001, "0.0001"}, {1e-05, "1e-05"}, {1.5e-05, "1.5e-05"},
		{1.23e-08, "1.23e-08"}, {6.02214076e+23, "6.02214076e+23"},
		{1.7976931348623157e+308, "1.7976931348623157e+308"}, {5e-324, "5e-324"},
		{2.2250738585072014e-308, "2.2250738585072014e-308"}, {3e-05, "3e-05"}, {0.07, "0.07"},
		{1.1, "1.1"}, {2.675, "2.675"}, {16777216.0, "16777216.0"}, {0.15625, "0.15625"},
		{59.999999, "59.999999"}, {1e+100, "1e+100"}, {1e-100, "1e-100"}, {12345.678, "12345.678"},
	} {
		got, err := plist.Encode(c.f)
		if want := "<real>" + c.want + "</real>"; err != nil || !containsLine(string(got), want) {
			t.Errorf("Encode(%v) = %q, want a line %q", c.f, got, want)
		}
	}
}

func containsLine(text, line string) bool {
	for i, start := 0, 0; i <= len(text); i++ {
		if i == len(text) || text[i] == '\n' {
			if text[start:i] == line {
				return true
			}
			start = i + 1
		}
	}
	return false
}

// A string holding a control character is refused, as plistlib refuses it;
// tabs and newlines aren't.
func TestEncodeRefusesControlCharacters(t *testing.T) {
	if _, err := plist.Encode(map[string]any{"bell": "ding\a"}); err == nil {
		t.Error("Encode() wrote a control character")
	}
	if _, err := plist.Encode(map[string]any{"tab": "a\tb\nc"}); err != nil {
		t.Errorf("Encode() = %v", err)
	}
	if _, err := plist.Encode(map[string]any{"value": nil}); err == nil {
		t.Error("Encode() wrote a nil")
	}
}

func TestDecodeRefusesWhatIsntOne(t *testing.T) {
	for _, text := range []string{"", "<dict/>", `<plist version="1.0"></plist>`, `<plist><dict><key>a</key></dict></plist>`, `<plist><integer>x</integer></plist>`, `<plist><date>2026Z</date></plist>`} {
		if _, err := plist.Decode([]byte(text)); err == nil {
			t.Errorf("Decode(%q) read it", text)
		}
	}
	if v, err := plist.Decode([]byte(`<plist><integer>0x1F</integer></plist>`)); err != nil || v != int64(31) {
		t.Errorf("Decode(0x1F) = %v, %v", v, err)
	}
}

// A binary property list, as an app's Info.plist may be: made with
// plistlib, made-up values.
func TestDecodeReadsBinary(t *testing.T) {
	raw, _ := hex.DecodeString("62706c6973743030d90102030405060708090a0b0c0d0e111213145342696754426c6f625f1012434642756e646c654964656e7469666965725c434642756e646c654e616d65554974656d735b4c535549456c656d656e74534e656755526174696f5756657273696f6e140000000000000000ffffffffffffffff4201025f100f636f6d2e6578616d706c652e617070670045007800e4006d0070006c0065a20f1010015374776f0913fffffffffffffffb233fe00000000000001003081b1f2439464c585c626a7b7e909fa2a4a8a9b2bb00000000000001010000000000000015000000000000000000000000000000bd")
	v, err := plist.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"CFBundleIdentifier": "com.example.app", "CFBundleName": "Exämple", "LSUIElement": true,
		"Version": int64(3), "Ratio": 0.5, "Items": []any{int64(1), "two"}, "Blob": plist.Data{1, 2},
		"Big": uint64(math.MaxUint64), "Neg": int64(-5),
	}
	if !plist.Equal(v, want) {
		t.Errorf("Decode() = %v", v)
	}
	if _, err := plist.Decode(raw[:40]); err == nil {
		t.Error("Decode() read a cut-off binary property list")
	}
}
