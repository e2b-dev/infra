//go:build linux

package network

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	consulApi "github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hangWatchdog is deliberately far larger than any timeout these tests
// configure. It makes the tests hang detectors rather than stopwatches: a
// correct implementation returns in milliseconds, a regression blocks forever.
const hangWatchdog = 10 * time.Second

// blockingRequestTimeout is the per-request timeout used by tests that must
// prove the *caller's* context aborted the call. It is longer than
// hangWatchdog, so if the context were ignored the watchdog would fire first.
const blockingRequestTimeout = time.Minute

// newBlackHoleConsul starts a server that accepts requests and never answers,
// standing in for a wedged Consul agent.
func newBlackHoleConsul(t *testing.T) *httptest.Server {
	t.Helper()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))

	// Unblock the handlers before Close, which waits for outstanding requests.
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	return srv
}

// newTestStorageKV builds a StorageKV talking to addr. The client is
// constructed directly instead of through NewStorageKV so the tests need no
// environment variables and stay parallel-safe.
func newTestStorageKV(t *testing.T, addr string, requestTimeout time.Duration) *StorageKV {
	t.Helper()

	cfg := consulApi.DefaultConfig()
	cfg.Address = addr

	client, err := consulApi.NewClient(cfg)
	require.NoError(t, err)

	return &StorageKV{
		config:         Config{},
		slotsSize:      8,
		consulClient:   client,
		nodeID:         "node-test",
		egressProxy:    NoopEgressProxy{},
		requestTimeout: requestTimeout,
	}
}

// runWithin runs fn and fails the test if it does not return before the
// watchdog fires, which is what an unbounded Consul call looks like.
func runWithin(t *testing.T, what string, fn func() error) error {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- fn() }()

	select {
	case err := <-done:
		return err
	case <-time.After(hangWatchdog):
		t.Fatalf("%s did not return within %s: it is blocked on the unresponsive Consul agent", what, hangWatchdog)

		return nil
	}
}

