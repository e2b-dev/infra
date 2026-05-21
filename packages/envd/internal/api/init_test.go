package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
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
			err := os.WriteFile(inputPath, []byte(value), 0o644)
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
		EnvVars: utils.NewEnvVars(),
	}
	api := New(&logger, defaults, nil, false, cgroups.NewNoopManager())
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
	ctx := t.Context()
	logger := zerolog.Nop()

	t.Run("access token updates", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name           string
			existingToken  *SecureToken
			requestToken   *SecureToken
			wantFinalToken *SecureToken
		}{
			{
				name:           "first-time setup: sets initial token",
				existingToken:  nil,
				requestToken:   secureTokenPtr("initial-token"),
				wantFinalToken: secureTokenPtr("initial-token"),
			},
			{
				name:           "first-time setup: nil request token leaves token unset",
				existingToken:  nil,
				requestToken:   nil,
				wantFinalToken: nil,
			},
			{
				name:           "re-init with same token: token unchanged",
				existingToken:  secureTokenPtr("same-token"),
				requestToken:   secureTokenPtr("same-token"),
				wantFinalToken: secureTokenPtr("same-token"),
			},
			{
				name:           "updates token when request has new token",
				existingToken:  secureTokenPtr("old-token"),
				requestToken:   secureTokenPtr("new-token"),
				wantFinalToken: secureTokenPtr("new-token"),
			},
			{
				name:           "clears token when request is nil and existing token is set",
				existingToken:  secureTokenPtr("existing-token"),
				requestToken:   nil,
				wantFinalToken: nil,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				mmdsClient := &mockMMDSClient{}
				api := newTestAPI(tt.existingToken, mmdsClient)

				data := PostInitJSONBody{
					AccessToken: tt.requestToken,
				}

				err := api.SetData(ctx, logger, data)
				require.NoError(t, err)

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
			DefaultUser: new("testuser"),
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
			DefaultUser: new(""),
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
			DefaultWorkdir: new("/home/user"),
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
			DefaultWorkdir: new(""),
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
			DefaultUser:    new("user"),
			DefaultWorkdir: new("/workdir"),
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

func TestShouldRemountNFS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		isMounted          bool
		mountedLifecycleID string
		requestLifecycleID string
		wantRemount        bool
	}{
		{
			name:               "not mounted: should mount",
			isMounted:          false,
			mountedLifecycleID: "",
			requestLifecycleID: "",
			wantRemount:        true,
		},
		{
			name:               "not mounted with request lifecycle: should mount",
			isMounted:          false,
			mountedLifecycleID: "",
			requestLifecycleID: "abc",
			wantRemount:        true,
		},
		{
			name:               "mounted empty + request empty: no remount (would cause infinite loop)",
			isMounted:          true,
			mountedLifecycleID: "",
			requestLifecycleID: "",
			wantRemount:        false,
		},
		{
			name:               "mounted with lifecycle + request empty: remount (lifecycle cleared)",
			isMounted:          true,
			mountedLifecycleID: "abc",
			requestLifecycleID: "",
			wantRemount:        true,
		},
		{
			name:               "mounted empty + request with lifecycle: remount (new lifecycle)",
			isMounted:          true,
			mountedLifecycleID: "",
			requestLifecycleID: "abc",
			wantRemount:        true,
		},
		{
			name:               "mounted + request same lifecycle: no remount",
			isMounted:          true,
			mountedLifecycleID: "abc",
			requestLifecycleID: "abc",
			wantRemount:        false,
		},
		{
			name:               "mounted + request different lifecycle: remount",
			isMounted:          true,
			mountedLifecycleID: "abc",
			requestLifecycleID: "xyz",
			wantRemount:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := shouldRemountNFS(tt.isMounted, tt.mountedLifecycleID, tt.requestLifecycleID)

			assert.Equal(t, tt.wantRemount, got)
		})
	}
}

type fakeCgroupManager struct {
	mu               sync.Mutex
	frozen           []cgroups.ProcessType
	freezeErr        error
	unfrozen         []cgroups.ProcessType
	unfreezeAttempts []cgroups.ProcessType
	unfreezeErr      error
}

func (f *fakeCgroupManager) GetFileDescriptor(cgroups.ProcessType) (int, bool) {
	return 0, false
}

