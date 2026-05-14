package sandbox_test

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

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	reservations_pkg "github.com/e2b-dev/infra/packages/api/internal/sandbox/reservations"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// =============================================================================
// Test Helpers
// =============================================================================

// CallbackTracker tracks callback invocations with synchronization for async callbacks
type CallbackTracker struct {
	mu            sync.Mutex
	calls         map[string][]sandbox.Sandbox
	creationMeta  map[string][]sandbox.CreationMetadata
	expectedCalls int
	actualCalls   atomic.Int32
	done          chan struct{}
	closeOnce     sync.Once
}

func NewCallbackTracker(expectedCalls int) *CallbackTracker {
	return &CallbackTracker{
		calls:         make(map[string][]sandbox.Sandbox),
		creationMeta:  make(map[string][]sandbox.CreationMetadata),
		expectedCalls: expectedCalls,
		done:          make(chan struct{}),
	}
}

// Track returns a callback function that tracks invocations
func (ct *CallbackTracker) Track(name string) sandbox.InsertCallback {
	return func(_ context.Context, sbx sandbox.Sandbox) {
		ct.mu.Lock()
		ct.calls[name] = append(ct.calls[name], sbx)
		ct.mu.Unlock()

		if int(ct.actualCalls.Add(1)) >= ct.expectedCalls {
			ct.closeOnce.Do(func() {
				close(ct.done)
			})
		}
	}
}

