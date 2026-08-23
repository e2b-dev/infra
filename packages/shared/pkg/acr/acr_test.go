package acr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorepolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCredential stands in for an AAD credential without reaching the network.
type fakeCredential struct {
	token string
	err   error
}

func (f fakeCredential) GetToken(context.Context, azcorepolicy.TokenRequestOptions) (azcore.AccessToken, error) {
	if f.err != nil {
		return azcore.AccessToken{}, f.err
	}

	return azcore.AccessToken{Token: f.token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// newTestAuthenticator points an Authenticator at a TLS test server standing in for ACR.
func newTestAuthenticator(t *testing.T, handler http.HandlerFunc) *Authenticator {
	t.Helper()

	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	a, err := NewAuthenticator(
		strings.TrimPrefix(srv.URL, "https://"),
		fakeCredential{token: "aad-access-token"},
		&azcontainerregistry.AuthenticationClientOptions{
			ClientOptions: azcore.ClientOptions{Transport: srv.Client()},
		},
	)
	require.NoError(t, err)

	return a
}

func exchangeOK(t *testing.T, checkForm bool) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oauth2/exchange", r.URL.Path)

		if checkForm {
			assert.NoError(t, r.ParseForm())
			// The AAD token must be what is exchanged; sending anything else would
			// fail against a real registry in a way no unit test would notice.
			assert.Equal(t, "aad-access-token", r.PostForm.Get("access_token"))
			assert.Equal(t, "access_token", r.PostForm.Get("grant_type"))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"refresh_token":"acr-refresh-token"}`))
	}
}

func TestAuthorizationExchangesAADTokenForRefreshToken(t *testing.T) {
	t.Parallel()

	a := newTestAuthenticator(t, exchangeOK(t, true))

	auth, err := a.Authorization()
	require.NoError(t, err)
	assert.Equal(t, "acr-refresh-token", auth.Password)
	// ACR requires this exact username alongside a refresh token.
	assert.Equal(t, "00000000-0000-0000-0000-000000000000", auth.Username)
}

func TestAuthorizationFailsOnDenied(t *testing.T) {
	t.Parallel()

	a := newTestAuthenticator(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"code":"DENIED"}]}`))
	})

	_, err := a.Authorization()
	require.Error(t, err)
	// The status has to reach the caller: "denied" and "registry unreachable" are different
	// operator problems and the message is the only thing that distinguishes them.
	assert.Contains(t, err.Error(), "403")
}

func TestAuthorizationFailsOnEmptyRefreshToken(t *testing.T) {
	t.Parallel()

	// A 200 with no refresh token would otherwise become an empty docker password, which
	// surfaces as an unauthenticated pull rather than as this failure.
	a := newTestAuthenticator(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	_, err := a.Authorization()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refresh token")
}

func TestAuthorizationCachesRefreshToken(t *testing.T) {
	t.Parallel()

	// One exchange per TTL, not one per pull: the registry rate-limits
	// /oauth2/exchange and a cold node fans out many concurrent pulls.
	var mu sync.Mutex
	exchanges := 0
	a := newTestAuthenticator(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		exchanges++
		mu.Unlock()
		exchangeOK(t, false)(w, r)
	})

	for range 3 {
		auth, err := a.Authorization()
		require.NoError(t, err)
		assert.Equal(t, "acr-refresh-token", auth.Password)
	}

	assert.Equal(t, 1, exchanges, "cached token must be reused within its TTL")

	// A soft-expired entry triggers a fresh exchange, not a stale serve.
	a.mu.Lock()
	a.softExpiry = time.Now().Add(-time.Minute)
	a.mu.Unlock()

	_, err := a.Authorization()
	require.NoError(t, err)
	assert.Equal(t, 2, exchanges, "soft-expired token must be re-exchanged")
}

func TestAuthorizationServesStaleTokenWhileExchangeErrors(t *testing.T) {
	t.Parallel()

	// A transient AAD/exchange outage inside the hard-TTL window must be a
	// non-event: the cached token still authenticates for over an hour.
	var mu sync.Mutex
	failing := false
	a := newTestAuthenticator(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		f := failing
		mu.Unlock()
		if f {
			w.WriteHeader(http.StatusForbidden) // 4xx: azcore does not retry it

			return
		}
		exchangeOK(t, false)(w, r)
	})

	_, err := a.Authorization()
	require.NoError(t, err)

	mu.Lock()
	failing = true
	mu.Unlock()
	a.mu.Lock()
	a.softExpiry = time.Now().Add(-time.Minute) // soft-expired, hard still valid
	a.mu.Unlock()

	auth, err := a.Authorization()
	require.NoError(t, err, "stale-while-error must serve the cached token")
	assert.Equal(t, "acr-refresh-token", auth.Password)

	// Past the hard TTL the stale token may no longer authenticate; the
	// failure must surface.
	a.mu.Lock()
	a.hardExpiry = time.Now().Add(-time.Minute)
	a.mu.Unlock()

	_, err = a.Authorization()
	require.Error(t, err, "a hard-expired cache must not be served")
}

func TestInvalidateForcesReExchange(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	exchanges := 0
	a := newTestAuthenticator(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		exchanges++
		mu.Unlock()
		exchangeOK(t, false)(w, r)
	})

	_, err := a.Authorization()
	require.NoError(t, err)

	a.Invalidate()

	_, err = a.Authorization()
	require.NoError(t, err)
	assert.Equal(t, 2, exchanges, "Invalidate must drop the cache")
}

func TestConcurrentAuthorizationSharesOneWalk(t *testing.T) {
	t.Parallel()

	// A cold node fans out many pulls at once; they must share one exchange,
	// not race N of them.
	var mu sync.Mutex
	exchanges := 0
	release := make(chan struct{})
	a := newTestAuthenticator(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		exchanges++
		mu.Unlock()
		<-release
		exchangeOK(t, false)(w, r)
	})

	results := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			auth, err := a.Authorization()
			if err == nil && auth.Password != "acr-refresh-token" {
				err = fmt.Errorf("unexpected password %q", auth.Password)
			}
			results <- err
		})
	}

	// Give the goroutines time to queue on the single in-flight walk, then
	// let the one exchange answer.
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)

	for err := range results {
		require.NoError(t, err)
	}
	assert.Equal(t, 1, exchanges, "concurrent callers must share one exchange")
}

func TestAuthorizationContextHonorsCancellation(t *testing.T) {
	t.Parallel()

	// A cancelled caller must stop waiting; the shared walk itself keeps
	// running detached for the callers that still want the result.
	started := make(chan struct{})
	release := make(chan struct{})
	a := newTestAuthenticator(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		exchangeOK(t, false)(w, r)
	})
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-started
		cancel()
	}()

	_, err := a.AuthorizationContext(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestIsUnauthorized(t *testing.T) {
	t.Parallel()

	assert.True(t, IsUnauthorized(&transport.Error{StatusCode: http.StatusUnauthorized}))
	assert.True(t, IsUnauthorized(&transport.Error{StatusCode: http.StatusForbidden}))
	assert.False(t, IsUnauthorized(&transport.Error{StatusCode: http.StatusNotFound}))
	assert.False(t, IsUnauthorized(context.Canceled))
	assert.False(t, IsUnauthorized(nil))
}
