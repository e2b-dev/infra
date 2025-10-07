package network

import (
	"context"
	"fmt"
	"math/rand"
	"slices"

	consul "github.com/hashicorp/consul/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type copyAndSet interface {
	CAS(kv *consul.KVPair, opts *consul.WriteOptions) (bool, *consul.WriteMeta, error)
	DeleteCAS(kv *consul.KVPair, opts *consul.WriteOptions) (bool, *consul.WriteMeta, error)
	Get(key string, q *consul.QueryOptions) (*consul.KVPair, *consul.QueryMeta, error)
	Keys(prefix, separator string, q *consul.QueryOptions) ([]string, *consul.QueryMeta, error)
}

var _ copyAndSet = (*consul.KV)(nil)

const attempts = 10

type StorageKV struct {
	slotsSize int
	kv        copyAndSet
	nodeID    string
}

func (s *StorageKV) getKVKey(slotIdx int) string {
	return fmt.Sprintf("%s/%d", s.nodeID, slotIdx)
}

func NewStorageKV(slotsSize int, nodeID string) (*StorageKV, error) {
	consulToken := utils.RequiredEnv("CONSUL_TOKEN", "Consul token for authenticating requests to the Consul API")

	consulClient, err := newConsulClient(consulToken)
	if err != nil {
		return nil, fmt.Errorf("failed to init StorageKV consul client: %w", err)
	}

	return &StorageKV{
		slotsSize: slotsSize,
		kv:        consulClient.KV(),
		nodeID:    nodeID,
	}, nil
}

func newConsulClient(token string) (*consul.Client, error) {
	config := consul.DefaultConfig()
	config.Token = token

	consulClient, err := consul.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Consul client: %w", err)
	}

	return consulClient, nil
}

func (s *StorageKV) Acquire(ctx context.Context) (*Slot, error) {
	var slot *Slot

	trySlot := func(slotIdx int, key string) (*Slot, error) {
		status, _, err := s.kv.CAS(&consul.KVPair{
			Key:         key,
			ModifyIndex: 0,
		}, new(consul.WriteOptions).WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to write to Consul KV: %w", err)
		}

		if status {
			return NewSlot(key, slotIdx)
		}

		return nil, nil
	}

	for range attempts {
		slotIdx := rand.Intn(s.slotsSize) + 1 // network slots are 1-based
		key := s.getKVKey(slotIdx)

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
		reservedKeys, _, keysErr := s.kv.Keys(s.nodeID+"/", "", new(consul.QueryOptions).WithContext(ctx))
		if keysErr != nil {
			return nil, fmt.Errorf("failed to read Consul KV: %w", keysErr)
		}

		for slotIdx := range s.slotsSize {
			key := s.getKVKey(slotIdx)

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

func (s *StorageKV) Release(ctx context.Context, ips *Slot) error {
	pair, _, err := s.kv.Get(ips.Key, new(consul.QueryOptions).WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to read Consul KV: %w", err)
	}

	if pair == nil {
		return fmt.Errorf("IP slot %d was already released", ips.Idx)
	}

	status, _, err := s.kv.DeleteCAS(&consul.KVPair{
		Key:         ips.Key,
		ModifyIndex: pair.ModifyIndex,
	}, new(consul.WriteOptions).WithContext(ctx))
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to delete slot from Consul KV: %w", err)
	}

	if !status {
		return fmt.Errorf("IP slot '%d' for was already realocated", ips.Idx)
	}

	return nil
}
