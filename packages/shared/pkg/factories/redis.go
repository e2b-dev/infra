package factories

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

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
}

func NewRedisClient(ctx context.Context, config RedisConfig) (redis.UniversalClient, error) {
	var redisClient redis.UniversalClient

	switch {
	case config.RedisClusterURL != "":
		// For managed Redis Cluster in GCP we should use Cluster Client, because
		// > Redis node endpoints can change and can be recycled as nodes are added and removed over time.
		// https://cloud.google.com/memorystore/docs/cluster/cluster-node-specification#cluster_endpoints
		// https://cloud.google.com/memorystore/docs/cluster/client-library-code-samples#go-redis
		clusterOpts := &redis.ClusterOptions{
			Addrs:        []string{config.RedisClusterURL},
			MinIdleConns: 1,
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

				return nil, fmt.Errorf("failed to parse Redis cluster TLS CA certificate")
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
		redisClient = redis.NewClient(&redis.Options{
			Addr:         config.RedisURL,
			MinIdleConns: 1,
		})
	default:
		return nil, ErrRedisDisabled
	}

	// Enable tracing
	if err := redisotel.InstrumentTracing(redisClient); err != nil {
		closeErr := redisClient.Close()

		return nil, errors.Join(fmt.Errorf("failed to enable redis tracing: %w", err), closeErr)
	}

	if _, err := redisClient.Ping(ctx).Result(); err != nil {
		closeErr := redisClient.Close()

		return nil, errors.Join(fmt.Errorf("failed to ping redis: %w", err), closeErr)
	}

	return redisClient, nil
}

func CloseCleanly(client redis.UniversalClient) error {
	if err := client.Close(); err != nil && !errors.Is(err, redis.ErrClosed) {
		return err
	}

	return nil
}
