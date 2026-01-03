package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// testBackend represents a test backend server
type testBackend struct {
	server       *http.Server
	listener     net.Listener
	url          *url.URL
	requestCount *atomic.Uint64
	id           string
	cancel       context.CancelFunc
}

func (b *testBackend) RequestCount() uint64 {
	return b.requestCount.Load()
}

const bodyWriteDelayHeader = "body-write-delay"

// newTestBackend creates a new test backend server
func newTestBackend(listener net.Listener, id string) (*testBackend, error) {
	var requestCount atomic.Uint64

	ctx, cancel := context.WithCancel(context.Background())

	backend := &testBackend{
		server: &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-ctx.Done():
					w.WriteHeader(http.StatusBadGateway)

					return
				default:
				}

				requestCount.Add(1)

				w.WriteHeader(http.StatusOK)

				// Flush the headers, so we can read the headers and body separately after .Do() returns.
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}

				// Check for "body-write-delay" header (interpreted as seconds)
				delayHeader := r.Header.Get(bodyWriteDelayHeader)

				if delayHeader != "" {
					if n, err := time.ParseDuration(delayHeader); err == nil {
						time.Sleep(n)
					}
				}

				w.Write([]byte(id))
			}),
		},
		listener:     listener,
		requestCount: &requestCount,
		id:           id,
		cancel:       cancel,
	}

	// Start the server
	go backend.server.Serve(backend.listener)

	// Parse the URL
	backendURL, err := url.Parse(fmt.Sprintf("http://%s", listener.Addr().String()))
	if err != nil {
		listener.Close()

		return nil, fmt.Errorf("failed to parse backend URL: %w", err)
	}
	backend.url = backendURL

	return backend, nil
}

// Interrupt closes the listener.
// We close the listener directly because we want to simulate ungraceful shutdown of the backend
// that happens when a sandbox is killed.
func (b *testBackend) Interrupt() error {
	var errs []error
	err := b.listener.Close()
	if err != nil {
		errs = append(errs, err)
	}

	b.cancel()

	return errors.Join(errs...)
}

func (b *testBackend) Close() error {
	return b.server.Close()
}

func assertBackendOutput(t *testing.T, backend *testBackend, resp *http.Response) {
	t.Helper()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "status code should be 200")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, string(body), backend.id, "backend id should be the same")
}

func assertStreamError(t *testing.T, resp *http.Response) {
	t.Helper()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "status code should be 200")

	_, err := io.ReadAll(resp.Body)
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

// newTestProxy creates a new proxy server for testing
func newTestProxy(t *testing.T, getDestination func(r *http.Request) (*pool.Destination, error)) (*Proxy, uint, error) {
	t.Helper()

	// Find a free port for the proxy
	var lisCfg net.ListenConfig
	l, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get free port: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port

	// Set up the proxy server
	proxy := New(
		uint16(port),
		SandboxProxyRetries,
		20*time.Second, // Short idle timeout
		getDestination,
		false,
	)

	// Start the proxy server
	go func() {
		proxy.Serve(l)
	}()

	return proxy, uint(port), nil
}

func TestProxyRoutesToTargetServer(t *testing.T) {
	t.Parallel()
	var lisCfg net.ListenConfig
	listener, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	backend, err := newTestBackend(listener, "backend-1")
	require.NoError(t, err)
	defer backend.Close()

	// Set up a routing function that always returns the backend
	getDestination := func(*http.Request) (*pool.Destination, error) {
		return &pool.Destination{
			Url:           backend.url,
			SandboxId:     "test-sandbox",
			RequestLogger: logger.NewNopLogger(),
			ConnectionKey: backend.id,
		}, nil
	}

	proxy, port, err := newTestProxy(t, getDestination)
	require.NoError(t, err)
	defer proxy.Close()

	assert.Equal(t, uint64(0), proxy.TotalPoolConnections())
	assert.Equal(t, uint64(0), backend.RequestCount())

	// Make a request to the proxy
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/hello", port)
	resp, err := httpGet(t, proxyURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assertBackendOutput(t, backend, resp)

	assert.Equal(t, uint64(1), backend.RequestCount(), "backend should have been called once")
	assert.Equal(t, uint64(1), proxy.TotalPoolConnections(), "proxy should have established one connection")
}

func httpGet(t *testing.T, proxyURL string) (*http.Response, error) {
	t.Helper()

	return httpGetWithHeaders(t, proxyURL, nil)
}

func httpGetWithBodyWriteDelay(t *testing.T, proxyURL string, bodyWriteDelay time.Duration) (*http.Response, error) {
	t.Helper()

	return httpGetWithHeaders(t, proxyURL, http.Header{bodyWriteDelayHeader: {bodyWriteDelay.String()}})
}

func httpGetWithHeaders(t *testing.T, proxyURL string, headers http.Header) (*http.Response, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, proxyURL, nil)
	if err != nil {
		return nil, err
	}

	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	rsp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}

	return rsp, nil
}

