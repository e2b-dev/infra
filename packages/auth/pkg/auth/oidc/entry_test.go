package oidc

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth/jwtutil"
)

func TestEntry_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entry   Entry
		wantErr string
	}{
		{
			name: "valid single audience no policy",
			entry: Entry{
				Issuer: Issuer{
					URL:       "https://issuer.example.com",
					Audiences: []string{"dashboard-api"},
				},
			},
		},
		{
			name: "valid single audience MatchAny",
			entry: Entry{
				Issuer: Issuer{
					URL:                 "https://issuer.example.com",
					Audiences:           []string{"dashboard-api"},
					AudienceMatchPolicy: jwtutil.AudienceMatchAny,
				},
			},
		},
		{
			name: "valid multiple audiences with MatchAny",
			entry: Entry{
				Issuer: Issuer{
					URL:                 "https://issuer.example.com",
					Audiences:           []string{"a", "b"},
					AudienceMatchPolicy: jwtutil.AudienceMatchAny,
				},
			},
		},
		{
			name: "missing issuer URL",
			entry: Entry{
				Issuer: Issuer{
					Audiences: []string{"a"},
				},
			},
			wantErr: "issuer.url is required",
		},
		{
			name: "non-https issuer URL",
			entry: Entry{
				Issuer: Issuer{
					URL:       "http://issuer.example.com",
					Audiences: []string{"a"},
				},
			},
			wantErr: "must be https",
		},
		{
			name: "issuer URL with userinfo",
			entry: Entry{
				Issuer: Issuer{
					URL:       "https://user:pass@issuer.example.com",
					Audiences: []string{"a"},
				},
			},
			wantErr: "must not contain a username or password",
		},
		{
			name: "issuer URL with query",
			entry: Entry{
				Issuer: Issuer{
					URL:       "https://issuer.example.com?foo=bar",
					Audiences: []string{"a"},
				},
			},
			wantErr: "must not contain a query",
		},
		{
			name: "issuer URL with fragment",
			entry: Entry{
				Issuer: Issuer{
					URL:       "https://issuer.example.com#frag",
					Audiences: []string{"a"},
				},
			},
			wantErr: "must not contain a fragment",
		},
		{
			name: "discoveryURL same as issuer URL",
			entry: Entry{
				Issuer: Issuer{
					URL:          "https://issuer.example.com",
					DiscoveryURL: "https://issuer.example.com",
					Audiences:    []string{"a"},
				},
			},
			wantErr: "discoveryURL must be different",
		},
		{
			name: "empty audiences",
			entry: Entry{
				Issuer: Issuer{
					URL: "https://issuer.example.com",
				},
			},
			wantErr: "audiences must contain at least one entry",
		},
		{
			name: "multiple audiences without MatchAny",
			entry: Entry{
				Issuer: Issuer{
					URL:       "https://issuer.example.com",
					Audiences: []string{"a", "b"},
				},
			},
			wantErr: "audienceMatchPolicy must be",
		},
		{
			name: "single audience with invalid policy",
			entry: Entry{
				Issuer: Issuer{
					URL:                 "https://issuer.example.com",
					Audiences:           []string{"a"},
					AudienceMatchPolicy: "MatchAll",
				},
			},
			wantErr: "audienceMatchPolicy must be empty or",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.entry.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