func (f *fakeCgroupManager) Freeze(pt cgroups.ProcessType) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.freezeErr != nil {
		return f.freezeErr
	}
	f.frozen = append(f.frozen, pt)

	return nil
}

func (f *fakeCgroupManager) Unfreeze(pt cgroups.ProcessType) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unfreezeAttempts = append(f.unfreezeAttempts, pt)
	if f.unfreezeErr != nil {
		return f.unfreezeErr
	}
	f.unfrozen = append(f.unfrozen, pt)

	return nil
}

func (f *fakeCgroupManager) Close() error { return nil }

func newAPIWithCgroupManager(mgr cgroups.Manager) *API {
	logger := zerolog.Nop()

	return New(&logger, &execcontext.Defaults{EnvVars: utils.NewEnvVars()}, nil, false, mgr)
}

func TestPostFreeze(t *testing.T) {
	t.Parallel()

	t.Run("freezes all user cgroups", func(t *testing.T) {
		t.Parallel()
		mgr := &fakeCgroupManager{}
		api := newAPIWithCgroupManager(mgr)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/freeze", http.NoBody)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		api.PostFreeze(rec, req)

		require.Equal(t, http.StatusNoContent, rec.Code)
		assert.Equal(t, userCgroupsToFreeze, mgr.frozen)
	})

	t.Run("returns 500 on freeze error", func(t *testing.T) {
		t.Parallel()
		mgr := &fakeCgroupManager{freezeErr: errors.New("write cgroup.freeze: io error")}
		api := newAPIWithCgroupManager(mgr)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/freeze", http.NoBody)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		api.PostFreeze(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Empty(t, mgr.frozen)
	})
}

func TestPostUnfreeze(t *testing.T) {
	t.Parallel()

	t.Run("unfreezes all user cgroups", func(t *testing.T) {
		t.Parallel()
		mgr := &fakeCgroupManager{}
		api := newAPIWithCgroupManager(mgr)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/unfreeze", http.NoBody)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		api.PostUnfreeze(rec, req)

		require.Equal(t, http.StatusNoContent, rec.Code)
		assert.Equal(t, userCgroupsToFreeze, mgr.unfrozen)
	})

	t.Run("returns 500 but attempts every cgroup on unfreeze error", func(t *testing.T) {
		t.Parallel()
		mgr := &fakeCgroupManager{unfreezeErr: errors.New("write cgroup.freeze: io error")}
		api := newAPIWithCgroupManager(mgr)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/unfreeze", http.NoBody)
		require.NoError(t, err)
		rec := httptest.NewRecorder()
		api.PostUnfreeze(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Empty(t, mgr.unfrozen)
		assert.Equal(t, userCgroupsToFreeze, mgr.unfreezeAttempts)
	})
}

// Stale /init (Timestamp older than lastSetTime) must still thaw user cgroups
// even though SetData is skipped.
func TestPostInit_UnfreezeOnStaleTimestamp(t *testing.T) {
	t.Parallel()

	mgr := &fakeCgroupManager{}
	api := newAPIWithCgroupManager(mgr)
	api.isNotFC = true

	now := time.Now()
	require.True(t, api.lastSetTime.SetToGreater(now.UnixNano()))

	stale := now.Add(-1 * time.Minute)
	body, err := json.Marshal(PostInitJSONBody{
		Timestamp: &stale,
		EnvVars:   &EnvVars{"SHOULD_NOT_BE_SET": "x"},
	})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/init", bytes.NewReader(body))
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	api.PostInit(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	_, ok := api.defaults.EnvVars.Load("SHOULD_NOT_BE_SET")
	assert.False(t, ok, "stale /init should not apply EnvVars")
	assert.Equal(t, userCgroupsToFreeze, mgr.unfrozen, "stale /init must still unfreeze")
}

// Unauthorized /init must NOT thaw cgroups.
func TestPostInit_UnauthorizedDoesNotUnfreeze(t *testing.T) {
	t.Parallel()

	mgr := &fakeCgroupManager{}
	api := newAPIWithCgroupManager(mgr)
	api.isNotFC = true
	api.accessToken.TakeFrom(secureTokenPtr("real-token"))

	body := []byte(`{"accessToken":"wrong-token"}`)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/init", bytes.NewReader(body))
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	api.PostInit(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, mgr.unfreezeAttempts, "unauthorized /init must not attempt unfreeze")
}