type instrumentedConn struct {
	net.Conn

	listener *instrumentedListener
}

func (c *instrumentedConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err != nil {
		c.listener.AddReadError(err)
	}

	return n, err
}

func (l *instrumentedListener) AddReadError(err error) {
	select {
	case l.FirstReadErr <- err:
	default:
	}
}

type instrumentedListener struct {
	net.Listener

	FirstReadErr chan error
}

func newInstrumentedListener(l net.Listener) *instrumentedListener {
	return &instrumentedListener{
		Listener:     l,
		FirstReadErr: make(chan error, 1),
	}
}

func (l *instrumentedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return &instrumentedConn{Conn: conn, listener: l}, nil
}

func TestProxyReusesConnections(t *testing.T) {
	t.Parallel()
	var lisCfg net.ListenConfig
	listener, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	backend, err := newTestBackend(listener, "backend-1")
	require.NoError(t, err)
	defer backend.Close()

	// Set up a routing function that always returns the backend
	getDestination := func(*http.Request) (*pool.Destination, error) {
		return &pool.Destination{
			Url:           backend.url,
			SandboxId:     "test-sandbox",
			RequestLogger: logger.NewNopLogger(),
			ConnectionKey: backend.id,
		}, nil
	}

	proxy, port, err := newTestProxy(t, getDestination)
	require.NoError(t, err)
	defer proxy.Close()

	// Make two requests to the proxy
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/hello", port)

	// First request
	resp1, err := httpGet(t, proxyURL)
	require.NoError(t, err)
	defer resp1.Body.Close()

	assertBackendOutput(t, backend, resp1)

	// Second request
	resp2, err := httpGet(t, proxyURL)
	require.NoError(t, err)
	defer resp2.Body.Close()

	assertBackendOutput(t, backend, resp2)

	// Verify that only one connection was established
	assert.Equal(t, uint64(2), backend.RequestCount(), "backend should have been called twice")
	assert.Equal(t, uint64(1), proxy.TotalPoolConnections(), "proxy should have used one connection")
}

