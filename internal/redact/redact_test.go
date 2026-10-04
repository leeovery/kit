package redact_test

import (
	"testing"

	"github.com/leeovery/kit/internal/redact"
)

func TestText(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		secrets []string
		want    string
	}{
		{name: "nothing secret", text: "brew list --formula --full-name -1", want: "brew list --formula --full-name -1"},
		{name: "a GitHub token", text: "token ghp_ABCDEFGHIJKLMNOPQRSTuvwxyz0123 in a line", want: "token [redacted] in a line"},
		{name: "a fine-grained GitHub token", text: "GH=github_pat_11ABCDEFG0123456789_abcdefghijklmnop", want: "GH=[redacted]"},
		{name: "a GitHub app token", text: "ghs_abcdefghijklmnopqrstuvwxyz0123456789", want: "[redacted]"},
		{name: "a Claude token", text: "key: sk-ant-oat01-abc_DEF-123", want: "key: [redacted]"},
		{name: "a 1Password service account token", text: "OP_SERVICE_ACCOUNT_TOKEN=ops_eyJzaWduSW5BZGRyZXNzIjoibXkuMXBhc3N3b3JkLmNvbSJ9", want: "OP_SERVICE_ACCOUNT_TOKEN=[redacted]"},
		{name: "a bearer header", text: "Authorization: Bearer abcdefghijklmnop.qrstuvwx", want: "Authorization: Bearer [redacted]"},
		{name: "a token header", text: "Authorization: token 0123456789abcdef0123", want: "Authorization: token [redacted]"},
		{name: "a short word after bearer stays", text: "bearer of news", want: "bearer of news"},
		{name: "a secret handed over", text: "wrote hunter2-value to the file", secrets: []string{"hunter2-value", ""}, want: "wrote [redacted] to the file"},
		{name: "ghp_ too short to be a token", text: "ghp_short", want: "ghp_short"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redact.Text(tt.text, tt.secrets...); got != tt.want {
				t.Errorf("Text(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}