func TestStorageKV_ReleaseHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	srv := newBlackHoleConsul(t)
	storage := newTestStorageKV(t, srv.Listener.Addr().String(), blockingRequestTimeout)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := runWithin(t, "Release", func() error {
		return storage.Release(ctx, &Slot{Key: "node-test/1", Idx: 1})
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestStorageKV_AcquireHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	srv := newBlackHoleConsul(t)
	storage := newTestStorageKV(t, srv.Listener.Addr().String(), blockingRequestTimeout)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := runWithin(t, "Acquire", func() error {
		_, acquireErr := storage.Acquire(ctx)

		return acquireErr
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestStorageKV_ReleaseBoundsRequestWithoutDeadline covers the case the
// shutdown path actually hits: a live context with no deadline of its own. The
// per-request timeout must still cut the call off.
func TestStorageKV_ReleaseBoundsRequestWithoutDeadline(t *testing.T) {
	t.Parallel()

	srv := newBlackHoleConsul(t)
	storage := newTestStorageKV(t, srv.Listener.Addr().String(), 50*time.Millisecond)

	err := runWithin(t, "Release", func() error {
		return storage.Release(t.Context(), &Slot{Key: "node-test/1", Idx: 1})
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestStorageKV_AcquireBoundsRequestWithoutDeadline(t *testing.T) {
	t.Parallel()

	srv := newBlackHoleConsul(t)
	storage := newTestStorageKV(t, srv.Listener.Addr().String(), 50*time.Millisecond)

	err := runWithin(t, "Acquire", func() error {
		_, acquireErr := storage.Acquire(t.Context())

		return acquireErr
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// fakeConsul is a minimal Consul KV endpoint: it answers the handful of
// requests StorageKV makes and records what it saw.
type fakeConsul struct {
	mu sync.Mutex
	// modifyIndex is the index reported for every existing key.
	modifyIndex uint64
	// deleted records the cas parameter of every DELETE, keyed by KV key.
	deleted map[string]string
	// casPut records the keys a CAS PUT succeeded on.
	casPut []string
}

func (f *fakeConsul) deletedCAS(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cas, ok := f.deleted[key]

	return cas, ok
}

func (f *fakeConsul) putKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.casPut...)
}

func (f *fakeConsul) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Path[len("/v1/kv/"):]

	f.mu.Lock()
	defer f.mu.Unlock()

	w.Header().Set("X-Consul-Index", "1")
	w.Header().Set("X-Consul-LastContact", "0")
	w.Header().Set("X-Consul-KnownLeader", "true")

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"Key":%q,"CreateIndex":1,"ModifyIndex":%d,"Value":null}]`, key, f.modifyIndex)
	case http.MethodDelete:
		f.deleted[key] = r.URL.Query().Get("cas")
		fmt.Fprint(w, "true")
	case http.MethodPut:
		// Slot 0 is not a valid slot index, so refuse it and let Acquire retry.
		// Ten consecutive draws of the same index are not realistic.
		if key == "node-test/0" {
			fmt.Fprint(w, "false")

			return
		}

		f.casPut = append(f.casPut, key)
		fmt.Fprint(w, "true")
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func newFakeConsul(t *testing.T, modifyIndex uint64) (*fakeConsul, *httptest.Server) {
	t.Helper()

	fake := &fakeConsul{modifyIndex: modifyIndex, deleted: make(map[string]string)}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	return fake, srv
}

// TestNewConsulConfig_SetsHTTPClientTimeout guards the backstop. Every call
// site bounds itself with a per-operation context today; the client-level
// timeout is what keeps a Consul call added later from being unbounded,
// because consulApi.DefaultConfig() sets neither Timeout nor
// ResponseHeaderTimeout.
func TestNewConsulConfig_SetsHTTPClientTimeout(t *testing.T) {
	t.Parallel()

	cfg, err := newConsulConfig("test-token")
	require.NoError(t, err)
	require.NotNil(t, cfg.HttpClient)

	assert.Equal(t, consulRequestTimeout, cfg.HttpClient.Timeout)
	assert.Equal(t, "test-token", cfg.Token)
}

// TestStorageKV_ReleaseAgainstRespondingConsul guards the normal path: the
// context plumbing must not break a release that should succeed, and the
// DeleteCAS must carry the ModifyIndex read by the preceding Get.
func TestStorageKV_ReleaseAgainstRespondingConsul(t *testing.T) {
	t.Parallel()

	fake, srv := newFakeConsul(t, 42)
	storage := newTestStorageKV(t, srv.Listener.Addr().String(), blockingRequestTimeout)

	err := runWithin(t, "Release", func() error {
		return storage.Release(t.Context(), &Slot{Key: "node-test/1", Idx: 1})
	})
	require.NoError(t, err)

	cas, ok := fake.deletedCAS("node-test/1")
	require.True(t, ok, "Release must delete the slot key from Consul KV")
	assert.Equal(t, "42", cas, "DeleteCAS must use the ModifyIndex returned by the preceding Get")
}

// TestStorageKV_AcquireAgainstRespondingConsul is the Acquire counterpart: the
// WriteOptions context must not break a CAS that should succeed.
func TestStorageKV_AcquireAgainstRespondingConsul(t *testing.T) {
	t.Parallel()

	fake, srv := newFakeConsul(t, 0)
	storage := newTestStorageKV(t, srv.Listener.Addr().String(), blockingRequestTimeout)

	var slot *Slot
	err := runWithin(t, "Acquire", func() error {
		var acquireErr error
		slot, acquireErr = storage.Acquire(t.Context())

		return acquireErr
	})
	require.NoError(t, err)
	require.NotNil(t, slot)

	assert.Contains(t, fake.putKeys(), slot.Key, "Acquire must CAS the key it hands back")
	assert.Equal(t, fmt.Sprintf("node-test/%d", slot.Idx), slot.Key)
}
