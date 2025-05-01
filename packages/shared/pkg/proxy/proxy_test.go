package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"gotest.tools/assert"

	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
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
		return nil, fmt.Errorf("failed to parse backend URL: %v", err)
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
	assert.Equal(t, resp.StatusCode, http.StatusOK, "status code should be 200")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	assert.Equal(t, string(body), backend.id, "backend id should be the same")
}

// newTestProxy creates a new proxy server for testing
func newTestProxy(getDestination func(r *http.Request) (*pool.Destination, error)) (*Proxy, uint, error) {
	// Find a free port for the proxy
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	// Set up the proxy server
	proxy, err := New(
		uint(port),
		1,
		20*time.Second, // Short idle timeout
		getDestination,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create proxy: %v", err)
	}

	// Start the proxy server
	go func() {
		_ = proxy.ListenAndServe()
	}()

	// Wait for the proxy to start
	time.Sleep(100 * time.Millisecond)

	return proxy, uint(port), nil
}

func TestProxyRoutesToTargetServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	backend, err := newTestBackend(listener, "backend-1")
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	defer backend.Close()

	// Set up a routing function that always returns the backend
	getDestination := func(r *http.Request) (*pool.Destination, error) {
		return &pool.Destination{
			Url:           backend.url,
			SandboxId:     "test-sandbox",
			Logger:        zap.NewNop(),
			ConnectionKey: backend.id,
		}, nil
	}

	proxy, port, err := newTestProxy(getDestination)
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}
	defer proxy.Close()

	assert.Equal(t, proxy.TotalPoolConnections(), uint64(0))

	// Make a request to the proxy
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/hello", port)
	resp, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("failed to GET from proxy: %v", err)
	}
	defer resp.Body.Close()

	assertBackendOutput(t, backend, resp)

	assert.Equal(t, backend.RequestCount(), uint64(1), "backend should have been called once")
	assert.Equal(t, proxy.TotalPoolConnections(), uint64(1), "proxy should have established one connection")
}

func TestProxyReusesConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	backend, err := newTestBackend(listener, "backend-1")
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	defer backend.Close()

	// Set up a routing function that always returns the backend
	getDestination := func(r *http.Request) (*pool.Destination, error) {
		return &pool.Destination{
			Url:           backend.url,
			SandboxId:     "test-sandbox",
			Logger:        zap.NewNop(),
			ConnectionKey: backend.id,
		}, nil
	}

	proxy, port, err := newTestProxy(getDestination)
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}
	defer proxy.Close()

	// Make two requests to the proxy
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/hello", port)

	// First request
	resp1, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("failed to GET from proxy (first request): %v", err)
	}
	defer resp1.Body.Close()

	assertBackendOutput(t, backend, resp1)

	// Second request
	resp2, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("failed to GET from proxy (second request): %v", err)
	}
	defer resp2.Body.Close()

	assertBackendOutput(t, backend, resp2)

	// Verify that only one connection was established
	assert.Equal(t, backend.RequestCount(), uint64(2), "backend should have been called twice")
	assert.Equal(t, proxy.TotalPoolConnections(), uint64(1), "proxy should have used one connection")
}