func TestProxyCloseIdleConnectionsFromPool(t *testing.T) {
	t.Parallel()
	var lisCfg net.ListenConfig
	listener, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	backend, err := newTestBackend(listener, "backend-1")
	require.NoError(t, err)
	defer backend.Close()

	getDestination := func(*http.Request) (*pool.Destination, error) {
		return &pool.Destination{
			Url:           backend.url,
			SandboxId:     "test-sandbox",
			RequestLogger: logger.NewNopLogger(),
			ConnectionKey: backend.id,
		}, nil
	}

	proxy, port, err := newTestProxy(t, getDestination)
	require.NoError(t, err)
	defer proxy.Close()

	// Make a request to the proxy
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/hello", port)
	resp, err := httpGet(t, proxyURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assertBackendOutput(t, backend, resp)

	assert.Equal(t, uint64(1), proxy.TotalPoolConnections(), "proxy should have established one connection")
	assert.Equal(t, int64(1), proxy.CurrentPoolConnections(), "proxy should have established one connection that is still alive")
	assert.Equal(t, uint64(1), backend.RequestCount(), "backend should have been called once")

	// Remove the connection from the pool
	err = proxy.RemoveFromPool(backend.id)
	require.NoError(t, err)

	assert.Equal(t, uint64(1), proxy.TotalPoolConnections(), "proxy should have still one connection in the pool")
	assert.Equal(t, int64(0), proxy.CurrentPoolConnections(), "proxy should have removed the connection from the pool that is still alive")
}

func TestProxyResetAliveConnectionsFromPool(t *testing.T) {
	t.Parallel()
	var lisCfg net.ListenConfig

	listener, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	instrumentedListener := newInstrumentedListener(listener)

	backend, err := newTestBackend(instrumentedListener, "backend-1")
	require.NoError(t, err)
	defer backend.Close()

	getDestination := func(*http.Request) (*pool.Destination, error) {
		return &pool.Destination{
			Url:           backend.url,
			SandboxId:     "test-sandbox",
			RequestLogger: logger.NewNopLogger(),
			ConnectionKey: backend.id,
		}, nil
	}

	proxy, port, err := newTestProxy(t, getDestination)
	require.NoError(t, err)
	defer proxy.Close()

	requestEnded := make(chan struct{}, 1)

	go func() {
		defer close(requestEnded)

		// Make a request to the proxy
		proxyURL := fmt.Sprintf("http://127.0.0.1:%d/hello", port)
		resp, err := httpGetWithBodyWriteDelay(t, proxyURL, 10*time.Second)
		assert.NoError(t, err)
		defer resp.Body.Close()

		assertStreamError(t, resp)
	}()

	// Wait for the request to start being processed by the backend
	time.Sleep(1 * time.Second)

	assert.Equal(t, uint64(1), proxy.TotalPoolConnections(), "proxy should have established one connection")
	assert.Equal(t, int64(1), proxy.CurrentPoolConnections(), "proxy should have established one connection that is still alive")
	assert.Equal(t, uint64(1), backend.RequestCount(), "backend should have been called once")

	// Remove the connection from the pool
	err = proxy.RemoveFromPool(backend.id)
	require.NoError(t, err)

	assert.Equal(t, uint64(1), proxy.TotalPoolConnections(), "proxy should have still one connection in the pool")
	assert.Equal(t, int64(0), proxy.CurrentPoolConnections(), "proxy should have removed the connection from the pool that is still alive")

	select {
	case <-requestEnded:
	case <-t.Context().Done():
		t.Fatalf("request timed out: %v", t.Context().Err())
	}

	select {
	case readErr, ok := <-instrumentedListener.FirstReadErr:
		if !ok {
			t.Fatalf("read error channel closed")
		}
		require.ErrorContains(t, readErr, "connection reset by peer")

		// io.EOF is returned for the FIN packet.
		require.NotErrorIs(t, readErr, io.EOF, "server connection should have read error other than EOF")
	case <-t.Context().Done():
		t.Fatalf("read error timed out: %v", t.Context().Err())
	}
}

// This is a test that verify that the proxy reuse fails when the backend changes.
func TestProxyReuseConnectionsWhenBackendChangesFails(t *testing.T) {
	t.Parallel()
	// Create first backend
	var lisCfg net.ListenConfig
	listener, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	backend1, err := newTestBackend(listener, "backend-1")
	require.NoError(t, err)
	defer backend1.Close()

	// Get the address of the first backend
	backendAddr := backend1.listener.Addr().String()

	backendMapping := map[string]string{
		backendAddr: backend1.id,
	}
	var backendMappingMutex sync.Mutex

	// Set up a routing function that returns the current backend
	getDestination := func(_ *http.Request) (*pool.Destination, error) {
		backendMappingMutex.Lock()
		defer backendMappingMutex.Unlock()

		backendKey, ok := backendMapping[backendAddr]
		if !ok {
			return nil, fmt.Errorf("backend not found")
		}

		return &pool.Destination{
			Url:           backend1.url,
			SandboxId:     "backend1",
			RequestLogger: logger.NewNopLogger(),
			ConnectionKey: backendKey,
		}, nil
	}

	// Create proxy with the initial routing function
	proxy, port, err := newTestProxy(t, getDestination)
	require.NoError(t, err)
	defer proxy.Close()

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/hello", port)

	// Make request to first backend
	resp1, err := httpGet(t, proxyURL)
	require.NoError(t, err)
	defer resp1.Body.Close()

	assertBackendOutput(t, backend1, resp1)

	assert.Equal(t, uint64(1), proxy.TotalPoolConnections(), "proxy should have used one connection")
	assert.Equal(t, uint64(1), backend1.RequestCount(), "first backend should have been called once")

	// Close the first backend
	backend1.Interrupt()

	// Create second backend on the same address
	listener, err = lisCfg.Listen(t.Context(), "tcp", backendAddr)
	require.NoError(t, err)

	backend2, err := newTestBackend(listener, "backend-2")
	require.NoError(t, err)
	defer backend2.Close()

	// Make request to second backend
	resp2, err := httpGet(t, proxyURL)
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp2.StatusCode, "status code should be 502")
}

