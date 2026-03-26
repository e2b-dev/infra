package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox/reservations"
	sandboxmemory "github.com/e2b-dev/infra/packages/api/internal/sandbox/storage/memory"
)

// snapshotSource is a thread-safe mutable data source that simulates the
// snapshot cache. Tests mutate it between fetcher creation and invocation to
// verify that the fetcher reads fresh data.
type snapshotSource struct {
	mu      sync.RWMutex
	version int
	envID   string
}

func (s *snapshotSource) set(version int, envID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version = version
	s.envID = envID
}

func (s *snapshotSource) get() (int, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version, s.envID
}

// makeLazyFetcher mirrors the buildResumeSandboxData pattern: it captures the
// sandboxID at creation time but reads the snapshot lazily at call time.
func makeLazyFetcher(sandboxID string, snap *snapshotSource) SandboxDataFetcher {
	return func(_ context.Context) (SandboxMetadata, error) {
		version, envID := snap.get()
		return SandboxMetadata{
			TemplateID: envID,
			Metadata: map[string]string{
				"version":    fmt.Sprintf("v%d", version),
				"sandbox_id": sandboxID,
			},
		}, nil
	}
}

func newTestStore() *sandbox.Store {
	return sandbox.NewStore(
		sandboxmemory.NewStorage(),
		reservations.NewReservationStorage(),
		sandbox.Callbacks{
			AddSandboxToRoutingTable: func(context.Context, sandbox.Sandbox) {},
			AsyncNewlyCreatedSandbox: func(context.Context, sandbox.Sandbox) {},
		},
	)
}

// TestSandboxDataFetcher_LazyExecution verifies that SandboxDataFetcher reads
// data at invocation time, not at creation time. This prevents stale snapshot
// data in the following race condition:
//
//  1. Resume handler pre-checks snapshot ownership (reads V1)
//  2. Handler creates a lazy SandboxDataFetcher (captures sandboxID, not data)
//  3. Concurrently, another resume+pause cycle creates snapshot V2
//  4. Handler enters CreateSandbox → Reserve acquires the lock
//  5. Fetcher is invoked after the lock → reads V2 from cache (not stale V1)
func TestSandboxDataFetcher_LazyExecution(t *testing.T) {
	t.Parallel()

	t.Run("fetcher reads data at call time not creation time", func(t *testing.T) {
		t.Parallel()

		snap := &snapshotSource{version: 1, envID: "template-v1"}

		// Create fetcher while snapshot is at V1.
		fetcher := makeLazyFetcher("sbx-1", snap)

		// Snapshot changes to V2 (simulating a concurrent pause that
		// created a new snapshot between fetcher creation and invocation).
		snap.set(2, "template-v2")

		// Fetcher is invoked (as CreateSandbox would, after Reserve).
		data, err := fetcher(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "v2", data.Metadata["version"],
			"lazy fetcher must return data from call time, not creation time")
		assert.Equal(t, "template-v2", data.TemplateID)
	})

	t.Run("each resume creates a fresh fetcher that sees latest data", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		snap := &snapshotSource{version: 1, envID: "template-v1"}
		store := newTestStore()
		teamID := uuid.New()
		sandboxID := "test-sequential-resumes"

		// --- Resume-1: acquires lock, fetcher sees V1 ---
		finishStart1, waitForStart1, err := store.Reserve(ctx, teamID, sandboxID, 10)
		require.NoError(t, err)
		require.NotNil(t, finishStart1)
		require.Nil(t, waitForStart1)

		data1, err := makeLazyFetcher(sandboxID, snap)(ctx)
		require.NoError(t, err)
		assert.Equal(t, "v1", data1.Metadata["version"])

		// Resume-1 completes.
		finishStart1(sandbox.Sandbox{SandboxID: sandboxID, TeamID: teamID}, nil)
		require.NoError(t, store.Release(ctx, teamID, sandboxID))

		// --- Snapshot updates to V2 (sandbox was paused again) ---
		snap.set(2, "template-v2")

		// --- Resume-2: acquires lock, fetcher sees V2 ---
		finishStart2, waitForStart2, err := store.Reserve(ctx, teamID, sandboxID, 10)
		require.NoError(t, err)
		require.NotNil(t, finishStart2)
		require.Nil(t, waitForStart2)

		data2, err := makeLazyFetcher(sandboxID, snap)(ctx)
		require.NoError(t, err)
		assert.Equal(t, "v2", data2.Metadata["version"],
			"Resume-2's fetcher must see the latest snapshot after the pause")
		assert.Equal(t, "template-v2", data2.TemplateID)

		finishStart2(sandbox.Sandbox{SandboxID: sandboxID, TeamID: teamID}, nil)
	})

	t.Run("concurrent resume while holding lock sees mid-flight snapshot change", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		snap := &snapshotSource{version: 1, envID: "template-v1"}
		store := newTestStore()
		teamID := uuid.New()
		sandboxID := "test-concurrent-resume"

		// Resume-1 acquires the reservation lock.
		finishStart1, _, err := store.Reserve(ctx, teamID, sandboxID, 10)
		require.NoError(t, err)
		require.NotNil(t, finishStart1)

		// Resume-1 creates its fetcher while snapshot is V1.
		fetcher1 := makeLazyFetcher(sandboxID, snap)

		// A concurrent pause updates the snapshot to V2 while Resume-1
		// still holds the reservation lock.
		snap.set(2, "template-v2")

		// Resume-2 tries to reserve the same sandbox and blocks.
		var resume2Data SandboxMetadata
		var resume2Err error

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, waitForStart, reserveErr := store.Reserve(ctx, teamID, sandboxID, 10)
			if reserveErr != nil {
				resume2Err = reserveErr
				return
			}
			if waitForStart != nil {
				// Resume-2 waits for Resume-1 to finish.
				_, _ = waitForStart(ctx)
			}
			// After Resume-1 completes, Resume-2 would go through the
			// resume flow again (sandbox was paused), creating a fresh
			// fetcher that reads the latest snapshot data.
			fetcher2 := makeLazyFetcher(sandboxID, snap)
			resume2Data, resume2Err = fetcher2(ctx)
		}()

		// Resume-1 calls its fetcher — even though it was created when
		// snapshot was V1, the lazy read sees V2.
		data1, err := fetcher1(ctx)
		require.NoError(t, err)
		assert.Equal(t, "v2", data1.Metadata["version"],
			"Resume-1's fetcher should see V2 because it reads lazily")

		// Resume-1 finishes, unblocking Resume-2.
		finishStart1(sandbox.Sandbox{SandboxID: sandboxID, TeamID: teamID}, nil)
		wg.Wait()

		require.NoError(t, resume2Err)
		assert.Equal(t, "v2", resume2Data.Metadata["version"],
			"Resume-2's fetcher must see V2 after the concurrent pause")
	})
}
