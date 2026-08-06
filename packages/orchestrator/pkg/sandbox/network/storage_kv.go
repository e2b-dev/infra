//go:build linux

package network

import (
	"bytes"
	"context"
	cryptoRand "crypto/rand"
	"errors"
	"fmt"
	mathRand "math/rand"
	"slices"

	consulApi "github.com/hashicorp/consul/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type StorageKV struct {
	config       Config
	slotsSize    int
	consulClient *consulApi.Client
	nodeID       string
	egressProxy  EgressProxy
	slotFactory  func(key string, slotIdx int) (*Slot, error)
}

func (s *StorageKV) getKVKey(slotIdx int) string {
	return fmt.Sprintf("%s/%d", s.nodeID, slotIdx)
}

func NewStorageKV(nodeID string, config Config, egressProxy EgressProxy) (*StorageKV, error) {
	consulToken := utils.RequiredEnv("CONSUL_TOKEN", "Consul token for authenticating requests to the Consul API")

	consulClient, err := newConsulClient(consulToken)
	if err != nil {
		return nil, fmt.Errorf("failed to init StorageKV consul client: %w", err)
	}

	return &StorageKV{
		config:       config,
		slotsSize:    vrtSlotsSize,
		consulClient: consulClient,
		nodeID:       nodeID,
		egressProxy:  egressProxy,
		slotFactory: func(key string, slotIdx int) (*Slot, error) {
			return NewSlot(key, slotIdx, config, egressProxy)
		},
	}, nil
}

func newConsulClient(token string) (*consulApi.Client, error) {
	config := consulApi.DefaultConfig()
	config.Token = token

	consulClient, err := consulApi.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Consul client: %w", err)
	}

	return consulClient, nil
}

func (s *StorageKV) Acquire(_ context.Context) (*Slot, error) {
	kv := s.consulClient.KV()
	if s.slotsSize <= 1 {
		return nil, errors.New("failed to acquire IP slot: no usable slots configured")
	}

	var slot *Slot
	slotFactory := s.slotFactory
	if slotFactory == nil {
		slotFactory = func(key string, slotIdx int) (*Slot, error) {
			return NewSlot(key, slotIdx, s.config, s.egressProxy)
		}
	}

	trySlot := func(slotIdx int, key string) (*Slot, error) {
		reservationValue := make([]byte, 32)
		if _, err := cryptoRand.Read(reservationValue); err != nil {
			return nil, fmt.Errorf("failed to create IP slot reservation identity: %w", err)
		}
		status, _, err := kv.CAS(&consulApi.KVPair{
			Key:         key,
			ModifyIndex: 0,
			Value:       reservationValue,
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to write to Consul KV: %w", err)
		}

		if status {
			reservedSlot, slotErr := slotFactory(key, slotIdx)
			if slotErr == nil {
				reservedSlot.reservationValue = slices.Clone(reservationValue)
				return reservedSlot, nil
			}

			// CAS has already made the reservation visible. Compensate with a
			// second CAS bound to the exact version we created; never leak a key
			// when local slot construction rejects the reservation.
			pair, _, getErr := kv.Get(key, nil)
			if getErr != nil {
				return nil, fmt.Errorf("failed to construct IP slot (%v) and read its Consul reservation for rollback: %w", slotErr, getErr)
			}
			if pair == nil {
				return nil, fmt.Errorf("failed to construct IP slot: %w", slotErr)
			}
			if !bytes.Equal(pair.Value, reservationValue) {
				return nil, fmt.Errorf("failed to construct IP slot (%v) and its Consul reservation changed before rollback", slotErr)
			}
			deleted, _, deleteErr := kv.DeleteCAS(pair, nil)
			if deleteErr != nil {
				return nil, fmt.Errorf("failed to construct IP slot (%v) and delete its Consul reservation: %w", slotErr, deleteErr)
			}
			if !deleted {
				return nil, fmt.Errorf("failed to construct IP slot (%v) and its Consul reservation changed before rollback", slotErr)
			}
			return nil, fmt.Errorf("failed to construct IP slot: %w", slotErr)
		}

		return nil, nil
	}

	for randomTry := 1; randomTry <= 10; randomTry++ {
		slotIdx := mathRand.Intn(s.slotsSize-1) + 1
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
		reservedKeys, _, keysErr := kv.Keys(s.nodeID+"/", "", nil)
		if keysErr != nil {
			return nil, fmt.Errorf("failed to read Consul KV: %w", keysErr)
		}

		for slotIdx := 1; slotIdx < s.slotsSize; slotIdx++ {
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
		return nil, errors.New("failed to acquire IP slot: no empty slots found")
	}

	return slot, nil
}

func (s *StorageKV) Release(ips *Slot) error {
	kv := s.consulClient.KV()
	if ips == nil || len(ips.reservationValue) == 0 {
		return errors.New("failed to release IP slot: missing reservation identity")
	}

	pair, _, err := kv.Get(ips.Key, nil)
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to read Consul KV: %w", err)
	}

	if pair == nil {
		return fmt.Errorf("IP slot %d was already released", ips.Idx)
	}
	if !bytes.Equal(pair.Value, ips.reservationValue) {
		return fmt.Errorf("IP slot '%d' was already reallocated", ips.Idx)
	}

	status, _, err := kv.DeleteCAS(&consulApi.KVPair{
		Key:         ips.Key,
		ModifyIndex: pair.ModifyIndex,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to delete slot from Consul KV: %w", err)
	}

	if !status {
		return fmt.Errorf("IP slot '%d' was already reallocated", ips.Idx)
	}

	return nil
}
