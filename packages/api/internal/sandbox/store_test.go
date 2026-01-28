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
	expectedCalls int
	actualCalls   atomic.Int32
	done          chan struct{}
	closeOnce     sync.Once
}

func NewCallbackTracker(expectedCalls int) *CallbackTracker {
	return &CallbackTracker{
		calls:         make(map[string][]sandbox.Sandbox),
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
		nil,   // envdAccessToken
		nil,   // allowInternetAccess
		"base-template",
		nil, // domain
		nil, // network
		nil, // trafficAccessToken
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

		tracker := NewCallbackTracker(3) // Expect 3 callbacks
		callbacks := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker.Track("AddSandboxToRoutingTable"),
			AsyncSandboxCounter:      tracker.Track("AsyncSandboxCounter"),
			AsyncNewlyCreatedSandbox: tracker.Track("AsyncNewlyCreatedSandbox"),
		}

		store := sandbox.NewStore(storage, reservations, callbacks)
		sbx := createTestSandbox()

		// Execute
		err := store.Add(ctx, sbx, true)

		// Wait for async callbacks
		tracker.WaitForCalls(t, 2*time.Second)

		// Assert
		require.NoError(t, err)

		// Verify all callbacks called exactly once
		tracker.AssertCallCount(t, "AddSandboxToRoutingTable", 1)
		tracker.AssertCallCount(t, "AsyncSandboxCounter", 1)
		tracker.AssertCallCount(t, "AsyncNewlyCreatedSandbox", 1)

		// Verify sandbox in storage
		stored, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
		require.NoError(t, err)
		assert.Equal(t, sbx.SandboxID, stored.SandboxID)
	})
}

func TestAdd_AlreadyInCache(t *testing.T) {
	t.Parallel()
	t.Run("newlyCreated=true - AsyncSandboxCounter NOT called when already in cache", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		// First add with all 3 callbacks
		tracker1 := NewCallbackTracker(3)
		callbacks1 := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker1.Track("AddSandboxToRoutingTable"),
			AsyncSandboxCounter:      tracker1.Track("AsyncSandboxCounter"),
			AsyncNewlyCreatedSandbox: tracker1.Track("AsyncNewlyCreatedSandbox"),
		}
		store1 := sandbox.NewStore(storage, reservations, callbacks1)
		sbx := createTestSandbox()

		err := store1.Add(ctx, sbx, true)
		tracker1.WaitForCalls(t, 2*time.Second)
		require.NoError(t, err)

		// Second add with newlyCreated=true, only 2 callbacks
		// (AsyncSandboxCounter is NOT called because already in cache)
		tracker2 := NewCallbackTracker(1)
		callbacks2 := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker2.Track("AddSandboxToRoutingTable"),
			AsyncSandboxCounter:      tracker2.Track("AsyncSandboxCounter"),
			AsyncNewlyCreatedSandbox: tracker2.Track("AsyncNewlyCreatedSandbox"),
		}
		store2 := sandbox.NewStore(storage, reservations, callbacks2)

		err = store2.Add(ctx, sbx, true)
		tracker2.WaitForCalls(t, 2*time.Second)

		require.NoError(t, err)
		tracker2.AssertNotCalled(t, "AddSandboxToRoutingTable")
		tracker2.AssertNotCalled(t, "AsyncSandboxCounter") // NOT called when already in cache!
		tracker2.AssertCallCount(t, "AsyncNewlyCreatedSandbox", 1)
	})

	t.Run("newlyCreated=false - only AddSandboxToRoutingTable called when already in cache", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		// First add with newlyCreated=true
		tracker1 := NewCallbackTracker(3)
		callbacks1 := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker1.Track("AddSandboxToRoutingTable"),
			AsyncSandboxCounter:      tracker1.Track("AsyncSandboxCounter"),
			AsyncNewlyCreatedSandbox: tracker1.Track("AsyncNewlyCreatedSandbox"),
		}
		store1 := sandbox.NewStore(storage, reservations, callbacks1)
		sbx := createTestSandbox()

		err := store1.Add(ctx, sbx, true)
		tracker1.WaitForCalls(t, 2*time.Second)
		require.NoError(t, err)

		// Second add with newlyCreated=false, no callbacks expected
		// (No callbacks called because already in cache)
		tracker2 := NewCallbackTracker(0)
		callbacks2 := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker2.Track("AddSandboxToRoutingTable"),
			AsyncSandboxCounter:      tracker2.Track("AsyncSandboxCounter"),
			AsyncNewlyCreatedSandbox: tracker2.Track("AsyncNewlyCreatedSandbox"),
		}
		store2 := sandbox.NewStore(storage, reservations, callbacks2)

		err = store2.Add(ctx, sbx, false)
		require.NoError(t, err)

		// Give a small delay for any async callbacks (there should be none)
		time.Sleep(100 * time.Millisecond)

		tracker2.AssertNotCalled(t, "AddSandboxToRoutingTable")
		tracker2.AssertNotCalled(t, "AsyncSandboxCounter") // NOT called when already in cache
		tracker2.AssertNotCalled(t, "AsyncNewlyCreatedSandbox")
	})
}

