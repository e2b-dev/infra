package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	sandboxredis "github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/redis"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

// newTestStorage spins up a redis testcontainer and returns a fresh redis-backed
// sandbox.Storage. The container and storage are cleaned up via t.Cleanup.
func newTestStorage(t *testing.T) (Storage, e2bcatalog.SandboxesCatalog) {
	t.Helper()

	client := redis_utils.SetupInstance(t)
	storage, err := sandboxredis.NewStorage(client, noop.NewMeterProvider(), nil)
	require.NoError(t, err)
	go storage.Start(t.Context())
	t.Cleanup(func() { storage.Close(context.WithoutCancel(t.Context())) })

	return storage, e2bcatalog.NewRedisSandboxCatalog(client)
}

// =============================================================================
// Test Helpers
// =============================================================================

// CallbackTracker tracks callback invocations with synchronization for async callbacks
type CallbackTracker struct {
	mu            sync.Mutex
	calls         map[string][]Sandbox
	creationMeta  map[string][]CreationMetadata
	expectedCalls int
	actualCalls   atomic.Int32
	done          chan struct{}
	closeOnce     sync.Once
}

func NewCallbackTracker(expectedCalls int) *CallbackTracker {
	return &CallbackTracker{
		calls:         make(map[string][]Sandbox),
		creationMeta:  make(map[string][]CreationMetadata),
		expectedCalls: expectedCalls,
		done:          make(chan struct{}),
	}
}

// TrackCreation returns a CreationCallback that tracks invocations and per-call CreationMetadata.
func (ct *CallbackTracker) TrackCreation(name string) CreationCallback {
	return func(_ context.Context, sbx Sandbox, meta CreationMetadata) {
		ct.mu.Lock()
		ct.calls[name] = append(ct.calls[name], sbx)
		ct.creationMeta[name] = append(ct.creationMeta[name], meta)
		ct.mu.Unlock()

		if int(ct.actualCalls.Add(1)) >= ct.expectedCalls {
			ct.closeOnce.Do(func() {
				close(ct.done)
			})
		}
	}
}

func (ct *CallbackTracker) GetCreationMeta(name string) []CreationMetadata {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return append([]CreationMetadata{}, ct.creationMeta[name]...)
}

// WaitForCalls blocks until expected number of callbacks received or timeout
func (ct *CallbackTracker) WaitForCalls(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-ct.done:
		return
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for callbacks: expected %d, got %d", ct.expectedCalls, ct.actualCalls.Load())
	}
}

// AssertNotCalled asserts that a callback was never invoked
func (ct *CallbackTracker) AssertNotCalled(t *testing.T, name string) {
	t.Helper()
	ct.mu.Lock()
	defer ct.mu.Unlock()
	assert.Empty(t, ct.calls[name], "expected callback %s not to be called", name)
}

// AssertCallCount asserts exact number of invocations
func (ct *CallbackTracker) AssertCallCount(t *testing.T, name string, count int) {
	t.Helper()
	ct.mu.Lock()
	defer ct.mu.Unlock()
	assert.Len(t, ct.calls[name], count, "expected callback %s to be called %d times", name, count)
}

// NoOpReservationStorage is a no-op implementation for testing
type NoOpReservationStorage struct{}

func (n *NoOpReservationStorage) Reserve(_ context.Context, _ uuid.UUID, _ string, _ int) (func(Sandbox, error), func(ctx context.Context) (Sandbox, error), error) {
	return nil, nil, nil
}

func (n *NoOpReservationStorage) Release(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

// MockStorage wraps real storage and can inject errors
type MockStorage struct {
	Storage

	addError error
	mu       sync.Mutex
}

type deleteTrackingCatalog struct {
	e2bcatalog.SandboxesCatalog

	deleteCalls atomic.Int32
}

func (c *deleteTrackingCatalog) DeleteSandbox(ctx context.Context, sandboxID, executionID string) error {
	c.deleteCalls.Add(1)

	return c.SandboxesCatalog.DeleteSandbox(ctx, sandboxID, executionID)
}

func NewMockStorage(storage Storage) *MockStorage {
	return &MockStorage{
		Storage: storage,
	}
}

// SetAddError configures Add() to return an error
func (m *MockStorage) SetAddError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addError = err
}

