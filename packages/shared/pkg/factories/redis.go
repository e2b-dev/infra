package factories

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var ErrRedisDisabled = errors.New("redis is disabled")

type RedisConfig struct {
	RedisURL         string
	RedisClusterURL  string
	RedisTLSCABase64 string
	// PoolSize overrides the default connection pool size.
	// When non-positive, defaults to 40.
	PoolSize int
	// MinIdleConns overrides the minimum number of idle connections maintained in the pool
	// (per cluster node for cluster clients).
	// When non-positive, defaults to min(defaultMinIdleConns, PoolSize).
	MinIdleConns int
}

const (
	defaultPoolSize     = 40
	defaultMinIdleConns = 10

	// connMaxLifetime controls the maximum age of a connection before it is recycled.
	// Combined with connMaxLifetimeJitter, this spreads connection recycling evenly over time
	// instead of expiring in bursts (which happens with idle-time-based eviction under LIFO reuse).
	connMaxLifetime = 30 * time.Minute
	// connMaxLifetimeJitter adds random offset in [-jitter, +jitter] to each connection's lifetime,
	// so connections expire between 20-40 minutes instead of all at exactly 30 minutes.
	connMaxLifetimeJitter = 10 * time.Minute

	// redisConnectTimeout bounds the startup ping retry window.
	redisConnectTimeout = 30 * time.Second
)

// resolvePoolSize computes the effective pool size and minimum idle connections
// from the given config, applying defaults and floors.
func resolvePoolSize(config RedisConfig) (poolSize, minIdleConns int) {
	poolSize = defaultPoolSize
	if config.PoolSize > 0 {
		poolSize = config.PoolSize
	}

	minIdleConns = min(defaultMinIdleConns, poolSize)
	if config.MinIdleConns > 0 {
		minIdleConns = min(config.MinIdleConns, poolSize)
	}

	return poolSize, minIdleConns
}

func NewRedisClient(ctx context.Context, config RedisConfig) (redis.UniversalClient, error) {
	var redisClient redis.UniversalClient

	poolSize, minIdleConns := resolvePoolSize(config)

	switch {
	case config.RedisClusterURL != "":
		// For managed Redis Cluster in GCP we should use Cluster Client, because
		// > Redis node endpoints can change and can be recycled as nodes are added and removed over time.
		// https://cloud.google.com/memorystore/docs/cluster/cluster-node-specification#cluster_endpoints
		// https://cloud.google.com/memorystore/docs/cluster/client-library-code-samples#go-redis

		clusterOpts := &redis.ClusterOptions{
			Addrs:        []string{config.RedisClusterURL},
			PoolSize:     poolSize,
			MinIdleConns: minIdleConns,
			// Disable idle-time eviction; use lifetime-based recycling with jitter instead.
			// Under the default LIFO reuse, ConnMaxIdleTime causes thundering-herd bursts because
			// the cold (bottom-of-stack) connections all idle-expire simultaneously.
			ConnMaxIdleTime:       -1,
			ConnMaxLifetime:       connMaxLifetime,
			ConnMaxLifetimeJitter: connMaxLifetimeJitter,
		}

		if config.RedisTLSCABase64 != "" {
			cert, err := base64.StdEncoding.DecodeString(config.RedisTLSCABase64)
			if err != nil {
				logger.L().Error(ctx, "Failed to decode Redis cluster TLS CA certificate from base64", zap.Error(err))

				return nil, err
			}

			certPool := x509.NewCertPool()
			if !certPool.AppendCertsFromPEM(cert) {
				logger.L().Error(ctx, "Failed to parse Redis cluster TLS CA certificate")

				return nil, errors.New("failed to parse Redis cluster TLS CA certificate")
			}

			// Remove the port if present
			serverName := strings.Split(config.RedisClusterURL, ":")[0]
			clusterOpts.TLSConfig = &tls.Config{
				RootCAs:    certPool,
				MinVersion: tls.VersionTLS12,
				ServerName: serverName,
			}

			logger.L().Info(ctx, "Redis cluster will be started with TLS enabled")
		}

		redisClient = redis.NewClusterClient(clusterOpts)
	case config.RedisURL != "":
		opts := &redis.Options{
			Addr:                  config.RedisURL,
			PoolSize:              poolSize,
			MinIdleConns:          minIdleConns,
			ConnMaxIdleTime:       -1,
			ConnMaxLifetime:       connMaxLifetime,
			ConnMaxLifetimeJitter: connMaxLifetimeJitter,
		}

		redisClient = redis.NewClient(opts)
	default:
		return nil, ErrRedisDisabled
	}

	// Enable tracing.
	if err := redisotel.InstrumentTracing(
		redisClient,
		redisotel.WithDBStatement(false),
		redisotel.WithCallerEnabled(false),
	); err != nil {
		closeErr := redisClient.Close()

		return nil, errors.Join(fmt.Errorf("failed to enable redis tracing: %w", err), closeErr)
	}

	// Enable metrics (pool stats, command latency histograms)
	if err := redisotel.InstrumentMetrics(redisClient); err != nil {
		closeErr := redisClient.Close()

		return nil, errors.Join(fmt.Errorf("failed to enable redis metrics: %w", err), closeErr)
	}

	if err := pingWithRetry(ctx, redisClient); err != nil {
		closeErr := redisClient.Close()

		return nil, errors.Join(fmt.Errorf("failed to ping redis: %w", err), closeErr)
	}

	return redisClient, nil
}

// redisConnectWindow is redisConnectTimeout unless REDIS_CONNECT_TIMEOUT
// overrides it. Environments that induce outages longer than the default —
// deterministic simulation testing, where network partitions are deliberate and
// can outlast 30s — need a longer window, but production keeps the default.
func redisConnectWindow() time.Duration {
	if v := os.Getenv("REDIS_CONNECT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}

	return redisConnectTimeout
}

// pingWithRetry retries the startup ping for a bounded window. Callers treat a
// failure here as fatal, so a single dropped packet would otherwise take the
// process down — and since every node reconnects at once after a Redis blip,
// that turns a brief outage into a fleet-wide crash loop.
func pingWithRetry(ctx context.Context, client redis.UniversalClient) error {
	deadline := time.Now().Add(redisConnectWindow())
	backoff := 100 * time.Millisecond

	for attempt := 1; ; attempt++ {
		_, err := client.Ping(ctx).Result()
		if err == nil {
			return nil
		}

		if ctx.Err() != nil || time.Now().After(deadline) {
			return err
		}

		logger.L().Warn(ctx, "Redis ping failed, retrying",
			zap.Int("attempt", attempt), zap.Error(err))

		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}

		if backoff < time.Second {
			backoff *= 2
		}
	}
}

func CloseCleanly(client redis.UniversalClient) error {
	if err := client.Close(); err != nil && !errors.Is(err, redis.ErrClosed) {
		return err
	}

	return nil
}