func TestProxyDoesNotReuseConnectionsWhenBackendChanges(t *testing.T) {
	t.Parallel()
	// Create first backend
	var lisCfg net.ListenConfig
	listener, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	backend1, err := newTestBackend(listener, "backend-1")
	require.NoError(t, err)
	defer backend1.Close()

	// Get the address of the first backend
	backendAddr := backend1.listener.Addr().String()

	backendMapping := map[string]string{
		backendAddr: backend1.id,
	}
	var backendMappingMutex sync.Mutex

	// Set up a routing function that returns the current backend
	getDestination := func(_ *http.Request) (*pool.Destination, error) {
		backendMappingMutex.Lock()
		defer backendMappingMutex.Unlock()

		backendKey, ok := backendMapping[backendAddr]
		if !ok {
			return nil, fmt.Errorf("backend not found")
		}

		return &pool.Destination{
			Url:           backend1.url,
			SandboxId:     "backend1",
			RequestLogger: logger.NewNopLogger(),
			ConnectionKey: backendKey,
		}, nil
	}

	// Create proxy with the initial routing function
	proxy, port, err := newTestProxy(t, getDestination)
	require.NoError(t, err)
	defer proxy.Close()

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/hello", port)

	// Make request to first backend
	resp1, err := httpGet(t, proxyURL)
	require.NoError(t, err)
	defer resp1.Body.Close()

	assertBackendOutput(t, backend1, resp1)

	assert.Equal(t, uint64(1), proxy.TotalPoolConnections(), "proxy should have reused the connection")
	assert.Equal(t, uint64(1), backend1.RequestCount(), "first backend should have been called once")

	// Close the first backend
	backend1.Interrupt()

	// Create second backend on the same address
	listener, err = lisCfg.Listen(t.Context(), "tcp", backendAddr)
	require.NoError(t, err)

	backend2, err := newTestBackend(listener, "backend-2")
	require.NoError(t, err)
	defer backend2.Close()

	backendMappingMutex.Lock()
	backendMapping[backend2.listener.Addr().String()] = backend2.id
	backendMappingMutex.Unlock()

	// Make request to second backend
	resp2, err := httpGet(t, proxyURL)
	require.NoError(t, err)
	defer resp2.Body.Close()

	assertBackendOutput(t, backend2, resp2)

	assert.Equal(t, uint64(1), backend2.RequestCount(), "second backend should have been called once")
	assert.Equal(t, uint64(1), backend1.RequestCount(), "first backend should have been called once")
	assert.Equal(t, uint64(2), proxy.TotalPoolConnections(), "proxy should not have reused the connection")
}

