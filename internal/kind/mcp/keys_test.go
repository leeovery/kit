package mcp_test

import (
	"slices"
	"testing"

	"github.com/leeovery/kit/internal/kind/mcp"
)

func TestPlainKeys(t *testing.T) {
	for name, tt := range map[string]struct {
		def  map[string]any
		want []string
	}{
		"placeholders":    {map[string]any{"headers": map[string]any{"Authorization": "Bearer ${KEY}"}, "env": map[string]any{"API_TOKEN": "${TOKEN}"}}, nil},
		"plain header":    {map[string]any{"headers": map[string]any{"X-Api-Key": "abc"}}, []string{"headers.X-Api-Key"}},
		"plain env":       {map[string]any{"env": map[string]any{"RESEND_API_KEY": "re_abc", "LOG_LEVEL": "debug"}}, []string{"env.RESEND_API_KEY"}},
		"url param":       {map[string]any{"url": "https://x.example.com/mcp?apiKey=abc&mode=1"}, []string{"url's apiKey"}},
		"url placeholder": {map[string]any{"url": "https://x.example.com/mcp?token=${T}"}, nil},
		"flag and value":  {map[string]any{"args": []any{"server", "--api-key", "abc"}}, []string{"args' --api-key"}},
		"flag joined":     {map[string]any{"args": []any{"--token=abc", "--verbose"}}, []string{"args' --token"}},
		"flag named":      {map[string]any{"args": []any{"--token", "${T}"}}, nil},
	} {
		if got := mcp.PlainKeys(tt.def); !slices.Equal(got, tt.want) {
			t.Errorf("%s: PlainKeys() = %q, want %q", name, got, tt.want)
		}
	}
}
