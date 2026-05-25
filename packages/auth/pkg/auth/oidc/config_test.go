package oidc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEntry_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entry   Config
		wantErr string
	}{
		{
			name: "valid single audience no policy",
			entry: Config{
				Issuer: Issuer{
					URL:       "https://issuer.example.com",
					Audiences: []string{"dashboard-api"},
				},
			},
		},
		{
			name: "valid single audience MatchAny",
			entry: Config{
				Issuer: Issuer{
					URL:                 "https://issuer.example.com",
					Audiences:           []string{"dashboard-api"},
					AudienceMatchPolicy: AudienceMatchAny,
				},
			},
		},
		{
			name: "valid multiple audiences with MatchAny",
			entry: Config{
				Issuer: Issuer{
					URL:                 "https://issuer.example.com",
					Audiences:           []string{"a", "b"},
					AudienceMatchPolicy: AudienceMatchAny,
				},
			},
		},
		{
			name: "missing issuer URL",
			entry: Config{
				Issuer: Issuer{
					Audiences: []string{"a"},
				},
			},
			wantErr: "issuer.url is required",
		},
		{
			name: "non-https issuer URL",
			entry: Config{
				Issuer: Issuer{
					URL:       "http://issuer.example.com",
					Audiences: []string{"a"},
				},
			},
			wantErr: "must be https",
		},
		{
			// Loopback carve-out: http is allowed when the host is
			// localhost. Used by the local devenv stack to point at
			// self-hosted Hydra on http://localhost:4444/.
			name: "http issuer URL with localhost host is accepted",
			entry: Config{
				Issuer: Issuer{
					URL:       "http://localhost:4444/",
					Audiences: []string{"a"},
				},
			},
		},
		{
			name: "http issuer URL with 127.0.0.1 host is accepted",
			entry: Config{
				Issuer: Issuer{
					URL:       "http://127.0.0.1:4444/",
					Audiences: []string{"a"},
				},
			},
		},
		{
			name: "http issuer URL with IPv6 loopback is accepted",
			entry: Config{
				Issuer: Issuer{
					URL:       "http://[::1]:4444/",
					Audiences: []string{"a"},
				},
			},
		},
		{
			name: "issuer URL with userinfo",
			entry: Config{
				Issuer: Issuer{
					URL:       "https://user:pass@issuer.example.com",
					Audiences: []string{"a"},
				},
			},
			wantErr: "must not contain a username or password",
		},
		{
			name: "issuer URL with query",
			entry: Config{
				Issuer: Issuer{
					URL:       "https://issuer.example.com?foo=bar",
					Audiences: []string{"a"},
				},
			},
			wantErr: "must not contain a query",
		},
		{
			name: "issuer URL with fragment",
			entry: Config{
				Issuer: Issuer{
					URL:       "https://issuer.example.com#frag",
					Audiences: []string{"a"},
				},
			},
			wantErr: "must not contain a fragment",
		},
		{
			name: "discoveryURL same as issuer URL",
			entry: Config{
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
			entry: Config{
				Issuer: Issuer{
					URL: "https://issuer.example.com",
				},
			},
			wantErr: "audiences must contain at least one entry",
		},
		{
			name: "multiple audiences without MatchAny",
			entry: Config{
				Issuer: Issuer{
					URL:       "https://issuer.example.com",
					Audiences: []string{"a", "b"},
				},
			},
			wantErr: "audienceMatchPolicy must be",
		},
		{
			name: "single audience with invalid policy",
			entry: Config{
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