func TestAdd_NotNewlyCreated(t *testing.T) {
	t.Parallel()
	t.Run("not in cache - AddSandboxToRoutingTable and AsyncSandboxCounter called", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		// Add with newlyCreated=false, expect 2 callbacks
		tracker := NewCallbackTracker(2)
		callbacks := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker.Track("AddSandboxToRoutingTable"),
			AsyncSandboxCounter:      tracker.Track("AsyncSandboxCounter"),
			AsyncNewlyCreatedSandbox: tracker.Track("AsyncNewlyCreatedSandbox"),
		}
		store := sandbox.NewStore(storage, reservations, callbacks)
		sbx := createTestSandbox()

		err := store.Add(ctx, sbx, false)
		tracker.WaitForCalls(t, 2*time.Second)

		require.NoError(t, err)
		tracker.AssertCallCount(t, "AddSandboxToRoutingTable", 1)
		tracker.AssertCallCount(t, "AsyncSandboxCounter", 1)
		tracker.AssertNotCalled(t, "AsyncNewlyCreatedSandbox")
	})

	t.Run("already in cache - only AddSandboxToRoutingTable called", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		// First add
		tracker1 := NewCallbackTracker(2)
		callbacks1 := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker1.Track("AddSandboxToRoutingTable"),
			AsyncSandboxCounter:      tracker1.Track("AsyncSandboxCounter"),
			AsyncNewlyCreatedSandbox: tracker1.Track("AsyncNewlyCreatedSandbox"),
		}
		store1 := sandbox.NewStore(storage, reservations, callbacks1)
		sbx := createTestSandbox()

		err := store1.Add(ctx, sbx, false)
		tracker1.WaitForCalls(t, 2*time.Second)
		require.NoError(t, err)

		// Second add with same sandbox, newlyCreated=false, no callbacks expected
		tracker2 := NewCallbackTracker(0)
		callbacks2 := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker2.Track("AddSandboxToRoutingTable"),
			AsyncSandboxCounter:      tracker2.Track("AsyncSandboxCounter"),
			AsyncNewlyCreatedSandbox: tracker2.Track("AsyncNewlyCreatedSandbox"),
		}
		store2 := sandbox.NewStore(storage, reservations, callbacks2)

		err = store2.Add(ctx, sbx, false)
		require.NoError(t, err)

		// Give a small delay for any async callbacks (there should be none)
		time.Sleep(100 * time.Millisecond)

		tracker2.AssertNotCalled(t, "AddSandboxToRoutingTable")
		tracker2.AssertNotCalled(t, "AsyncSandboxCounter") // NOT called when already in cache
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
			AsyncSandboxCounter:      tracker.Track("AsyncSandboxCounter"),
			AsyncNewlyCreatedSandbox: tracker.Track("AsyncNewlyCreatedSandbox"),
		}
		store := sandbox.NewStore(mockStorage, reservations, callbacks)
		sbx := createTestSandbox()

		err := store.Add(ctx, sbx, true)

		// Error should be returned
		require.Error(t, err)
		assert.Equal(t, customErr, err)

		// Give a small delay for any async callbacks (there should be none)
		time.Sleep(100 * time.Millisecond)

		// No callbacks should have been called
		tracker.AssertNotCalled(t, "AddSandboxToRoutingTable")
		tracker.AssertNotCalled(t, "AsyncSandboxCounter")
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
		tracker := NewCallbackTracker(numGoroutines * 3) // Each add calls 3 callbacks

		callbacks := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker.Track("AddSandboxToRoutingTable"),
			AsyncSandboxCounter:      tracker.Track("AsyncSandboxCounter"),
			AsyncNewlyCreatedSandbox: tracker.Track("AsyncNewlyCreatedSandbox"),
		}
		store := sandbox.NewStore(storage, reservations, callbacks)

		var wg sync.WaitGroup
		errorsChan := make(chan error, numGoroutines)

		// Launch concurrent adds
		for i := range numGoroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				sbx := createTestSandbox()
				sbx.SandboxID = fmt.Sprintf("concurrent-sandbox-%d", id)
				err := store.Add(ctx, sbx, true)
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
		tracker.AssertCallCount(t, "AsyncSandboxCounter", numGoroutines)
		tracker.AssertCallCount(t, "AsyncNewlyCreatedSandbox", numGoroutines)

		// Verify all sandboxes are in storage
		for i := range numGoroutines {
			sandboxID := fmt.Sprintf("concurrent-sandbox-%d", i)
			_, err := storage.Get(ctx, uuid.UUID{}, sandboxID)
			assert.NoError(t, err, "expected sandbox %s to be in storage", sandboxID)
		}
	})

	t.Run("concurrent adds for same sandbox", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		storage := memory.NewStorage()
		reservations := &NoOpReservationStorage{}

		numGoroutines := 10
		sbx := createTestSandbox()
		sbx.SandboxID = "concurrent-same-sandbox"

		// One will succeed with all 3 callbacks, rest will get ErrAlreadyExists with only AsyncNewlyCreatedSandbox callback
		// Total: 3 + 9 = 12 callbacks (AddSandboxToRoutingTable: 1, AsyncSandboxCounter: 1, AsyncNewlyCreatedSandbox: 10)
		tracker := NewCallbackTracker(2 + numGoroutines)

		callbacks := sandbox.Callbacks{
			AddSandboxToRoutingTable: tracker.Track("AddSandboxToRoutingTable"),
			AsyncSandboxCounter:      tracker.Track("AsyncSandboxCounter"),
			AsyncNewlyCreatedSandbox: tracker.Track("AsyncNewlyCreatedSandbox"),
		}
		store := sandbox.NewStore(storage, reservations, callbacks)

		var wg sync.WaitGroup
		successCount := atomic.Int32{}

		// Launch concurrent adds for the same sandbox
		for range numGoroutines {
			wg.Go(func() {
				err := store.Add(ctx, sbx, true)
				if err == nil {
					successCount.Add(1)
				}
			})
		}

		wg.Wait()

		// All should succeed (Add returns nil even for ErrAlreadyExists)
		assert.Equal(t, int32(numGoroutines), successCount.Load())

		// Wait for all callbacks
		tracker.WaitForCalls(t, 5*time.Second)

		// Verify callbacks
		tracker.AssertCallCount(t, "AddSandboxToRoutingTable", 1)             // Only called once (first successful add)
		tracker.AssertCallCount(t, "AsyncSandboxCounter", 1)                  // Only called once (first successful add)
		tracker.AssertCallCount(t, "AsyncNewlyCreatedSandbox", numGoroutines) // All calls have newlyCreated=true

		// Verify sandbox exists in storage
		stored, err := storage.Get(ctx, sbx.TeamID, sbx.SandboxID)
		require.NoError(t, err)
		assert.Equal(t, sbx.SandboxID, stored.SandboxID)
	})
}
