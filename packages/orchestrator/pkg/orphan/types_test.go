//go:build linux

package orphan_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/orphan"
)

func TestSweepResult_IsClean_EmptyResult(t *testing.T) {
	t.Parallel()

	r := &orphan.SweepResult{}
	assert.True(t, r.IsClean(), "empty result should be clean")
}

func TestSweepResult_IsClean_WithOrphanedProcess(t *testing.T) {
	t.Parallel()

	r := &orphan.SweepResult{
		OrphanedProcesses: []orphan.OrphanedProcess{
			{PID: 1234, PPID: 1, SocketPath: "/tmp/fc-abc.sock", DetectedAt: time.Now()},
		},
	}
	assert.False(t, r.IsClean(), "result with orphaned processes should not be clean")
}

func TestSweepResult_IsClean_WithOrphanedSocket(t *testing.T) {
	t.Parallel()

	r := &orphan.SweepResult{
		OrphanedSockets: []orphan.OrphanedSocket{
			{Path: "/tmp/fc-abc-def.sock", DetectedAt: time.Now()},
		},
	}
	assert.False(t, r.IsClean())
}

func TestSweepResult_IsClean_WithOrphanedFIFO(t *testing.T) {
	t.Parallel()

	r := &orphan.SweepResult{
		OrphanedFIFOs: []orphan.OrphanedFIFO{
			{Path: "/tmp/fc-metrics-abc-def.fifo", DetectedAt: time.Now()},
		},
	}
	assert.False(t, r.IsClean())
}

func TestSweepResult_IsClean_WithOrphanedVeth(t *testing.T) {
	t.Parallel()

	r := &orphan.SweepResult{
		OrphanedVeths: []orphan.OrphanedVeth{
			{Name: "veth-42", SlotIdx: 42, DetectedAt: time.Now()},
		},
	}
	assert.False(t, r.IsClean())
}

func TestSweepResult_Total_Empty(t *testing.T) {
	t.Parallel()

	r := &orphan.SweepResult{}
	assert.Equal(t, 0, r.Total())
}

func TestSweepResult_Total_Mixed(t *testing.T) {
	t.Parallel()

	now := time.Now()
	r := &orphan.SweepResult{
		OrphanedProcesses: []orphan.OrphanedProcess{
			{PID: 1, PPID: 1, DetectedAt: now},
			{PID: 2, PPID: 1, DetectedAt: now},
		},
		OrphanedSockets: []orphan.OrphanedSocket{
			{Path: "/tmp/fc-a.sock", DetectedAt: now},
		},
		OrphanedFIFOs: []orphan.OrphanedFIFO{
			{Path: "/tmp/fc-metrics-a.fifo", DetectedAt: now},
			{Path: "/tmp/fc-metrics-b.fifo", DetectedAt: now},
			{Path: "/tmp/fc-metrics-c.fifo", DetectedAt: now},
		},
		OrphanedVeths: []orphan.OrphanedVeth{
			{Name: "veth-1", SlotIdx: 1, DetectedAt: now},
		},
	}
	// 2 processes + 1 socket + 3 FIFOs + 1 veth = 7
	assert.Equal(t, 7, r.Total())
}

func TestSweepResult_Total_OnlyVeths(t *testing.T) {
	t.Parallel()

	r := &orphan.SweepResult{
		OrphanedVeths: []orphan.OrphanedVeth{
			{Name: "veth-10", SlotIdx: 10, DetectedAt: time.Now()},
			{Name: "veth-20", SlotIdx: 20, DetectedAt: time.Now()},
		},
	}
	assert.Equal(t, 2, r.Total())
}