// Add wraps Storage.Add() with error injection
func (m *MockStorage) Add(ctx context.Context, sbx Sandbox, routing *RoutingMetadata) error {
	m.mu.Lock()
	err := m.addError
	m.mu.Unlock()

	if err != nil {
		return err
	}

	return m.Storage.Add(ctx, sbx, routing)
}

// createTestSandbox creates a test sandbox with default values
func createTestSandbox() Sandbox {
	return NewSandbox(
		"test-sandbox-"+uuid.New().String()[:8],
		"test-template",
		consts.ClientID,
		nil, // alias
		uuid.NewString(),
		uuid.New(),
		uuid.New(),
		map[string]string{"test": "metadata"},
		time.Hour,                 // maxInstanceLength
		time.Now(),                // startTime
		time.Now().Add(time.Hour), // endTime
		2,                         // vcpu
		1024,                      // diskMB
		512,                       // ramMB
		"5.10",                    // kernel
		"1.0",                     // firecracker
		"1.0",                     // envd
		"node-1",
		consts.LocalClusterID,
		false, // autoPause
		false, // autoPauseFilesystemOnly
		nil,   // autoResume
		nil,   // envdAccessToken
		nil,   // allowInternetAccess
		"base-template",
		nil, // domain
		nil, // network
		nil, // trafficAccessToken
		nil, // volumes
		nil, // iam tokens
	)
}

func createTestRouting() *RoutingMetadata {
	return &RoutingMetadata{
		OrchestratorID: "orchestrator-1",
		OrchestratorIP: "10.0.0.1",
	}
}

// =============================================================================
// Test Cases
// =============================================================================

func TestAdd_NewSandbox(t *testing.T) {
	t.Parallel()
	t.Run("success - route stored and callback called", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		// Setup
		storage, routingCatalog := newTestStorage(t)
		reservations := &NoOpReservationStorage{}

		tracker := NewCallbackTracker(1)
		callbacks := Callbacks{
			AsyncNewlyCreatedSandbox: tracker.TrackCreation("AsyncNewlyCreatedSandbox"),
		}

		store := NewStore(storage, reservations, routingCatalog, callbacks)
		sbx := createTestSandbox()
		routing := createTestRouting()

		// Execute
		err := store.Add(ctx, sbx, routing, &CreationMetadata{})

		// Wait for async callbacks
		tracker.WaitForCalls(t, time.Second)

		// Assert
		require.NoError(t, err)

		tracker.AssertCallCount(t, "AsyncNewlyCreatedSandbox", 1)

		// Verify sandbox in storage
		stored, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
		require.NoError(t, err)
		assert.Equal(t, sbx.SandboxID, stored.SandboxID)

		route, err := routingCatalog.GetSandbox(ctx, sbx.SandboxID)
		require.NoError(t, err)
		assert.Equal(t, routing.OrchestratorID, route.OrchestratorID)
		assert.Equal(t, routing.OrchestratorIP, route.OrchestratorIP)
		assert.Equal(t, sbx.ExecutionID, route.ExecutionID)
		assert.WithinDuration(t, sbx.StartTime, route.StartedAt, time.Millisecond)
		assert.Equal(t, int64(1), route.MaxLengthInHours)
	})
}

func TestAdd_NotNewlyCreated(t *testing.T) {
	t.Parallel()
	t.Run("route stored without creation callback", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage, routingCatalog := newTestStorage(t)
		reservations := &NoOpReservationStorage{}

		tracker := NewCallbackTracker(1)
		callbacks := Callbacks{
			AsyncNewlyCreatedSandbox: tracker.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store := NewStore(storage, reservations, routingCatalog, callbacks)
		sbx := createTestSandbox()

		err := store.Add(ctx, sbx, createTestRouting(), nil)

		require.NoError(t, err)
		tracker.AssertNotCalled(t, "AsyncNewlyCreatedSandbox")
		_, err = routingCatalog.GetSandbox(ctx, sbx.SandboxID)
		require.NoError(t, err)
	})
}

