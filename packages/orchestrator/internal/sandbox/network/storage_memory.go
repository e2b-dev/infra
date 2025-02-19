package network

import (
	"fmt"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
	"sync"
)

var (
	freeSlots   = make([]bool, slotsSize)
	freeSlotsMu sync.Mutex
)

type StorageMemory struct {
	slotsSize int
}

func NewStorageMemory(slotsSize int) *StorageMemory {
	return &StorageMemory{
		slotsSize: slotsSize,
	}
}

func (s *StorageMemory) Acquire() (*Slot, error) {
	freeSlotsMu.Lock()
	defer freeSlotsMu.Unlock()

	// Simple slot tracking in memory
	for slotIdx := 0; slotIdx < s.slotsSize; slotIdx++ {
		key := getMemoryKey(slotIdx)
		if !freeSlots[slotIdx] {
			freeSlots[slotIdx] = true
			return NewSlot(key, slotIdx), nil
		}
	}

	return nil, fmt.Errorf("failed to acquire IP slot: no empty slots found")
}

func (s *StorageMemory) Release(ips *Slot) error {
	freeSlotsMu.Lock()
	defer freeSlotsMu.Unlock()

	freeSlots[ips.Idx] = false

	return nil
}

func getMemoryKey(slotIdx int) string {
	return fmt.Sprintf("%s/%d", consul.ClientID, slotIdx)
}
