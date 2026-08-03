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

const (
	// consulRequestTimeout bounds a single Consul HTTP request, so an agent
	// that accepts the connection and then goes silent cannot block forever.
	consulRequestTimeout = 5 * time.Second

	// consulReleaseTimeout bounds a whole slot release (Get + DeleteCAS); keep
	// it <= 2*consulRequestTimeout. Pool.Close drains slots serially, so this
	// is the per-slot cost of an unreachable agent during shutdown.
	consulReleaseTimeout = 10 * time.Second
)

type StorageKV struct {
	config       Config
	slotsSize    int
	consulClient *consulApi.Client
	nodeID       string
	egressProxy  EgressProxy

	// releaseTimeout is the per-release deadline; consulReleaseTimeout outside tests.
	releaseTimeout time.Duration
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
		releaseTimeout: consulReleaseTimeout,
	}, nil
}

// newConsulConfig builds the Consul client configuration with an explicit
// overall HTTP timeout, which consulApi.DefaultConfig() does not set.
func newConsulConfig(token string) (*consulApi.Config, error) {
	config := consulApi.DefaultConfig()
	config.Token = token

	// NewClient replaces HttpClient for "unix://" addresses; we never configure one.
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
		status, _, err := kv.CAS(&consulApi.KVPair{
			Key:         key,
			ModifyIndex: 0,
		}, (&consulApi.WriteOptions{}).WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("failed to write to Consul KV: %w", err)
		}

		if status {
			return NewSlot(key, slotIdx, s.config, s.egressProxy)
		}

		return nil, nil
	}

	for randomTry := 1; randomTry <= 10; randomTry++ {
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
		reservedKeys, _, keysErr := kv.Keys(s.nodeID+"/", "", (&consulApi.QueryOptions{}).WithContext(ctx))
		if keysErr != nil {
			return nil, fmt.Errorf("failed to read Consul KV: %w", keysErr)
		}

		for slotIdx := range s.slotsSize {
			// Reserved slots skip the CAS below, so without this check a
			// cancelled context still walks every remaining slot.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("failed to acquire IP slot: %w", ctxErr)
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

// Release deliberately takes no context: honouring a caller's cancellation
// would skip the delete and leak the node-scoped Consul key, which nothing
// reclaims. The release is unconditional but bounded by releaseTimeout.
func (s *StorageKV) Release(ips *Slot) error {
	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), s.releaseTimeout)
	defer releaseCancel()

	kv := s.consulClient.KV()

	pair, _, err := kv.Get(ips.Key, (&consulApi.QueryOptions{}).WithContext(releaseCtx))
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to read Consul KV: %w", err)
	}

	if pair == nil {
		return fmt.Errorf("IP slot %d was already released", ips.Idx)
	}

	status, _, err := kv.DeleteCAS(&consulApi.KVPair{
		Key:         ips.Key,
		ModifyIndex: pair.ModifyIndex,
	}, (&consulApi.WriteOptions{}).WithContext(releaseCtx))
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to delete slot from Consul KV: %w", err)
	}

	if !status {
		return fmt.Errorf("IP slot '%d' for was already realocated", ips.Idx)
	}

	return nil
}
