//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"time"

	consulApi "github.com/hashicorp/consul/api"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// consulRequestTimeout bounds a single Consul KV round trip. The Consul API
// client's http.Client has no timeout of its own, so an agent that accepts
// the connection but never answers would block callers — including
// shutdown's slot releases — forever.
const consulRequestTimeout = 5 * time.Second

type StorageKV struct {
	config       Config
	slotsSize    int
	consulClient *consulApi.Client
	nodeID       string
	egressProxy  EgressProxy
	// requestTimeout bounds each Consul round trip; tests shrink it.
	requestTimeout time.Duration
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
		config:         config,
		slotsSize:      vrtSlotsSize,
		consulClient:   consulClient,
		nodeID:         nodeID,
		egressProxy:    egressProxy,
		requestTimeout: consulRequestTimeout,
	}, nil
}

func (s *StorageKV) opContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.requestTimeout)
}

func newConsulConfig(token string) (*consulApi.Config, error) {
	config := consulApi.DefaultConfig()
	config.Token = token

	// The client NewClient would build, plus a Timeout DefaultConfig never
	// sets — the backstop for any future call that skips a per-operation
	// context. NewClient replaces this client for "unix://" addresses, which
	// we never configure.
	httpClient, err := consulApi.NewHttpClient(config.Transport, config.TLSConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Consul HTTP client: %w", err)
	}

	httpClient.Timeout = consulRequestTimeout
	config.HttpClient = httpClient

	return config, nil
}

func newConsulClient(token string) (*consulApi.Client, error) {
	config, err := newConsulConfig(token)
	if err != nil {
		return nil, err
	}

	consulClient, err := consulApi.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Consul client: %w", err)
	}

	return consulClient, nil
}

func (s *StorageKV) Acquire(ctx context.Context) (*Slot, error) {
	kv := s.consulClient.KV()

	var slot *Slot

	trySlot := func(slotIdx int, key string) (*Slot, error) {
		opCtx, cancel := s.opContext(ctx)
		defer cancel()

		status, _, err := kv.CAS(&consulApi.KVPair{
			Key:         key,
			ModifyIndex: 0,
		}, (&consulApi.WriteOptions{}).WithContext(opCtx))
		if err != nil {
			return nil, fmt.Errorf("failed to write to Consul KV: %w", err)
		}

		if status {
			return NewSlot(key, slotIdx, s.config, s.egressProxy)
		}

		return nil, nil
	}

	for randomTry := 1; randomTry <= 10; randomTry++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("failed to acquire IP slot: %w", err)
		}

		slotIdx := rand.Intn(s.slotsSize)
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
		reservedKeys, keysErr := s.reservedKeys(ctx, kv)
		if keysErr != nil {
			return nil, fmt.Errorf("failed to read Consul KV: %w", keysErr)
		}

		for slotIdx := range s.slotsSize {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("failed to acquire IP slot: %w", err)
			}

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

// reservedKeys lists the slot keys already taken on this node. Separate
// function so its per-request context is cancelled on return.
func (s *StorageKV) reservedKeys(ctx context.Context, kv *consulApi.KV) ([]string, error) {
	opCtx, cancel := s.opContext(ctx)
	defer cancel()

	keys, _, err := kv.Keys(s.nodeID+"/", "", (&consulApi.QueryOptions{}).WithContext(opCtx))

	return keys, err
}

func (s *StorageKV) Release(ctx context.Context, ips *Slot) error {
	kv := s.consulClient.KV()

	pair, err := s.slotPair(ctx, kv, ips.Key)
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to read Consul KV: %w", err)
	}

	if pair == nil {
		return fmt.Errorf("IP slot %d was already released", ips.Idx)
	}

	deleteCtx, cancel := s.opContext(ctx)
	defer cancel()

	status, _, err := kv.DeleteCAS(&consulApi.KVPair{
		Key:         ips.Key,
		ModifyIndex: pair.ModifyIndex,
	}, (&consulApi.WriteOptions{}).WithContext(deleteCtx))
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to delete slot from Consul KV: %w", err)
	}

	if !status {
		return fmt.Errorf("IP slot '%d' for was already realocated", ips.Idx)
	}

	return nil
}

// slotPair reads the slot's KV entry. Separate function so its per-request
// context is cancelled before the follow-up delete.
func (s *StorageKV) slotPair(ctx context.Context, kv *consulApi.KV, key string) (*consulApi.KVPair, error) {
	opCtx, cancel := s.opContext(ctx)
	defer cancel()

	pair, _, err := kv.Get(key, (&consulApi.QueryOptions{}).WithContext(opCtx))

	return pair, err
}
