package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/connlimit"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
)

// unroutableHandler is the handler as it behaves when the sandbox cannot be
// resolved, which is the only path where the proxy answers on its own behalf.
func unroutableHandler(err error) http.HandlerFunc {
	return handler(nil, func(*http.Request) (*pool.Destination, error) { return nil, err }, nil)
}

// Without this, the browser rejects the preflight and never sends the request
// the error was meant for, so JS sees an opaque network error instead of the
// status telling it the sandbox is gone.
func TestHandlerAnswersPreflightForMissingSandbox(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/health", nil)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	r.Header.Set("Access-Control-Request-Headers", "e2b-sandbox-id,e2b-sandbox-port")
	w := httptest.NewRecorder()

	unroutableHandler(NewErrSandboxNotFound("im9r2ycjiy2534qsdy1oo")).ServeHTTP(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "e2b-sandbox-id,e2b-sandbox-port", w.Header().Get("Access-Control-Allow-Headers"))
	assert.Empty(t, w.Body.String())
}

// A bare OPTIONS is a normal request, not a preflight, so it must still get the
// error the sandbox lookup produced.
func TestHandlerDoesNotShortCircuitBareOptions(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/health", nil)
	w := httptest.NewRecorder()

	unroutableHandler(NewErrSandboxNotFound("im9r2ycjiy2534qsdy1oo")).ServeHTTP(w, r)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.JSONEq(t,
		`{"sandboxId":"im9r2ycjiy2534qsdy1oo","message":"The sandbox was not found","code":502}`,
		w.Body.String())
}

// The connection limit is reached only when the sandbox resolved, so it sits
// past the handler's err != nil preflight check and needs its own. The 429 it
// would otherwise answer with is not an ok status, so the browser would reject
// the preflight and JS would never get to read the limit it hit.
func TestHandlerAnswersPreflightForConnectionLimitedSandbox(t *testing.T) {
	t.Parallel()

	var blocked atomic.Int64
	connLimitConfig := &ConnectionLimitConfig{
		Limiter:             connlimit.NewConnectionLimiter(),
		GetMaxLimit:         func(context.Context) int { return 0 },
		OnConnectionBlocked: func(context.Context) { blocked.Add(1) },
	}
	h := handler(nil, func(*http.Request) (*pool.Destination, error) {
		return &pool.Destination{
			SandboxId:     "im9r2ycjiy2534qsdy1oo",
			RequestLogger: logger.NewNopLogger(),
			ConnectionKey: "limited",
		}, nil
	}, connLimitConfig)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/health", nil)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	// Answering the preflight never reaches the sandbox, so no connection was
	// blocked and the metric must not claim otherwise.
	assert.Zero(t, blocked.Load())

	// The real request that follows still gets the limit error, with the headers
	// that let JS read it.
	r = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil)
	r.Header.Set("Origin", "https://app.example.com")
	w = httptest.NewRecorder()

	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, int64(1), blocked.Load())
}

// The raw http.Error paths are equally unreadable from a browser, so they carry
// the headers too.
func TestHandlerSetsCORSHeadersOnRawErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		err        error
		statusCode int
	}{
		{name: "invalid host", err: ErrInvalidHost, statusCode: http.StatusBadRequest},
		{name: "invalid sandbox ID", err: ErrInvalidSandboxID, statusCode: http.StatusBadRequest},
		{name: "missing header", err: MissingHeaderError{}, statusCode: http.StatusBadRequest},
		{name: "invalid sandbox port", err: &InvalidSandboxPortError{Port: "abc"}, statusCode: http.StatusBadRequest},
		{name: "unexpected", err: assert.AnError, statusCode: http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil)
			r.Header.Set("Origin", "https://app.example.com")
			w := httptest.NewRecorder()

			unroutableHandler(tt.err).ServeHTTP(w, r)

			require.Equal(t, tt.statusCode, w.Code)
			assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
		})
	}
}

// response is what a browser gets to see about a proxy-synthesized reply: the
// status and the headers, which is all these tests assert on.
type response struct {
	statusCode int
	header     http.Header
}

func do(t *testing.T, r *http.Request) response {
	t.Helper()

	resp, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return response{statusCode: resp.StatusCode, header: resp.Header}
}

