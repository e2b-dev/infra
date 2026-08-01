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
// client builds its http.Client without a Timeout and without a
// ResponseHeaderTimeout, so an agent that accepts the connection but never
// answers would otherwise block the caller forever — stalling orchestrator
// shutdown, which releases every pooled slot through this storage.
const consulRequestTimeout = 5 * time.Second

type StorageKV struct {
	config       Config
	slotsSize    int
	consulClient *consulApi.Client
	nodeID       string
	egressProxy  EgressProxy
	// requestTimeout bounds each individual Consul round trip. Tests shrink it.
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

// opContext bounds a single Consul round trip so no individual call can hang,
// even when the caller's context carries no deadline. The caller must cancel.
func (s *StorageKV) opContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.requestTimeout)
}

// newConsulConfig builds the Consul client configuration with an explicit
// overall HTTP timeout, which consulApi.DefaultConfig() does not set.
func newConsulConfig(token string) (*consulApi.Config, error) {
	config := consulApi.DefaultConfig()
	config.Token = token

	// Build the same client consulApi.NewClient would have built, but with an
	// explicit Timeout. Every call site below already bounds itself with a
	// per-operation context; this is the backstop so a call added later without
	// one cannot go unbounded. Note NewClient replaces this client for
	// "unix://" addresses, which we never configure.
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
		// Stop retrying as soon as the caller gives up instead of spending the
		// remaining attempts on doomed round trips.
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

// reservedKeys lists the slot keys already taken on this node. It lives in its
// own function so the per-request context is cancelled as soon as the call
// returns.
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

// slotPair reads the slot's KV entry. Separate function so the per-request
// context is cancelled before the follow-up delete starts.
func (s *StorageKV) slotPair(ctx context.Context, kv *consulApi.KV, key string) (*consulApi.KVPair, error) {
	opCtx, cancel := s.opContext(ctx)
	defer cancel()

	pair, _, err := kv.Get(key, (&consulApi.QueryOptions{}).WithContext(opCtx))

	return pair, err
}
