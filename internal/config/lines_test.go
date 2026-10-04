package config_test

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/config"
)

func TestWords(t *testing.T) {
	for text, want := range map[string][]string{
		`--transport http https://x.example.com`:  {"--transport", "http", "https://x.example.com"},
		`--header "Authorization: Bearer ${KEY}"`: {"--header", "Authorization: Bearer ${KEY}"},
		`-- zsh -c 'cd ~/x && run "it"'`:          {"--", "zsh", "-c", `cd ~/x && run "it"`},
		`a\ b "c \"d\" \\e"`:                      {"a b", `c "d" \e`},
		``:                                        nil,
	} {
		got, err := config.Words(text)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Words(%q) = %q, %v; want %q", text, got, err, want)
		}
	}
	for _, text := range []string{`"open`, `'open`, `trailing\`} {
		if _, err := config.Words(text); err == nil {
			t.Errorf("Words(%q): no error", text)
		}
	}
}

// What Quote writes, Words reads back.
func TestQuoteRoundTrips(t *testing.T) {
	for _, word := range []string{"plain", "${KEY}", "https://x.example.com/mcp?a=1", "Authorization: Bearer ${KEY}", `say "hi"`, `back\slash`, "", "a#b"} {
		got, err := config.Words(config.Quote(word))
		if err != nil || len(got) != 1 || got[0] != word {
			t.Errorf("Words(Quote(%q)) = %q, %v", word, got, err)
		}
	}
	if config.Quote("${KEY}") != "${KEY}" || config.Quote("a b") != `"a b"` {
		t.Errorf("Quote() quotes what needn't be, or doesn't what must")
	}
}