func preflight(t *testing.T, port uint) response {
	t.Helper()

	r, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, fmt.Sprintf("http://127.0.0.1:%d/health", port), nil)
	require.NoError(t, err)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Access-Control-Request-Method", http.MethodGet)
	r.Header.Set("Access-Control-Request-Headers", "e2b-sandbox-id,e2b-sandbox-port")

	return do(t, r)
}

func crossOriginGet(t *testing.T, port uint) response {
	t.Helper()

	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/health", port), nil)
	require.NoError(t, err)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Sec-Fetch-Mode", "cors")

	return do(t, r)
}

// Reproduces the browser's two-step exchange against a missing sandbox: the
// preflight must be ok or the browser never sends the GET whose 502 is the whole
// answer the SDK is after.
func TestProxyCORSForMissingSandbox(t *testing.T) {
	t.Parallel()

	proxy, port, err := newTestProxy(t, func(*http.Request) (*pool.Destination, error) {
		return nil, NewErrSandboxNotFound("test-sandbox")
	})
	require.NoError(t, err)
	t.Cleanup(func() { proxy.Close() })

	resp := preflight(t, port)
	assert.Equal(t, http.StatusNoContent, resp.statusCode)
	assert.Equal(t, "*", resp.header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "e2b-sandbox-id,e2b-sandbox-port", resp.header.Get("Access-Control-Allow-Headers"))

	resp = crossOriginGet(t, port)
	assert.Equal(t, http.StatusBadGateway, resp.statusCode)
	assert.Equal(t, "*", resp.header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "application/json; charset=utf-8", resp.header.Get("Content-Type"))
}

// Same for a sandbox that resolves but cannot be reached, which is answered by
// the pool's error handler instead.
func TestProxyCORSForUnreachableSandbox(t *testing.T) {
	t.Parallel()

	var lisCfg net.ListenConfig
	listener, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	backend, err := newTestBackend(listener, "backend-1")
	require.NoError(t, err)
	// Nothing is listening on the backend address any more.
	backend.Close()

	proxy, port, err := newTestProxy(t, func(*http.Request) (*pool.Destination, error) {
		return &pool.Destination{
			Url:                backend.url,
			SandboxId:          "test-sandbox",
			SandboxPort:        3000,
			RequestLogger:      logger.NewNopLogger(),
			ConnectionKey:      backend.id,
			DefaultToPortError: true,
		}, nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { proxy.Close() })

	resp := preflight(t, port)
	assert.Equal(t, http.StatusNoContent, resp.statusCode)
	assert.Equal(t, "*", resp.header.Get("Access-Control-Allow-Origin"))

	resp = crossOriginGet(t, port)
	assert.Equal(t, http.StatusBadGateway, resp.statusCode)
	assert.Equal(t, "*", resp.header.Get("Access-Control-Allow-Origin"))
}

// A live sandbox sends its own CORS headers, so the proxy must not add a second
// value — a browser rejects a response carrying two Access-Control-Allow-Origin
// headers just as it rejects one carrying none.
func TestProxyDoesNotDuplicateSandboxCORSHeaders(t *testing.T) {
	t.Parallel()

	var lisCfg net.ListenConfig
	listener, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET")
			w.WriteHeader(http.StatusNoContent)
		}),
		ReadHeaderTimeout: time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Logf("backend server: %v", err)
		}
	}()
	t.Cleanup(func() { server.Close() })

	backendURL := &url.URL{Scheme: "http", Host: listener.Addr().String()}
	proxy, port, err := newTestProxy(t, func(*http.Request) (*pool.Destination, error) {
		return &pool.Destination{
			Url:           backendURL,
			SandboxId:     "test-sandbox",
			RequestLogger: logger.NewNopLogger(),
			ConnectionKey: "live",
		}, nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { proxy.Close() })

	for _, resp := range []response{preflight(t, port), crossOriginGet(t, port)} {
		assert.Equal(t, []string{"*"}, resp.header.Values("Access-Control-Allow-Origin"))
		assert.Equal(t, []string{"GET"}, resp.header.Values("Access-Control-Allow-Methods"))
	}
}
