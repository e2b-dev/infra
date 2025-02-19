package network

import (
	"fmt"
	"math/rand"
	"slices"

	consulApi "github.com/hashicorp/consul/api"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
)

type StorageKV struct {
	slotsSize int
}

func NewStorageKV(slotsSize int) *StorageKV {
	return &StorageKV{
		slotsSize: slotsSize,
	}
}

func (s *StorageKV) Acquire() (*Slot, error) {
	kv := consul.Client().KV()

	var slot *Slot

	trySlot := func(slotIdx int, key string) (*Slot, error) {
		status, _, err := kv.CAS(&consulApi.KVPair{
			Key:         key,
			ModifyIndex: 0,
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to write to Consul KV: %w", err)
		}

		if status {
			return NewSlot(key, slotIdx), nil
		}

		return nil, nil
	}

	for randomTry := 1; randomTry <= 10; randomTry++ {
		slotIdx := rand.Intn(s.slotsSize)
		key := getKVKey(slotIdx)

		maybeSlot, err := trySlot(slotIdx, key)
		if err != nil {
			return nil, err
		}

		if maybeSlot != nil {
			slot = maybeSlot

			break
		}
	}

	if slot == nil {
		// This is a fallback for the case when all slots are taken.
		// There is no Consul lock so it's possible that multiple sandboxes will try to acquire the same slot.
		// In this case, only one of them will succeed and other will try with different slots.
		reservedKeys, _, keysErr := kv.Keys(consul.ClientID+"/", "", nil)
		if keysErr != nil {
			return nil, fmt.Errorf("failed to read Consul KV: %w", keysErr)
		}

		for slotIdx := 0; slotIdx < s.slotsSize; slotIdx++ {
			key := getKVKey(slotIdx)

			if slices.Contains(reservedKeys, key) {
				continue
			}

			maybeSlot, err := trySlot(slotIdx, key)
			if err != nil {
				return nil, err
			}

			if maybeSlot != nil {
				slot = maybeSlot

				break
			}
		}
	}

	if slot == nil {
		return nil, fmt.Errorf("failed to acquire IP slot: no empty slots found")
	}

	return slot, nil
}

func (s *StorageKV) Release(ips *Slot) error {
	kv := consul.Client().KV()

	pair, _, err := kv.Get(ips.Key, nil)
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to read Consul KV: %w", err)
	}

	if pair == nil {
		return fmt.Errorf("IP slot %d was already released", ips.Idx)
	}

	status, _, err := kv.DeleteCAS(&consulApi.KVPair{
		Key:         ips.Key,
		ModifyIndex: pair.ModifyIndex,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to delete slot from Consul KV: %w", err)
	}

	if !status {
		return fmt.Errorf("IP slot '%d' for was already realocated", ips.Idx)
	}

	return nil
}

func getKVKey(slotIdx int) string {
	return fmt.Sprintf("%s/%d", consul.ClientID, slotIdx)
}