// TrackCreation returns a CreationCallback that tracks invocations and per-call CreationMetadata.
func (ct *CallbackTracker) TrackCreation(name string) sandbox.CreationCallback {
	return func(_ context.Context, sbx sandbox.Sandbox, meta sandbox.CreationMetadata) {
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

func (ct *CallbackTracker) GetCreationMeta(name string) []sandbox.CreationMetadata {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return append([]sandbox.CreationMetadata{}, ct.creationMeta[name]...)
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

// AssertCalled asserts that a callback was invoked at least once
func (ct *CallbackTracker) AssertCalled(t *testing.T, name string) {
	t.Helper()
	ct.mu.Lock()
	defer ct.mu.Unlock()
	assert.NotEmpty(t, ct.calls[name], "expected callback %s to be called", name)
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

// GetCalls returns all invocations for a callback
func (ct *CallbackTracker) GetCalls(name string) []sandbox.Sandbox {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	return append([]sandbox.Sandbox{}, ct.calls[name]...)
}

// NoOpReservationStorage is a no-op implementation for testing
type NoOpReservationStorage struct{}

func (n *NoOpReservationStorage) Reserve(_ context.Context, _ uuid.UUID, _ string, _ int) (func(sandbox.Sandbox, error), func(ctx context.Context) (sandbox.Sandbox, error), error) {
	return nil, nil, nil
}

func (n *NoOpReservationStorage) Release(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

// MockStorage wraps real storage and can inject errors
type MockStorage struct {
	sandbox.Storage

	addError error
	mu       sync.Mutex
}

func NewMockStorage(storage sandbox.Storage) *MockStorage {
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
func (m *MockStorage) Add(ctx context.Context, sbx sandbox.Sandbox) error {
	m.mu.Lock()
	err := m.addError
	m.mu.Unlock()

	if err != nil {
		return err
	}

	return m.Storage.Add(ctx, sbx)
}

// createTestSandbox creates a test sandbox with default values
func createTestSandbox() sandbox.Sandbox {
	return sandbox.NewSandbox(
		"test-sandbox-"+uuid.New().String()[:8],
		"test-template",
		consts.ClientID,
		nil, // alias
		"",  // executionID
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
		uuid.New(),
		false, // autoPause
		nil,   // autoResume
		nil,   // envdAccessToken
		nil,   // allowInternetAccess
		"base-template",
		nil, // domain
		nil, // network
		nil, // trafficAccessToken
		nil, // volumes
	)
}

// =============================================================================
// Test Cases
// =============================================================================

func TestAdd_NewSandbox(t *testing.T) {
	t.Parallel()
	t.Run("success - all callbacks called", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		// Setup
		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		tracker := NewCallbackTracker(2) // Expect 2 callbacks
		callbacks := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker.Track("AddSandboxToRoutingTable"),
			AsyncNewlyCreatedSandbox: tracker.TrackCreation("AsyncNewlyCreatedSandbox"),
		}

		store := sandbox.NewStore(storage, reservations, callbacks)
		sbx := createTestSandbox()

		// Execute
		err := store.Add(ctx, sbx, &sandbox.CreationMetadata{})

		// Wait for async callbacks
		tracker.WaitForCalls(t, 2*time.Second)

		// Assert
		require.NoError(t, err)

		// Verify all callbacks called exactly once
		tracker.AssertCallCount(t, "AddSandboxToRoutingTable", 1)
		tracker.AssertCallCount(t, "AsyncNewlyCreatedSandbox", 1)

		// Verify sandbox in storage
		stored, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
		require.NoError(t, err)
		assert.Equal(t, sbx.SandboxID, stored.SandboxID)
	})
}

func TestAdd_AlreadyInCache(t *testing.T) {
	t.Parallel()
	t.Run("create path tolerates ErrAlreadyExists (sync/create race)", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		// First add succeeds with both callbacks
		tracker1 := NewCallbackTracker(2)
		callbacks1 := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker1.Track("AddSandboxToRoutingTable"),
			AsyncNewlyCreatedSandbox: tracker1.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store1 := sandbox.NewStore(storage, reservations, callbacks1)
		sbx := createTestSandbox()

		err := store1.Add(ctx, sbx, &sandbox.CreationMetadata{})
		tracker1.WaitForCalls(t, 2*time.Second)
		require.NoError(t, err)

		// Second add with creation!=nil (simulates sync winning the race before create):
		// must return nil so CreateSandbox does not kill a valid VM.
		// AddSandboxToRoutingTable is NOT called (sandbox already routed).
		// AsyncNewlyCreatedSandbox IS called because creation!=nil.
		tracker2 := NewCallbackTracker(1) // only AsyncNewlyCreatedSandbox
		callbacks2 := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker2.Track("AddSandboxToRoutingTable"),
			AsyncNewlyCreatedSandbox: tracker2.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store2 := sandbox.NewStore(storage, reservations, callbacks2)

		err = store2.Add(ctx, sbx, &sandbox.CreationMetadata{})
		require.NoError(t, err, "create path must not return ErrAlreadyExists (would kill a valid VM)")

		tracker2.WaitForCalls(t, 2*time.Second)

		tracker2.AssertNotCalled(t, "AddSandboxToRoutingTable")
		tracker2.AssertCallCount(t, "AsyncNewlyCreatedSandbox", 1)
	})
}

func TestAdd_NotNewlyCreated(t *testing.T) {
	t.Parallel()
	t.Run("not in cache - AddSandboxToRoutingTable called", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		// Add with newlyCreated=false, expect 1 callback
		tracker := NewCallbackTracker(1)
		callbacks := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker.Track("AddSandboxToRoutingTable"),
			AsyncNewlyCreatedSandbox: tracker.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store := sandbox.NewStore(storage, reservations, callbacks)
		sbx := createTestSandbox()

		err := store.Add(ctx, sbx, nil)
		tracker.WaitForCalls(t, 2*time.Second)

		require.NoError(t, err)
		tracker.AssertCallCount(t, "AddSandboxToRoutingTable", 1)
		tracker.AssertNotCalled(t, "AsyncNewlyCreatedSandbox")
	})

	t.Run("already in cache - sync re-add is tolerated", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		// First add (sync path, creation=nil)
		tracker1 := NewCallbackTracker(1)
		callbacks1 := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker1.Track("AddSandboxToRoutingTable"),
			AsyncNewlyCreatedSandbox: tracker1.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store1 := sandbox.NewStore(storage, reservations, callbacks1)
		sbx := createTestSandbox()

		err := store1.Add(ctx, sbx, nil)
		tracker1.WaitForCalls(t, 2*time.Second)
		require.NoError(t, err)

		// Second sync re-add of the same sandbox: ErrAlreadyExists is tolerated,
		// returns nil, and no routing-table callback is fired.
		tracker2 := NewCallbackTracker(0)
		callbacks2 := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker2.Track("AddSandboxToRoutingTable"),
			AsyncNewlyCreatedSandbox: tracker2.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store2 := sandbox.NewStore(storage, reservations, callbacks2)

		err = store2.Add(ctx, sbx, nil)
		require.NoError(t, err, "sync re-add of existing sandbox must not return an error")

		// Give a small delay for any async callbacks (there should be none)
		time.Sleep(100 * time.Millisecond)

		tracker2.AssertNotCalled(t, "AddSandboxToRoutingTable")
		tracker2.AssertNotCalled(t, "AsyncNewlyCreatedSandbox")
	})
}

func TestAdd_StorageErrors(t *testing.T) {
	t.Parallel()
	t.Run("storage returns non-ErrAlreadyExists error", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		mockStorage := NewMockStorage(storage)
		customErr := errors.New("storage failure")
		mockStorage.SetAddError(customErr)

		reservations := &NoOpReservationStorage{}

		// Expect 0 callbacks since error should be returned immediately
		tracker := NewCallbackTracker(1)
		callbacks := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker.Track("AddSandboxToRoutingTable"),
			AsyncNewlyCreatedSandbox: tracker.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store := sandbox.NewStore(mockStorage, reservations, callbacks)
		sbx := createTestSandbox()

		err := store.Add(ctx, sbx, &sandbox.CreationMetadata{})

		// Error should be returned
		require.Error(t, err)
		assert.Equal(t, customErr, err)

		// Give a small delay for any async callbacks (there should be none)
		time.Sleep(100 * time.Millisecond)

		// No callbacks should have been called
		tracker.AssertNotCalled(t, "AddSandboxToRoutingTable")
		tracker.AssertNotCalled(t, "AsyncNewlyCreatedSandbox")
	})
}