func TestAdd_StorageErrors(t *testing.T) {
	t.Parallel()
	t.Run("storage returns non-ErrAlreadyExists error", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage, routingCatalog := newTestStorage(t)
		mockStorage := NewMockStorage(storage)
		customErr := errors.New("storage failure")
		mockStorage.SetAddError(customErr)

		reservations := &NoOpReservationStorage{}

		tracker := NewCallbackTracker(1)
		callbacks := Callbacks{
			AsyncNewlyCreatedSandbox: tracker.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store := NewStore(mockStorage, reservations, routingCatalog, callbacks)
		sbx := createTestSandbox()

		err := store.Add(ctx, sbx, createTestRouting(), &CreationMetadata{})

		// Error should be returned
		require.Error(t, err)
		assert.Equal(t, customErr, err)

		// Give a small delay for any async callbacks (there should be none)
		time.Sleep(100 * time.Millisecond)

		// No callbacks should have been called
		tracker.AssertNotCalled(t, "AsyncNewlyCreatedSandbox")
		_, routeErr := routingCatalog.GetSandbox(ctx, sbx.SandboxID)
		require.ErrorIs(t, routeErr, e2bcatalog.ErrSandboxNotFound)
	})
}

func TestAdd_ConcurrentCalls(t *testing.T) {
	t.Parallel()
	t.Run("concurrent adds for different sandboxes", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage, routingCatalog := newTestStorage(t)
		reservations := &NoOpReservationStorage{}

		numGoroutines := 100
		tracker := NewCallbackTracker(numGoroutines)

		callbacks := Callbacks{
			AsyncNewlyCreatedSandbox: tracker.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store := NewStore(storage, reservations, routingCatalog, callbacks)

		teamID := uuid.New()
		var wg sync.WaitGroup
		errorsChan := make(chan error, numGoroutines)

		// Launch concurrent adds
		for i := range numGoroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sbx := createTestSandbox()
				sbx.SandboxID = fmt.Sprintf("concurrent-sandbox-%d", id)
				sbx.TeamID = teamID
				err := store.Add(ctx, sbx, createTestRouting(), &CreationMetadata{})
				if err != nil {
					errorsChan <- err
				}
			}(i)
		}

		wg.Wait()
		close(errorsChan)

		// Check for errors
		var errs []error
		for err := range errorsChan {
			errs = append(errs, err)
		}
		assert.Empty(t, errs, "expected no errors from concurrent adds")

		// Wait for all callbacks
		tracker.WaitForCalls(t, 5*time.Second)

		// Verify all callbacks were called expected number of times
		tracker.AssertCallCount(t, "AsyncNewlyCreatedSandbox", numGoroutines)

		// Verify all sandboxes are in storage
		for i := range numGoroutines {
			sandboxID := fmt.Sprintf("concurrent-sandbox-%d", i)
			_, err := storage.Get(ctx, teamID, sandboxID)
			require.NoError(t, err, "expected sandbox %s to be in storage", sandboxID)
			_, err = routingCatalog.GetSandbox(ctx, sandboxID)
			require.NoError(t, err, "expected sandbox %s to be routable", sandboxID)
		}
	})
}

func TestStartRemoving_Routing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		action        StateAction
		catalogRecord bool
		clusterID     uuid.UUID
		wantDeletes   int32
	}{
		{"terminal transition deletes route", StateActionPause, true, consts.LocalClusterID, 1},
		{"transient transition retains route", StateActionSnapshot, true, consts.LocalClusterID, 0},
		{"remote transition leaves routing to grpc metadata", StateActionPause, false, uuid.New(), 0},
		{"legacy local transition deletes route", StateActionPause, false, consts.LocalClusterID, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			storage, routingCatalog := newTestStorage(t)
			trackingCatalog := &deleteTrackingCatalog{SandboxesCatalog: routingCatalog}
			store := NewStore(storage, &NoOpReservationStorage{}, trackingCatalog, Callbacks{})
			sbx := createTestSandbox()
			sbx.ClusterID = tt.clusterID
			var routing *RoutingMetadata
			if tt.catalogRecord {
				routing = createTestRouting()
			}
			require.NoError(t, store.Add(t.Context(), sbx, routing, nil))

			_, alreadyDone, finish, err := store.StartRemoving(t.Context(), sbx.TeamID, sbx.SandboxID, RemoveOpts{Action: tt.action})
			require.NoError(t, err)
			require.False(t, alreadyDone)
			finish(t.Context(), nil)

			require.Equal(t, tt.wantDeletes, trackingCatalog.deleteCalls.Load())
		})
	}
}