// This is a test that verify that the proxy reuse fails when the backend changes.
func TestProxyReuseConnectionsWhenBackendChangesFails(t *testing.T) {
	// Create first backend
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	backend1, err := newTestBackend(listener, "backend-1")
	if err != nil {
		t.Fatalf("failed to create first backend: %v", err)
	}
	defer backend1.Close()

	// Get the address of the first backend
	backendAddr := backend1.listener.Addr().String()

	backendMapping := map[string]string{
		backendAddr: backend1.id,
	}
	var backendMappingMutex sync.Mutex

	// Set up a routing function that returns the current backend
	getDestination := func(r *http.Request) (*pool.Destination, error) {
		backendMappingMutex.Lock()
		defer backendMappingMutex.Unlock()

		backendKey, ok := backendMapping[backendAddr]
		if !ok {
			return nil, fmt.Errorf("backend not found")
		}

		return &pool.Destination{
			Url:           backend1.url,
			SandboxId:     "backend1",
			Logger:        zap.NewNop(),
			ConnectionKey: backendKey,
		}, nil
	}

	// Create proxy with the initial routing function
	proxy, port, err := newTestProxy(getDestination)
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}
	defer proxy.Close()

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/hello", port)

	// Make request to first backend
	resp1, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("failed to GET from proxy (first request): %v", err)
	}
	defer resp1.Body.Close()

	assertBackendOutput(t, backend1, resp1)

	assert.Equal(t, proxy.TotalPoolConnections(), uint64(1), "proxy should have used one connection")
	assert.Equal(t, backend1.RequestCount(), uint64(1), "first backend should have been called once")

	// Close the first backend
	backend1.Interrupt()

	// Create second backend on the same address
	listener, err = net.Listen("tcp", backendAddr)
	if err != nil {
		t.Fatalf("failed to create listener for second backend: %v", err)
	}

	backend2, err := newTestBackend(listener, "backend-2")
	if err != nil {
		t.Fatalf("failed to create second backend: %v", err)
	}
	defer backend2.Close()

	// Make request to second backend
	resp2, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("unexpectedly got a response from the second backend: %v", resp2.StatusCode)
	}
	defer resp2.Body.Close()

	assert.Equal(t, resp2.StatusCode, http.StatusBadGateway, "status code should be 502")
}

func TestProxyDoesNotReuseConnectionsWhenBackendChanges(t *testing.T) {
	// Create first backend
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	backend1, err := newTestBackend(listener, "backend-1")
	if err != nil {
		t.Fatalf("failed to create first backend: %v", err)
	}
	defer backend1.Close()

	// Get the address of the first backend
	backendAddr := backend1.listener.Addr().String()

	backendMapping := map[string]string{
		backendAddr: backend1.id,
	}
	var backendMappingMutex sync.Mutex

	// Set up a routing function that returns the current backend
	getDestination := func(r *http.Request) (*pool.Destination, error) {
		backendMappingMutex.Lock()
		defer backendMappingMutex.Unlock()

		backendKey, ok := backendMapping[backendAddr]
		if !ok {
			return nil, fmt.Errorf("backend not found")
		}

		return &pool.Destination{
			Url:           backend1.url,
			SandboxId:     "backend1",
			Logger:        zap.NewNop(),
			ConnectionKey: backendKey,
		}, nil
	}

	// Create proxy with the initial routing function
	proxy, port, err := newTestProxy(getDestination)
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}
	defer proxy.Close()

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d/hello", port)

	// Make request to first backend
	resp1, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("failed to GET from proxy (first request): %v", err)
	}
	defer resp1.Body.Close()

	assertBackendOutput(t, backend1, resp1)

	assert.Equal(t, proxy.TotalPoolConnections(), uint64(1), "proxy should have reused the connection")
	assert.Equal(t, backend1.RequestCount(), uint64(1), "first backend should have been called once")

	// Close the first backend
	backend1.Interrupt()

	// Create second backend on the same address
	listener, err = net.Listen("tcp", backendAddr)
	if err != nil {
		t.Fatalf("failed to create listener for second backend: %v", err)
	}

	backend2, err := newTestBackend(listener, "backend-2")
	if err != nil {
		t.Fatalf("failed to create second backend: %v", err)
	}
	defer backend2.Close()

	backendMappingMutex.Lock()
	backendMapping[backend2.listener.Addr().String()] = backend2.id
	backendMappingMutex.Unlock()

	// Make request to second backend
	resp2, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("failed to GET from proxy (second request): %v", err)
	}
	defer resp2.Body.Close()

	assertBackendOutput(t, backend2, resp2)

	assert.Equal(t, backend2.RequestCount(), uint64(1), "second backend should have been called once")
	assert.Equal(t, backend1.RequestCount(), uint64(1), "first backend should have been called once")
	assert.Equal(t, proxy.TotalPoolConnections(), uint64(2), "proxy should not have reused the connection")
}
