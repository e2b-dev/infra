package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/store"
)

const (
	sandboxKeyPrefix   = "sandbox:"
	sandboxExpiryIndex = "sandbox:expiry"
	defaultTTL         = 24 * time.Hour
)

type Store struct {
	client redis.UniversalClient
	// TODO: reservations         *store.ReservationStore
	insertCallbacks      []store.InsertCallback
	insertAsyncCallbacks []store.InsertCallback
	removeSandbox        func(ctx context.Context, sbx *store.Sandbox, removeType store.RemoveType) error
	removeAsyncCallbacks []store.RemoveCallback
}

var _ store.Backend = &Store{}

func NewRedisStore(
	client redis.UniversalClient,
	removeSandbox func(ctx context.Context, sbx *store.Sandbox, removeType store.RemoveType) error,
	insertCallbacks []store.InsertCallback,
	insertAsyncCallbacks []store.InsertCallback,
	removeAsyncCallbacks []store.RemoveCallback,
) *Store {
	return &Store{
		client:               client,
		removeSandbox:        removeSandbox,
		insertCallbacks:      insertCallbacks,
		insertAsyncCallbacks: insertAsyncCallbacks,
		removeAsyncCallbacks: removeAsyncCallbacks,
	}
}

func (s *Store) sandboxKey(sandboxID string) string {
	return sandboxKeyPrefix + sandboxID
}

func (s *Store) Add(ctx context.Context, sandbox *store.Sandbox, newlyCreated bool) error {
	// Create a copy of the sandbox for serialization to avoid race conditions
	data, err := json.Marshal(&sandbox)
	if err != nil {
		return fmt.Errorf("failed to marshal sandbox: %w", err)
	}

	pipe := s.client.TxPipeline()

	key := s.sandboxKey(sandbox.SandboxID)
	ttl := time.Until(sandbox.EndTime) + defaultTTL
	if ttl <= 0 {
		ttl = defaultTTL
	}

	// Backend the sandbox data
	pipe.Set(ctx, key, data, ttl)

	// Add to expiry sorted set
	pipe.ZAdd(ctx, sandboxExpiryIndex, redis.Z{
		Score:  float64(sandbox.EndTime.Unix()),
		Member: sandbox.SandboxID,
	})

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to add sandbox to Redis: %w", err)
	}

	// Execute callbacks
	for _, callback := range s.insertCallbacks {
		callback(ctx, sandbox, newlyCreated)
	}

	for _, callback := range s.insertAsyncCallbacks {
		go callback(context.WithoutCancel(ctx), sandbox, newlyCreated)
	}

	// Release the reservation if it exists
	// TODO: r.reservations.release(sandbox.SandboxID)

	return nil
}

func (s *Store) Exists(ctx context.Context, sandboxID string) bool {
	exists, err := s.client.Exists(ctx, s.sandboxKey(sandboxID)).Result()
	if err != nil {
		zap.L().Error("Failed to check if sandbox exists in Redis", zap.Error(err))
		return false
	}
	return exists > 0
}

