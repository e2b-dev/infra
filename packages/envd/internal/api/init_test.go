package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	utilsShared "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestSimpleCases(t *testing.T) {
	t.Parallel()
	testCases := map[string]func(string) string{
		"both newlines":               func(s string) string { return s },
		"no newline prefix":           func(s string) string { return strings.TrimPrefix(s, "\n") },
		"no newline suffix":           func(s string) string { return strings.TrimSuffix(s, "\n") },
		"no newline prefix or suffix": strings.TrimSpace,
	}

	for name, preprocessor := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tempDir := t.TempDir()

			value := `
# comment
127.0.0.1        one.host
127.0.0.2        two.host
`
			value = preprocessor(value)
			inputPath := filepath.Join(tempDir, "hosts")
			err := os.WriteFile(inputPath, []byte(value), hostsFilePermissions)
			require.NoError(t, err)

			err = rewriteHostsFile("127.0.0.3", inputPath)
			require.NoError(t, err)

			data, err := os.ReadFile(inputPath)
			require.NoError(t, err)

			assert.Equal(t, `# comment
127.0.0.1        one.host
127.0.0.2        two.host
127.0.0.3        events.e2b.local`, strings.TrimSpace(string(data)))
		})
	}
}

func TestShouldSetSystemTime(t *testing.T) {
	t.Parallel()
	sandboxTime := time.Now()

	tests := []struct {
		name     string
		hostTime time.Time
		want     bool
	}{
		{
			name:     "sandbox time far ahead of host time (should set)",
			hostTime: sandboxTime.Add(-10 * time.Second),
			want:     true,
		},
		{
			name:     "sandbox time at maxTimeInPast boundary ahead of host time (should not set)",
			hostTime: sandboxTime.Add(-50 * time.Millisecond),
			want:     false,
		},
		{
			name:     "sandbox time just within maxTimeInPast ahead of host time (should not set)",
			hostTime: sandboxTime.Add(-40 * time.Millisecond),
			want:     false,
		},
		{
			name:     "sandbox time slightly ahead of host time (should not set)",
			hostTime: sandboxTime.Add(-10 * time.Millisecond),
			want:     false,
		},
		{
			name:     "sandbox time equals host time (should not set)",
			hostTime: sandboxTime,
			want:     false,
		},
		{
			name:     "sandbox time slightly behind host time (should not set)",
			hostTime: sandboxTime.Add(1 * time.Second),
			want:     false,
		},
		{
			name:     "sandbox time just within maxTimeInFuture behind host time (should not set)",
			hostTime: sandboxTime.Add(4 * time.Second),
			want:     false,
		},
		{
			name:     "sandbox time at maxTimeInFuture boundary behind host time (should not set)",
			hostTime: sandboxTime.Add(5 * time.Second),
			want:     false,
		},
		{
			name:     "sandbox time far behind host time (should set)",
			hostTime: sandboxTime.Add(1 * time.Minute),
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := shouldSetSystemTime(tt.hostTime, sandboxTime)
			assert.Equal(t, tt.want, got)
		})
	}
}

func secureTokenPtr(s string) *SecureToken {
	token := &SecureToken{}
	_ = token.Set([]byte(s))

	return token
}

type mockMMDSClient struct {
	hash string
	err  error
}

func (m *mockMMDSClient) GetAccessTokenHash(_ context.Context) (string, error) {
	return m.hash, m.err
}

func newTestAPI(accessToken *SecureToken, mmdsClient MMDSClient) *API {
	logger := zerolog.Nop()
	defaults := &execcontext.Defaults{
		EnvVars: utils.NewMap[string, string](),
	}
	api := New(&logger, defaults, nil, false)
	if accessToken != nil {
		api.accessToken.TakeFrom(accessToken)
	}
	api.mmdsClient = mmdsClient

	return api
}

