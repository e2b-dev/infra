package factories

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
)

var ErrRedisDisabled = errors.New("redis is disabled")

func NewRedisClient(ctx context.Context, config cfg.Config) (redis.UniversalClient, error) {
	var redisClient redis.UniversalClient

	switch {
	case config.RedisClusterURL != "":
		// For managed Redis Cluster in GCP we should use Cluster Client, because
		// > Redis node endpoints can change and can be recycled as nodes are added and removed over time.
		// https://cloud.google.com/memorystore/docs/cluster/cluster-node-specification#cluster_endpoints
		// https://cloud.google.com/memorystore/docs/cluster/client-library-code-samples#go-redis
		redisClient = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:        []string{config.RedisClusterURL},
			MinIdleConns: 1,
		})
	case config.RedisURL != "":
		redisClient = redis.NewClient(&redis.Options{
			Addr:         config.RedisURL,
			MinIdleConns: 1,
		})
	default:
		return nil, ErrRedisDisabled
	}

	if _, err := redisClient.Ping(ctx).Result(); err != nil {
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	return redisClient, nil
}

func CloseCleanly(client redis.UniversalClient) error {
	if err := client.Close(); err != nil && !errors.Is(err, redis.ErrClosed) {
		return err
	}
	return nil
}