func TestAdd_ConcurrentCalls(t *testing.T) {
	t.Parallel()
	t.Run("concurrent adds for different sandboxes", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		numGoroutines := 100
		tracker := NewCallbackTracker(numGoroutines * 2) // Each add calls 2 callbacks

		callbacks := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker.Track("AddSandboxToRoutingTable"),
			AsyncNewlyCreatedSandbox: tracker.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store := sandbox.NewStore(storage, reservations, callbacks)

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
				err := store.Add(ctx, sbx, &sandbox.CreationMetadata{})
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
		tracker.AssertCallCount(t, "AddSandboxToRoutingTable", numGoroutines)
		tracker.AssertCallCount(t, "AsyncNewlyCreatedSandbox", numGoroutines)

		// Verify all sandboxes are in storage
		for i := range numGoroutines {
			sandboxID := fmt.Sprintf("concurrent-sandbox-%d", i)
			_, err := storage.Get(ctx, teamID, sandboxID)
			assert.NoError(t, err, "expected sandbox %s to be in storage", sandboxID)
		}
	})

	t.Run("concurrent creates for same sandbox - all succeed, one routes", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		numGoroutines := 10
		sbx := createTestSandbox()
		sbx.SandboxID = "concurrent-same-sandbox-create"

		// creation != nil: all goroutines must return nil (ErrAlreadyExists is tolerated
		// to prevent killing a valid VM on sync/create races).
		// AddSandboxToRoutingTable fires once (first insert).
		// AsyncNewlyCreatedSandbox fires for every call with creation!=nil.
		tracker := NewCallbackTracker(1 + numGoroutines) // 1 routing + N creation callbacks

		callbacks := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker.Track("AddSandboxToRoutingTable"),
			AsyncNewlyCreatedSandbox: tracker.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store := sandbox.NewStore(storage, reservations, callbacks)

		var wg sync.WaitGroup
		successCount := atomic.Int32{}

		for range numGoroutines {
			wg.Go(func() {
				err := store.Add(ctx, sbx, &sandbox.CreationMetadata{})
				if err == nil {
					successCount.Add(1)
				}
			})
		}

		wg.Wait()

		assert.Equal(t, int32(numGoroutines), successCount.Load(), "all creates must succeed (ErrAlreadyExists tolerated)")

		tracker.WaitForCalls(t, 5*time.Second)
		tracker.AssertCallCount(t, "AddSandboxToRoutingTable", 1)             // only the first insert routes
		tracker.AssertCallCount(t, "AsyncNewlyCreatedSandbox", numGoroutines) // every create call fires this

		stored, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
		require.NoError(t, err)
		assert.Equal(t, sbx.SandboxID, stored.SandboxID)
	})

	t.Run("concurrent sync re-adds for same sandbox - all succeed", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		numGoroutines := 10
		sbx := createTestSandbox()
		sbx.SandboxID = "concurrent-same-sandbox-sync"

		// creation == nil (sync/reconcile path): ErrAlreadyExists is tolerated,
		// so all goroutines must return nil.
		// Only the first successful storage.Add fires AddSandboxToRoutingTable.
		tracker := NewCallbackTracker(1) // only AddSandboxToRoutingTable from the winner

		callbacks := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker.Track("AddSandboxToRoutingTable"),
			AsyncNewlyCreatedSandbox: tracker.TrackCreation("AsyncNewlyCreatedSandbox"),
		}
		store := sandbox.NewStore(storage, reservations, callbacks)

		var wg sync.WaitGroup
		successCount := atomic.Int32{}

		for range numGoroutines {
			wg.Go(func() {
				err := store.Add(ctx, sbx, nil)
				if err == nil {
					successCount.Add(1)
				}
			})
		}

		wg.Wait()

		assert.Equal(t, int32(numGoroutines), successCount.Load(), "all sync re-adds must succeed")

		tracker.WaitForCalls(t, 5*time.Second)
		tracker.AssertCallCount(t, "AddSandboxToRoutingTable", 1) // only the first insert
		tracker.AssertNotCalled(t, "AsyncNewlyCreatedSandbox")    // never fired for sync path

		stored, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
		require.NoError(t, err)
		assert.Equal(t, sbx.SandboxID, stored.SandboxID)
	})
}