func TestValidateInitAccessToken(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	tests := []struct {
		name         string
		accessToken  *SecureToken
		requestToken *SecureToken
		mmdsHash     string
		mmdsErr      error
		wantErr      error
	}{
		{
			name:         "fast path: token matches existing",
			accessToken:  secureTokenPtr("secret-token"),
			requestToken: secureTokenPtr("secret-token"),
			mmdsHash:     "",
			mmdsErr:      nil,
			wantErr:      nil,
		},
		{
			name:         "MMDS match: token hash matches MMDS hash",
			accessToken:  secureTokenPtr("old-token"),
			requestToken: secureTokenPtr("new-token"),
			mmdsHash:     keys.HashAccessToken("new-token"),
			mmdsErr:      nil,
			wantErr:      nil,
		},
		{
			name:         "first-time setup: no existing token, MMDS error",
			accessToken:  nil,
			requestToken: secureTokenPtr("new-token"),
			mmdsHash:     "",
			mmdsErr:      assert.AnError,
			wantErr:      nil,
		},
		{
			name:         "first-time setup: no existing token, empty MMDS hash",
			accessToken:  nil,
			requestToken: secureTokenPtr("new-token"),
			mmdsHash:     "",
			mmdsErr:      nil,
			wantErr:      nil,
		},
		{
			name:         "first-time setup: both tokens nil, no MMDS",
			accessToken:  nil,
			requestToken: nil,
			mmdsHash:     "",
			mmdsErr:      assert.AnError,
			wantErr:      nil,
		},
		{
			name:         "mismatch: existing token differs from request, no MMDS",
			accessToken:  secureTokenPtr("existing-token"),
			requestToken: secureTokenPtr("wrong-token"),
			mmdsHash:     "",
			mmdsErr:      assert.AnError,
			wantErr:      ErrAccessTokenMismatch,
		},
		{
			name:         "mismatch: existing token differs from request, MMDS hash mismatch",
			accessToken:  secureTokenPtr("existing-token"),
			requestToken: secureTokenPtr("wrong-token"),
			mmdsHash:     keys.HashAccessToken("different-token"),
			mmdsErr:      nil,
			wantErr:      ErrAccessTokenMismatch,
		},
		{
			name:         "conflict: existing token, nil request, MMDS exists",
			accessToken:  secureTokenPtr("existing-token"),
			requestToken: nil,
			mmdsHash:     keys.HashAccessToken("some-token"),
			mmdsErr:      nil,
			wantErr:      ErrAccessTokenResetNotAuthorized,
		},
		{
			name:         "conflict: existing token, nil request, no MMDS",
			accessToken:  secureTokenPtr("existing-token"),
			requestToken: nil,
			mmdsHash:     "",
			mmdsErr:      assert.AnError,
			wantErr:      ErrAccessTokenResetNotAuthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mmdsClient := &mockMMDSClient{hash: tt.mmdsHash, err: tt.mmdsErr}
			api := newTestAPI(tt.accessToken, mmdsClient)

			err := api.validateInitAccessToken(ctx, tt.requestToken)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCheckMMDSHash(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("returns match when token hash equals MMDS hash", func(t *testing.T) {
		t.Parallel()
		token := "my-secret-token"
		mmdsClient := &mockMMDSClient{hash: keys.HashAccessToken(token), err: nil}
		api := newTestAPI(nil, mmdsClient)

		matches, exists := api.checkMMDSHash(ctx, secureTokenPtr(token))

		assert.True(t, matches)
		assert.True(t, exists)
	})

	t.Run("returns no match when token hash differs from MMDS hash", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: keys.HashAccessToken("different-token"), err: nil}
		api := newTestAPI(nil, mmdsClient)

		matches, exists := api.checkMMDSHash(ctx, secureTokenPtr("my-token"))

		assert.False(t, matches)
		assert.True(t, exists)
	})

	t.Run("returns exists but no match when request token is nil", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: keys.HashAccessToken("some-token"), err: nil}
		api := newTestAPI(nil, mmdsClient)

		matches, exists := api.checkMMDSHash(ctx, nil)

		assert.False(t, matches)
		assert.True(t, exists)
	})

	t.Run("returns false, false when MMDS returns error", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: "", err: assert.AnError}
		api := newTestAPI(nil, mmdsClient)

		matches, exists := api.checkMMDSHash(ctx, secureTokenPtr("any-token"))

		assert.False(t, matches)
		assert.False(t, exists)
	})

	t.Run("returns false, false when MMDS returns empty hash with non-nil request", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: "", err: nil}
		api := newTestAPI(nil, mmdsClient)

		matches, exists := api.checkMMDSHash(ctx, secureTokenPtr("any-token"))

		assert.False(t, matches)
		assert.False(t, exists)
	})

	t.Run("returns false, false when MMDS returns empty hash with nil request", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: "", err: nil}
		api := newTestAPI(nil, mmdsClient)

		matches, exists := api.checkMMDSHash(ctx, nil)

		assert.False(t, matches)
		assert.False(t, exists)
	})

	t.Run("returns true, true when MMDS returns hash of empty string with nil request (explicit reset)", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: keys.HashAccessToken(""), err: nil}
		api := newTestAPI(nil, mmdsClient)

		matches, exists := api.checkMMDSHash(ctx, nil)

		assert.True(t, matches)
		assert.True(t, exists)
	})
}

