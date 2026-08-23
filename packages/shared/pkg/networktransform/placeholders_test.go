package networktransform

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePlaceholders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    []Placeholder
		wantErr bool
	}{
		{
			name:  "static text",
			value: "Bearer token",
		},
		{
			name:  "mixed identity and secret placeholders",
			value: "a${e2b.identity.tokens.aws}b${e2b.secrets. API-Key }c",
			want: []Placeholder{
				{Kind: PlaceholderIdentityToken, Name: "aws", Start: 1, End: 27},
				{Kind: PlaceholderCustomerSecret, Name: "api-key", Start: 28, End: 52},
			},
		},
		{
			name:  "adjacent placeholders preserve byte spans",
			value: "${e2b.secrets.alpha}${e2b.identity.tokens.token}",
			want: []Placeholder{
				{Kind: PlaceholderCustomerSecret, Name: "alpha", Start: 0, End: 20},
				{Kind: PlaceholderIdentityToken, Name: "token", Start: 20, End: 48},
			},
		},
		{
			name:  "secret spellings canonicalize independently",
			value: "${e2b.secrets.Alpha}:${e2b.secrets. alpha }",
			want: []Placeholder{
				{Kind: PlaceholderCustomerSecret, Name: "alpha", Start: 0, End: 20},
				{Kind: PlaceholderCustomerSecret, Name: "alpha", Start: 21, End: 43},
			},
		},
		{
			name:  "unknown E2B namespace remains static",
			value: "${e2b.other.value}",
		},
		{
			name:  "identity token name remains an exact non-empty map key",
			value: "${e2b.identity.tokens. token key }",
			want: []Placeholder{
				{Kind: PlaceholderIdentityToken, Name: " token key ", Start: 0, End: 34},
			},
		},
		{name: "unterminated identity placeholder", value: "${e2b.identity.tokens.aws", wantErr: true},
		{name: "unterminated secret placeholder", value: "${e2b.secrets.alpha", wantErr: true},
		{name: "empty identity name", value: "${e2b.identity.tokens.}", wantErr: true},
		{name: "empty secret name", value: "${e2b.secrets.}", wantErr: true},
		{name: "nested identity placeholder", value: "${e2b.secrets.${e2b.identity.tokens.aws}}", wantErr: true},
		{name: "nested secret placeholder", value: "${e2b.identity.tokens.${e2b.secrets.alpha}}", wantErr: true},
		{name: "invalid secret name", value: "${e2b.secrets.bad name}", wantErr: true},
		{name: "reserved secret name", value: "${e2b.secrets.sec_private-canary}", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParsePlaceholders(test.value)
			if test.wantErr {
				require.ErrorIs(t, err, errMalformedPlaceholder)
				assert.NotContains(t, err.Error(), "private-canary")
				assert.NotContains(t, err.Error(), test.value)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
			for _, placeholder := range got {
				assert.True(t, strings.HasSuffix(test.value[placeholder.Start:placeholder.End], "}"))
			}
		})
	}
}
