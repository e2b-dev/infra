package nodes

import (
	"sync/atomic"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type PlacementMetrics struct {
	sandboxesInProgress *smap.Map[*sbxInProgress]

	createSuccess atomic.Uint64
	createFails   atomic.Uint64
}

func (p *PlacementMetrics) Success(sandboxID string) {
	p.createSuccess.Add(1)
	p.RemoveSandbox(sandboxID)
}

func (p *PlacementMetrics) Fail(sandboxID string) {
	p.createFails.Add(1)
	p.RemoveSandbox(sandboxID)
}

func (p *PlacementMetrics) SuccessCount() uint64 {
	return p.createSuccess.Load()
}

func (p *PlacementMetrics) FailsCount() uint64 {
	return p.createFails.Load()
}

func (p *PlacementMetrics) InProgress() map[string]*sbxInProgress {
	return p.sandboxesInProgress.Items()
}

func (p *PlacementMetrics) InProgressCount() uint32 {
	return uint32(p.sandboxesInProgress.Count())
}

func (p *PlacementMetrics) AddSandbox(sandboxID string, CPUs, MibMemory int64) {
	p.sandboxesInProgress.Insert(sandboxID, &sbxInProgress{
		MiBMemory: MibMemory,
		CPUs:      CPUs,
	})
}

func (p *PlacementMetrics) RemoveSandbox(sandboxID string) {
	p.sandboxesInProgress.Remove(sandboxID)
}