func TestSetData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	logger := zerolog.Nop()

	t.Run("access token updates", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name           string
			existingToken  *SecureToken
			requestToken   *SecureToken
			mmdsHash       string
			mmdsErr        error
			wantErr        error
			wantFinalToken *SecureToken
		}{
			{
				name:           "first-time setup: sets initial token",
				existingToken:  nil,
				requestToken:   secureTokenPtr("initial-token"),
				mmdsHash:       "",
				mmdsErr:        assert.AnError,
				wantErr:        nil,
				wantFinalToken: secureTokenPtr("initial-token"),
			},
			{
				name:           "first-time setup: nil request token leaves token unset",
				existingToken:  nil,
				requestToken:   nil,
				mmdsHash:       "",
				mmdsErr:        assert.AnError,
				wantErr:        nil,
				wantFinalToken: nil,
			},
			{
				name:           "re-init with same token: token unchanged",
				existingToken:  secureTokenPtr("same-token"),
				requestToken:   secureTokenPtr("same-token"),
				mmdsHash:       "",
				mmdsErr:        assert.AnError,
				wantErr:        nil,
				wantFinalToken: secureTokenPtr("same-token"),
			},
			{
				name:           "resume with MMDS: updates token when hash matches",
				existingToken:  secureTokenPtr("old-token"),
				requestToken:   secureTokenPtr("new-token"),
				mmdsHash:       keys.HashAccessToken("new-token"),
				mmdsErr:        nil,
				wantErr:        nil,
				wantFinalToken: secureTokenPtr("new-token"),
			},
			{
				name:           "resume with MMDS: fails when hash doesn't match",
				existingToken:  secureTokenPtr("old-token"),
				requestToken:   secureTokenPtr("new-token"),
				mmdsHash:       keys.HashAccessToken("different-token"),
				mmdsErr:        nil,
				wantErr:        ErrAccessTokenMismatch,
				wantFinalToken: secureTokenPtr("old-token"),
			},
			{
				name:           "fails when existing token and request token mismatch without MMDS",
				existingToken:  secureTokenPtr("existing-token"),
				requestToken:   secureTokenPtr("wrong-token"),
				mmdsHash:       "",
				mmdsErr:        assert.AnError,
				wantErr:        ErrAccessTokenMismatch,
				wantFinalToken: secureTokenPtr("existing-token"),
			},
			{
				name:           "conflict when existing token but nil request token",
				existingToken:  secureTokenPtr("existing-token"),
				requestToken:   nil,
				mmdsHash:       "",
				mmdsErr:        assert.AnError,
				wantErr:        ErrAccessTokenResetNotAuthorized,
				wantFinalToken: secureTokenPtr("existing-token"),
			},
			{
				name:           "conflict when existing token but nil request with MMDS present",
				existingToken:  secureTokenPtr("existing-token"),
				requestToken:   nil,
				mmdsHash:       keys.HashAccessToken("some-token"),
				mmdsErr:        nil,
				wantErr:        ErrAccessTokenResetNotAuthorized,
				wantFinalToken: secureTokenPtr("existing-token"),
			},
			{
				name:           "conflict when MMDS returns empty hash and request is nil (prevents unauthorized reset)",
				existingToken:  secureTokenPtr("existing-token"),
				requestToken:   nil,
				mmdsHash:       "",
				mmdsErr:        nil,
				wantErr:        ErrAccessTokenResetNotAuthorized,
				wantFinalToken: secureTokenPtr("existing-token"),
			},
			{
				name:           "resets token when MMDS returns hash of empty string and request is nil (explicit reset)",
				existingToken:  secureTokenPtr("existing-token"),
				requestToken:   nil,
				mmdsHash:       keys.HashAccessToken(""),
				mmdsErr:        nil,
				wantErr:        nil,
				wantFinalToken: nil,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				mmdsClient := &mockMMDSClient{hash: tt.mmdsHash, err: tt.mmdsErr}
				api := newTestAPI(tt.existingToken, mmdsClient)

				data := PostInitJSONBody{
					AccessToken: tt.requestToken,
				}

				err := api.SetData(ctx, logger, data)

				if tt.wantErr != nil {
					require.ErrorIs(t, err, tt.wantErr)
				} else {
					require.NoError(t, err)
				}

				if tt.wantFinalToken == nil {
					assert.False(t, api.accessToken.IsSet(), "expected token to not be set")
				} else {
					require.True(t, api.accessToken.IsSet(), "expected token to be set")
					assert.True(t, api.accessToken.EqualsSecure(tt.wantFinalToken), "expected token to match")
				}
			})
		}
	})

	t.Run("sets environment variables", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: "", err: assert.AnError}
		api := newTestAPI(nil, mmdsClient)

		envVars := EnvVars{"FOO": "bar", "BAZ": "qux"}
		data := PostInitJSONBody{
			EnvVars: &envVars,
		}

		err := api.SetData(ctx, logger, data)

		require.NoError(t, err)
		val, ok := api.defaults.EnvVars.Load("FOO")
		assert.True(t, ok)
		assert.Equal(t, "bar", val)
		val, ok = api.defaults.EnvVars.Load("BAZ")
		assert.True(t, ok)
		assert.Equal(t, "qux", val)
	})

	t.Run("sets default user", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: "", err: assert.AnError}
		api := newTestAPI(nil, mmdsClient)

		data := PostInitJSONBody{
			DefaultUser: utilsShared.ToPtr("testuser"),
		}

		err := api.SetData(ctx, logger, data)

		require.NoError(t, err)
		assert.Equal(t, "testuser", api.defaults.User)
	})

	t.Run("does not set default user when empty", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: "", err: assert.AnError}
		api := newTestAPI(nil, mmdsClient)
		api.defaults.User = "original"

		data := PostInitJSONBody{
			DefaultUser: utilsShared.ToPtr(""),
		}

		err := api.SetData(ctx, logger, data)

		require.NoError(t, err)
		assert.Equal(t, "original", api.defaults.User)
	})

	t.Run("sets default workdir", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: "", err: assert.AnError}
		api := newTestAPI(nil, mmdsClient)

		data := PostInitJSONBody{
			DefaultWorkdir: utilsShared.ToPtr("/home/user"),
		}

		err := api.SetData(ctx, logger, data)

		require.NoError(t, err)
		require.NotNil(t, api.defaults.Workdir)
		assert.Equal(t, "/home/user", *api.defaults.Workdir)
	})

	t.Run("does not set default workdir when empty", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: "", err: assert.AnError}
		api := newTestAPI(nil, mmdsClient)
		originalWorkdir := "/original"
		api.defaults.Workdir = &originalWorkdir

		data := PostInitJSONBody{
			DefaultWorkdir: utilsShared.ToPtr(""),
		}

		err := api.SetData(ctx, logger, data)

		require.NoError(t, err)
		require.NotNil(t, api.defaults.Workdir)
		assert.Equal(t, "/original", *api.defaults.Workdir)
	})

	t.Run("sets multiple fields at once", func(t *testing.T) {
		t.Parallel()
		mmdsClient := &mockMMDSClient{hash: "", err: assert.AnError}
		api := newTestAPI(nil, mmdsClient)

		envVars := EnvVars{"KEY": "value"}
		data := PostInitJSONBody{
			AccessToken:    secureTokenPtr("token"),
			DefaultUser:    utilsShared.ToPtr("user"),
			DefaultWorkdir: utilsShared.ToPtr("/workdir"),
			EnvVars:        &envVars,
		}

		err := api.SetData(ctx, logger, data)

		require.NoError(t, err)
		assert.True(t, api.accessToken.Equals("token"), "expected token to match")
		assert.Equal(t, "user", api.defaults.User)
		assert.Equal(t, "/workdir", *api.defaults.Workdir)
		val, ok := api.defaults.EnvVars.Load("KEY")
		assert.True(t, ok)
		assert.Equal(t, "value", val)
	})
}
