//go:build linux

package v2

import (
	"fmt"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

// SlotV2 extends a base network.Slot with v2-specific metadata.
// It wraps (composition) rather than embeds so the base *Slot flows
// through the existing sandbox pipeline unchanged.
type SlotV2 struct {
	Slot *network.Slot

	NetworkVersion int
	SandboxID      string
	ExecutionID    string
}

// NewSlotV2 wraps a base Slot with v2 metadata.
func NewSlotV2(slot *network.Slot) *SlotV2 {
	return &SlotV2{
		Slot:           slot,
		NetworkVersion: 2,
	}
}

// SlotV2Registry tracks v2 metadata for slots by their index.
type SlotV2Registry struct {
	mu    sync.RWMutex
	slots map[int]*SlotV2
}

func NewSlotV2Registry() *SlotV2Registry {
	return &SlotV2Registry{
		slots: make(map[int]*SlotV2),
	}
}

func (r *SlotV2Registry) Store(slotV2 *SlotV2) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slots[slotV2.Slot.Idx] = slotV2
}

func (r *SlotV2Registry) Load(idx int) (*SlotV2, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.slots[idx]

	return s, ok
}

func (r *SlotV2Registry) Delete(idx int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.slots, idx)
}

func (r *SlotV2Registry) Range(fn func(idx int, s *SlotV2) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for idx, s := range r.slots {
		if !fn(idx, s) {
			return
		}
	}
}

// String returns a debug-friendly representation.
func (s *SlotV2) String() string {
	return fmt.Sprintf("SlotV2{idx=%d}", s.Slot.Idx)
}
