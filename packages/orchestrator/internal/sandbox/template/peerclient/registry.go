package peerclient

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

func peerRedisKey(buildID string) string {
	return "peer:" + buildID
}

// Registry manages the per-build routing entries in Redis that tell peer
// orchestrators where to find snapshot files during the upload window.
type Registry interface {
	// Register advertises this node as the source for the given build's files.
	// The entry expires after ttl; callers should also call Unregister once
	// the GCS upload completes so peers switch to GCS sooner.
	Register(ctx context.Context, buildID string, ttl time.Duration) error
	// Lookup returns the address of the peer holding files for the given build,
	// or (false, nil) when no entry exists.
	Lookup(ctx context.Context, buildID string) (string, bool, error)
	// Unregister removes the routing entry for the given build.
	Unregister(ctx context.Context, buildID string) error
}

type redisRegistry struct {
	redis       redis.UniversalClient
	nodeAddress string
}

func NewRedisRegistry(client redis.UniversalClient, nodeAddress string) Registry {
	return &redisRegistry{redis: client, nodeAddress: nodeAddress}
}

func (r *redisRegistry) Register(ctx context.Context, buildID string, ttl time.Duration) error {
	return r.redis.Set(ctx, peerRedisKey(buildID), r.nodeAddress, ttl).Err()
}

func (r *redisRegistry) Lookup(ctx context.Context, buildID string) (string, bool, error) {
	addr, err := r.redis.Get(ctx, peerRedisKey(buildID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}

	return addr, true, err
}

func (r *redisRegistry) Unregister(ctx context.Context, buildID string) error {
	return r.redis.Del(ctx, peerRedisKey(buildID)).Err()
}

// nopRegistry is a Registry that silently discards all operations.
// It is used when peer-to-peer routing is disabled (e.g. Redis is not configured).
type nopRegistry struct{}

func NopRegistry() Registry { return nopRegistry{} }

func (nopRegistry) Register(_ context.Context, _ string, _ time.Duration) error { return nil }
func (nopRegistry) Lookup(_ context.Context, _ string) (string, bool, error) {
	return "", false, nil
}
func (nopRegistry) Unregister(_ context.Context, _ string) error { return nil }
