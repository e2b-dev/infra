package network

import (
	"fmt"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
)

type StorageMemory struct {
	slotsSize   int
	freeSlots   []bool
	freeSlotsMu sync.Mutex
}

func NewStorageMemory(slotsSize int) (*StorageMemory, error) {
	return &StorageMemory{
		slotsSize:   slotsSize,
		freeSlots:   make([]bool, slotsSize),
		freeSlotsMu: sync.Mutex{},
	}, nil
}

func (s *StorageMemory) Acquire() (*Slot, error) {
	s.freeSlotsMu.Lock()
	defer s.freeSlotsMu.Unlock()

	// Simple slot tracking in memory
	// We skip the first slot because it's the host slot
	for slotIdx := 1; slotIdx < s.slotsSize; slotIdx++ {
		key := getMemoryKey(slotIdx)
		if !s.freeSlots[slotIdx] {
			s.freeSlots[slotIdx] = true
			return NewSlot(key, slotIdx), nil
		}
	}

	return nil, fmt.Errorf("failed to acquire IP slot: no empty slots found")
}

func (s *StorageMemory) Release(ips *Slot) error {
	s.freeSlotsMu.Lock()
	defer s.freeSlotsMu.Unlock()

	s.freeSlots[ips.Idx] = false

	return nil
}

func getMemoryKey(slotIdx int) string {
	return fmt.Sprintf("%s/%d", consul.ClientID, slotIdx)
}