// =============================================================================
// Reservation bookkeeping tests (P1-2: non-Redis backends must not drift)
// =============================================================================

// TrackingReservationStorage records every Reserve call so tests can assert
// that the in-memory reservation index is kept in sync with the store.
type TrackingReservationStorage struct {
	mu       sync.Mutex
	reserved map[string]int // sandboxID → number of Reserve calls
}

func newTrackingReservationStorage() *TrackingReservationStorage {
	return &TrackingReservationStorage{
		reserved: make(map[string]int),
	}
}

func (tr *TrackingReservationStorage) Reserve(_ context.Context, _ uuid.UUID, sandboxID string, _ int) (func(sandbox.Sandbox, error), func(ctx context.Context) (sandbox.Sandbox, error), error) {
	tr.mu.Lock()
	tr.reserved[sandboxID]++
	tr.mu.Unlock()

	return func(_ sandbox.Sandbox, _ error) {}, nil, nil
}

func (tr *TrackingReservationStorage) Release(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

func (tr *TrackingReservationStorage) ReserveCount(sandboxID string) int {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	return tr.reserved[sandboxID]
}

// TestAdd_ReservationBookkeeping verifies that the in-memory reservation index
// is updated correctly on both the create path and the sync re-add path so that
// concurrency limits remain accurate (P1-2 regression guard).
func TestAdd_ReservationBookkeeping(t *testing.T) {
	t.Parallel()

	t.Run("create path records reservation", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := newTrackingReservationStorage()

		store := sandbox.NewStore(storage, reservations, sandbox.Callbacks{
			AddSandboxToRoutingTable: func(_ context.Context, _ sandbox.Sandbox) {},
			AsyncNewlyCreatedSandbox: func(_ context.Context, _ sandbox.Sandbox, _ sandbox.CreationMetadata) {},
		})
		sbx := createTestSandbox()

		err := store.Add(ctx, sbx, &sandbox.CreationMetadata{})
		require.NoError(t, err)

		assert.Equal(t, 1, reservations.ReserveCount(sbx.SandboxID),
			"create path must register sandbox in reservation index")
	})

	t.Run("sync re-add path records reservation even when already in storage", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := newTrackingReservationStorage()

		store := sandbox.NewStore(storage, reservations, sandbox.Callbacks{
			AddSandboxToRoutingTable: func(_ context.Context, _ sandbox.Sandbox) {},
			AsyncNewlyCreatedSandbox: func(_ context.Context, _ sandbox.Sandbox, _ sandbox.CreationMetadata) {},
		})
		sbx := createTestSandbox()

		// First add (create path)
		require.NoError(t, store.Add(ctx, sbx, &sandbox.CreationMetadata{}))
		firstCount := reservations.ReserveCount(sbx.SandboxID)

		// Second add (sync/reconcile path) — sandbox already in storage.
		// Must return nil AND call Reserve again so the index stays accurate.
		err := store.Add(ctx, sbx, nil)
		require.NoError(t, err, "sync re-add must not return an error")

		assert.Greater(t, reservations.ReserveCount(sbx.SandboxID), firstCount,
			"sync re-add must call Reserve to keep the reservation index in sync")
	})

	t.Run("repeated sync re-adds are idempotent in real ReservationStorage", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		// Use the real in-memory ReservationStorage to verify idempotency.
		storage := memory.NewStorage()
		realReservations := reservations_pkg.NewReservationStorage()

		store := sandbox.NewStore(storage, realReservations, sandbox.Callbacks{
			AddSandboxToRoutingTable: func(_ context.Context, _ sandbox.Sandbox) {},
			AsyncNewlyCreatedSandbox: func(_ context.Context, _ sandbox.Sandbox, _ sandbox.CreationMetadata) {},
		})
		sbx := createTestSandbox()
		teamID := sbx.TeamID

		// First add (create path)
		require.NoError(t, store.Add(ctx, sbx, &sandbox.CreationMetadata{}))

		// Simulate three node-sync re-adds of the same sandbox
		for range 3 {
			require.NoError(t, store.Add(ctx, sbx, nil))
		}

		// The real ReservationStorage uses a map keyed by sandboxID, so repeated
		// Reserve(limit=-1) calls for the same ID are idempotent — the sandbox
		// is counted exactly once. Verify by reserving a second sandbox with
		// limit=2: if the first is counted once, this must succeed.
		otherSbx := createTestSandbox()
		otherSbx.TeamID = teamID
		finishStart, _, err := realReservations.Reserve(ctx, teamID, otherSbx.SandboxID, 2)
		require.NoError(t, err, "team should have capacity for a second sandbox (limit=2, count=1)")
		if finishStart != nil {
			finishStart(otherSbx, nil)
		}
	})
}
