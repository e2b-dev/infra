package network

import (
	"context"
	"fmt"
	"strconv"
	"sync"
)

type StorageMemory struct {
	config      Config
	slotsSize   int
	freeSlots   []bool
	freeSlotsMu sync.Mutex
}

func NewStorageMemory(slotsSize int, config Config) (*StorageMemory, error) {
	return &StorageMemory{
		config:      config,
		slotsSize:   slotsSize,
		freeSlots:   make([]bool, slotsSize),
		freeSlotsMu: sync.Mutex{},
	}, nil
}

func (s *StorageMemory) Acquire(ctx context.Context) (*Slot, error) {
	s.freeSlotsMu.Lock()
	defer s.freeSlotsMu.Unlock()

	// Simple slot tracking in memory
	// We skip the first slot because it's the host slot
	for slotIdx := 1; slotIdx < s.slotsSize; slotIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, ctx.Err()
		}

		key := getMemoryKey(slotIdx)
		if !s.freeSlots[slotIdx] {
			s.freeSlots[slotIdx] = true

			return NewSlot(key, slotIdx, s.config)
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
	return strconv.Itoa(slotIdx)
}