func (s *Store) Get(ctx context.Context, sandboxID string, includeEvicting bool) (*store.Sandbox, error) {
	data, err := s.client.Get(ctx, s.sandboxKey(sandboxID)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("sandbox \"%s\" doesn't exist", sandboxID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox from Redis: %w", err)
	}

	var sandbox store.Sandbox
	if err := json.Unmarshal([]byte(data), &sandbox); err != nil {
		return nil, fmt.Errorf("failed to unmarshal sandbox: %w", err)
	}

	if sandbox.State != store.StateRunning && !includeEvicting {
		return nil, fmt.Errorf("sandbox \"%s\" is being evicted", sandboxID)
	}

	return &sandbox, nil
}

func (s *Store) Remove(ctx context.Context, sandboxID string, removeType store.RemoveType) error {
	sandbox, err := s.Get(ctx, sandboxID, true)
	if err != nil {
		return err
	}

	// Update the sandbox state in Redis before removal
	if err := s.updateSandboxState(ctx, sandbox); err != nil {
		zap.L().Error("Failed to update sandbox state before removal",
			zap.String("sandbox_id", sandboxID),
			zap.Error(err))
	}

	// Remove from the cache after processing
	defer func() {
		pipe := s.client.TxPipeline()
		pipe.Del(ctx, s.sandboxKey(sandboxID))
		pipe.ZRem(ctx, sandboxExpiryIndex, sandboxID)
		if _, err := pipe.Exec(ctx); err != nil {
			zap.L().Error("Failed to remove sandbox from Redis indices",
				zap.String("sandbox_id", sandboxID),
				zap.Error(err))
		}
	}()

	// Remove the sandbox from the node
	err = s.removeSandbox(ctx, sandbox, removeType)
	for _, callback := range s.removeAsyncCallbacks {
		go callback(context.WithoutCancel(ctx), sandbox, removeType)
	}
	if err != nil {
		return fmt.Errorf("error removing sandbox \"%s\": %w", sandboxID, err)
	}

	return nil
}

func (s *Store) updateSandboxState(ctx context.Context, sandbox *store.Sandbox) error {
	// Create a copy for serialization
	data, err := json.Marshal(sandbox)
	if err != nil {
		return fmt.Errorf("failed to marshal sandbox: %w", err)
	}

	key := s.sandboxKey(sandbox.SandboxID)
	ttl, err := s.client.TTL(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("failed to get TTL: %w", err)
	}

	if ttl <= 0 {
		ttl = defaultTTL
	}

	// Update sandbox data
	if err := s.client.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("failed to update sandbox data: %w", err)
	}

	// Update state indices
	pipe := s.client.TxPipeline()

	_, err = pipe.Exec(ctx)
	return err
}

func (s *Store) Items(ctx context.Context, teamID *uuid.UUID) []*store.Sandbox {
	return nil
}

func (s *Store) ExpiredItems(ctx context.Context) []*store.Sandbox {
	items := make([]*store.Sandbox, 0)

	now := time.Now().Unix()
	expiredIDs, err := s.client.ZRangeByScore(ctx, sandboxExpiryIndex, &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%d", now),
	}).Result()
	if err != nil {
		zap.L().Error("Failed to get expired sandboxes from Redis", zap.Error(err))
		return items
	}

	for _, id := range expiredIDs {
		sandbox, err := s.Get(ctx, id, true)
		if err != nil {
			continue
		}
		if time.Now().After(sandbox.EndTime) {
			items = append(items, sandbox)
		}
	}

	return items
}

func (s *Store) ItemsByState(ctx context.Context, teamID *uuid.UUID, states []store.State) map[store.State][]*store.Sandbox {
	return nil
}

func (s *Store) Len(ctx context.Context, teamID *uuid.UUID) int {
	return len(s.Items(ctx, teamID))
}

// KeepAliveFor extends the sandbox's expiration timer
func (s *Store) Update(ctx context.Context, sandbox *store.Sandbox) error {
	if err := s.updateSandboxState(ctx, sandbox); err != nil {
		return fmt.Errorf("failed to update sandbox in Redis: %w", err)
	}

	// Update the expiry index
	pipe := s.client.TxPipeline()
	pipe.ZAdd(ctx, sandboxExpiryIndex, redis.Z{
		Score:  float64(sandbox.EndTime.Unix()),
		Member: sandbox.SandboxID,
	})
	if _, err := pipe.Exec(ctx); err != nil {
		zap.L().Error("Failed to update expiry index",
			zap.String("sandbox_id", sandbox.SandboxID),
			zap.Error(err))
		return fmt.Errorf("failed to update expiry index: %w", err)
	}

	return nil
}

// Reserve reserves a slot for a new sandbox
func (s *Store) Reserve(ctx context.Context, sandboxID string, team uuid.UUID, limit int64) (release func(), err error) {
	// TODO:

	return func() {
	}, nil
}

func (s *Store) MarkRemoving(ctx context.Context, sandboxID string, removeType store.RemoveType) (*store.Sandbox, error) {
	return s.Get(ctx, sandboxID, true)
	// TODO implement me
}

func (s *Store) WaitForStop(ctx context.Context, sandboxID string) error {
	return nil
	// TODO implement me
}
