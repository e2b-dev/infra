package secretsstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAppendMarkerNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{name: "no marker", value: "Bearer token", want: nil},
		{name: "one marker", value: "Bearer ${e2b.secrets.alpha}", want: []string{"alpha"}},
		{name: "two markers keep first-seen order", value: "${e2b.secrets.beta}:${e2b.secrets.alpha}", want: []string{"beta", "alpha"}},
		{name: "case variants canonicalize once", value: "${e2b.secrets.Alpha} ${e2b.secrets.alpha}", want: []string{"alpha"}},
		{name: "unterminated marker stops the scan", value: "${e2b.secrets.alpha", want: nil},
		{name: "malformed name is skipped", value: "${e2b.secrets.al pha} ${e2b.secrets.beta}", want: []string{"beta"}},
		{name: "reserved prefix is skipped", value: "${e2b.secrets.sec_alpha}", want: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := AppendMarkerNames(nil, map[string]struct{}{}, test.value)
			assert.Equal(t, test.want, got)
		})
	}
}
