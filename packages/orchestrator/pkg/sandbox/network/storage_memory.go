//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
)

type StorageMemory struct {
	config      Config
	slotsSize   int
	freeSlots   []bool
	freeSlotsMu sync.Mutex
	egressProxy EgressProxy
	slotFactory func(key string, slotIdx int) (*Slot, error)
}

func NewStorageMemory(slotsSize int, config Config, egressProxy EgressProxy) (*StorageMemory, error) {
	return &StorageMemory{
		config:      config,
		slotsSize:   slotsSize,
		freeSlots:   make([]bool, slotsSize),
		freeSlotsMu: sync.Mutex{},
		egressProxy: egressProxy,
		slotFactory: func(key string, slotIdx int) (*Slot, error) {
			return NewSlot(key, slotIdx, config, egressProxy)
		},
	}, nil
}

func (s *StorageMemory) Acquire(_ context.Context) (*Slot, error) {
	s.freeSlotsMu.Lock()
	defer s.freeSlotsMu.Unlock()

	// Simple slot tracking in memory
	// We skip the first slot because it's the host slot
	slotFactory := s.slotFactory
	if slotFactory == nil {
		slotFactory = func(key string, slotIdx int) (*Slot, error) {
			return NewSlot(key, slotIdx, s.config, s.egressProxy)
		}
	}
	for slotIdx := 1; slotIdx < s.slotsSize; slotIdx++ {
		key := getMemoryKey(slotIdx)
		if !s.freeSlots[slotIdx] {
			slot, err := slotFactory(key, slotIdx)
			if err != nil {
				return nil, fmt.Errorf("failed to construct IP slot: %w", err)
			}
			s.freeSlots[slotIdx] = true

			return slot, nil
		}
	}

	return nil, errors.New("failed to acquire IP slot: no empty slots found")
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
