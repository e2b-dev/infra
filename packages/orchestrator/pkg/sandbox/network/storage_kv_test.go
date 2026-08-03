//go:build linux

package network

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	consulApi "github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWedgedConsul returns a StorageKV backed by a fake Consul agent that
// accepts requests and never answers. The channel closes on the first request.
func newWedgedConsul(t *testing.T, releaseTimeout time.Duration) (*StorageKV, <-chan struct{}) {
	t.Helper()

	gotRequest := make(chan struct{})
	unblock := make(chan struct{})

	var once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(gotRequest) })

		select {
		case <-unblock:
		case <-r.Context().Done():
		}
	}))

	// Let every in-flight handler go before Close, which waits on them.
	t.Cleanup(func() {
		close(unblock)
		srv.Close()
	})

	config, err := newConsulConfig("test-token")
	require.NoError(t, err)

	config.Address = srv.Listener.Addr().String()

	client, err := consulApi.NewClient(config)
	require.NoError(t, err)

	return &StorageKV{
		slotsSize:      4,
		consulClient:   client,
		nodeID:         "test-node",
		releaseTimeout: releaseTimeout,
	}, gotRequest
}

func TestNewConsulConfig_SetsHTTPRequestTimeout(t *testing.T) {
	t.Parallel()

	config, err := newConsulConfig("test-token")
	require.NoError(t, err)
	require.NotNil(t, config.HttpClient)

	assert.Positive(t, config.HttpClient.Timeout, "Consul HTTP client must have an overall request timeout")
	assert.Equal(t, consulRequestTimeout, config.HttpClient.Timeout)
	assert.Equal(t, "test-token", config.Token)
}

func TestStorageKV_ReleaseIsBounded(t *testing.T) {
	t.Parallel()

	const releaseTimeout = 100 * time.Millisecond

	storage, _ := newWedgedConsul(t, releaseTimeout)

	started := time.Now()

	errCh := make(chan error, 1)
	go func() { errCh <- storage.Release(&Slot{Key: "test-node/1", Idx: 1}) }()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.WithinDuration(t, started.Add(releaseTimeout), time.Now(), time.Second,
			"Release should give up at about its own deadline")
	case <-time.After(5 * time.Second):
		t.Fatal("Release stayed blocked on the wedged agent instead of hitting its own deadline")
	}
}

func TestStorageKV_AcquireHonoursContextCancellation(t *testing.T) {
	t.Parallel()

	storage, gotRequest := newWedgedConsul(t, consulReleaseTimeout)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := storage.Acquire(ctx)
		errCh <- err
	}()

	<-gotRequest
	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire ignored its context and stayed blocked on the wedged agent")
	}
}
