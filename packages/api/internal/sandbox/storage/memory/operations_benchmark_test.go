package memory

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
)

type benchFixture struct {
	storage     *Storage
	teamIDs     []uuid.UUID
	runningTeam uuid.UUID
	syncNodeID  string
	syncInput   []sandboxtypes.Sandbox
}

func buildFixture(total int) benchFixture {
	now := time.Now()
	s := NewStorage()

	const teamCount = 128
	teamIDs := make([]uuid.UUID, teamCount)
	for i := range teamCount {
		teamIDs[i] = uuid.New()
	}

	const nodeCount = 16
	nodeIDs := make([]string, nodeCount)
	for i := range nodeCount {
		nodeIDs[i] = fmt.Sprintf("node-%02d", i)
	}

	runningTeam := teamIDs[0]
	syncNodeID := nodeIDs[0]
	syncInput := make([]sandboxtypes.Sandbox, 0, total/nodeCount+1)

	for i := range total {
		teamID := teamIDs[i%teamCount]
		nodeID := nodeIDs[i%nodeCount]

		state := sandboxtypes.StateRunning
		if i%5 == 0 {
			state = sandboxtypes.StatePausing
		}

		endTime := now.Add(1 * time.Hour)
		// Keep a stable 5% expired-running subset for ExpiredItems benchmarks.
		if i%20 == 0 {
			state = sandboxtypes.StateRunning
			endTime = now.Add(-1 * time.Minute)
		}

		sbx := sandboxtypes.Sandbox{
			SandboxID: fmt.Sprintf("sbx-%06d", i),
			TeamID:    teamID,
			NodeID:    nodeID,
			State:     state,
			StartTime: now.Add(-1 * time.Hour),
			EndTime:   endTime,
		}

		s.items.Set(sbx.SandboxID, newMemorySandbox(sbx))

		// Feed Sync with full coverage for one node to avoid mutations during bench.
		if nodeID == syncNodeID {
			syncInput = append(syncInput, sbx)
		}
	}

	return benchFixture{
		storage:     s,
		teamIDs:     teamIDs,
		runningTeam: runningTeam,
		syncNodeID:  syncNodeID,
		syncInput:   syncInput,
	}
}

func benchmarkSizes(b *testing.B, fn func(b *testing.B, f benchFixture)) {
	b.Helper()

	for _, size := range []int{5000, 10000, 25000, 50000} {
		b.Run(fmt.Sprintf("items=%d", size), func(b *testing.B) {
			fixture := buildFixture(size)
			b.ReportAllocs()
			b.ResetTimer()
			fn(b, fixture)
		})
	}
}

func BenchmarkStorageGetItemsRunningByTeam(b *testing.B) {
	benchmarkSizes(b, func(b *testing.B, f benchFixture) {
		b.Helper()

		for range b.N {
			_ = f.storage.getItems(&f.runningTeam, []sandboxtypes.State{sandboxtypes.StateRunning})
		}
	})
}

func BenchmarkStorageExpiredItems(b *testing.B) {
	ctx := b.Context()
	benchmarkSizes(b, func(b *testing.B, f benchFixture) {
		b.Helper()

		for range b.N {
			_, _ = f.storage.ExpiredItems(ctx)
		}
	})
}

func BenchmarkStorageTeamsWithSandboxCount(b *testing.B) {
	ctx := b.Context()
	benchmarkSizes(b, func(b *testing.B, f benchFixture) {
		b.Helper()

		for range b.N {
			_, _ = f.storage.TeamsWithSandboxCount(ctx)
		}
	})
}

func BenchmarkStorageSyncRemoveScan(b *testing.B) {
	benchmarkSizes(b, func(b *testing.B, f benchFixture) {
		b.Helper()

		for range b.N {
			_ = f.storage.Reconcile(b.Context(), f.syncInput, f.syncNodeID)
		}
	})
}