// TestProxyRetriesOnDelayedBackendStartup simulates the scenario where a backend
// server starts up after the initial connection attempt (like envd port forwarding delay).
func TestProxyRetriesOnDelayedBackendStartup(t *testing.T) {
	t.Parallel()
	var lisCfg net.ListenConfig
	tempListener, err := lisCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	backendAddr := tempListener.Addr().String()
	tempListener.Close() // Close to simulate "connection refused" - small race is acceptable

	backendURL, err := url.Parse(fmt.Sprintf("http://%s", backendAddr))
	require.NoError(t, err)

	getDestination := func(_ *http.Request) (*pool.Destination, error) {
		return &pool.Destination{
			Url:           backendURL,
			SandboxId:     "test-sandbox",
			RequestLogger: logger.NewNopLogger(),
			ConnectionKey: "delayed-backend",
		}, nil
	}

	proxy, port, err := newTestProxy(t, getDestination)
	require.NoError(t, err)
	defer proxy.Close()

	type backendResult struct {
		backend *testBackend
		err     error
	}
	backendReady := make(chan backendResult, 1)

	// Start backend after a delay (simulating envd port forwarding)
	go func() {
		// Wait 300ms before starting the backend (should succeed on retry 2 or 3)
		time.Sleep(300 * time.Millisecond)

		listener, err := lisCfg.Listen(t.Context(), "tcp", backendAddr)
		if err != nil {
			backendReady <- backendResult{nil, fmt.Errorf("failed to create delayed backend listener: %w", err)}

			return
		}

		backend, err := newTestBackend(listener, "delayed-backend")
		if err != nil {
			listener.Close()
			backendReady <- backendResult{nil, fmt.Errorf("failed to create delayed backend: %w", err)}

			return
		}

		backendReady <- backendResult{backend, nil}
	}()

	// Make request - this should retry and eventually succeed
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/hello", port)
	start := time.Now()

	resp, err := httpGet(t, proxyURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	elapsed := time.Since(start)

	// Wait for backend to be ready before checking
	result := <-backendReady
	require.NoError(t, result.err)
	backend := result.backend
	defer backend.Close()

	assertBackendOutput(t, backend, resp)

	// Verify that it took at least the delay time (proving retries happened)
	assert.GreaterOrEqual(t, elapsed, 300*time.Millisecond, "request should have waited for backend to start")
	assert.Less(t, elapsed, 2*time.Second, "request should have succeeded before all retries exhausted")

	// Verify the connection was established
	assert.Equal(t, uint64(1), backend.RequestCount(), "backend should have been called once")
	assert.Equal(t, uint64(1), proxy.TotalPoolConnections(), "proxy should have established one connection")
}

type data struct {
	Tag     string      `json:"tag"`
	Host    string      `json:"host"`
	Headers http.Header `json:"headers"`
}

// TestChangeResponseHeader creates three http servers:
// - internal server (returns "internal")
// - masked server (returns "masked")
// - proxy server (should proxy to the internal server)
// The internal and masked server both return a constant string. The proxy, when masked,
// should return the "internal" server and not "masked" server.
func TestChangeResponseHeader(t *testing.T) {
	t.Parallel()
	proxyPort := uint16(30092)
	internalPort := uint64(30090)
	maskedPort := uint16(30091)

	client := &http.Client{}

	proxyURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
	require.NoError(t, err)
	maskedHost := fmt.Sprintf("127.0.0.1:%d", maskedPort)
	internalURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", internalPort))
	require.NoError(t, err)

	// start proxy
	proxy := New(proxyPort, 1, time.Second, func(_ *http.Request) (*pool.Destination, error) {
		return &pool.Destination{
			Url:                                internalURL,
			SandboxId:                          "12345",
			SandboxPort:                        internalPort,
			DefaultToPortError:                 false,
			RequestLogger:                      logger.L(),
			ConnectionKey:                      "connection-key",
			IncludeSandboxIdInProxyErrorLogger: true,
			MaskRequestHost:                    utils.ToPtr(maskedHost),
		}, nil
	}, false)

	go func() {
		err = proxy.ListenAndServe(t.Context())
		assert.ErrorIs(t, err, http.ErrServerClosed)
	}()

	t.Cleanup(func() {
		err := proxy.Close()
		assert.NoError(t, err)
	})

	// start internal server
	internalServer := http.Server{
		Addr: fmt.Sprintf("127.0.0.1:%d", internalPort),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			err = json.NewEncoder(w).Encode(data{"internal", r.Host, r.Header})
			assert.NoError(t, err)
		}),
	}
	go func() {
		err = internalServer.ListenAndServe()
		assert.NoError(t, err)
	}()

	// start fake server
	maskedServer := http.Server{
		Addr: fmt.Sprintf("127.0.0.1:%d", maskedPort),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			err = json.NewEncoder(w).Encode(data{"masked", r.Host, r.Header})
			assert.NoError(t, err)
		}),
	}
	go func() {
		err = maskedServer.ListenAndServe()
		assert.NoError(t, err)
	}()

	// create request
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, proxyURL.String(), nil)
	req.Header.Set("Host", fmt.Sprintf("localhost:%d", proxyPort))
	req.Header.Set("e2b-testing", "test123")
	require.NoError(t, err)

	var rsp *http.Response
	for range 10 {
		rsp, err = client.Do(req)
		if err == nil {
			t.Cleanup(func() {
				err = rsp.Body.Close()
				assert.NoError(t, err)
			})

			break
		}

		if errors.Is(err, syscall.ECONNREFUSED) {
			time.Sleep(100 * time.Millisecond)

			continue
		}

		require.NoError(t, err)
	}

	require.NotNil(t, rsp, "response should not be nil")
	assert.Equal(t, 200, rsp.StatusCode)

	body, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)

	var data data
	err = json.Unmarshal(body, &data)
	require.NoError(t, err)

	assert.Equal(t, "internal", data.Tag)
	assert.Equal(t, fmt.Sprintf("127.0.0.1:%d", maskedPort), data.Host)
	assert.Equal(t, "test123", data.Headers.Get("E2b-Testing"))
	assert.Equal(t, fmt.Sprintf("127.0.0.1:%d", proxyPort), data.Headers.Get("X-Forwarded-Host"))
}
