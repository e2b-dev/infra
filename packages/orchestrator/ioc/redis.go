package ioc

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	sharedevents "github.com/e2b-dev/infra/packages/shared/pkg/events"
)

func newRedisModule(config cfg.Config) fx.Option {
	return If(
		"redis",
		config.RedisURL != "",
		fx.Provide(
			newRedisClient,
			AsDeliveryTarget(newRedisDeliveryTarget),
		),
	).ElseIf(config.RedisClusterURL != "",
		fx.Provide(
			newRedisClusterClient,
			AsDeliveryTarget(newRedisDeliveryTarget),
		),
	).Build()
}

func newRedisClient(config cfg.Config) redis.UniversalClient {
	return redis.NewClient(&redis.Options{
		Addr:         config.RedisURL,
		MinIdleConns: 1,
	})
}

func newRedisClusterClient(config cfg.Config) (redis.UniversalClient, error) {
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
			zap.L().Error("Failed to decode Redis cluster TLS CA certificate from base64", zap.Error(err))

			return nil, err
		}

		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(cert) {
			zap.L().Error("Failed to parse Redis cluster TLS CA certificate")

			return nil, fmt.Errorf("failed to parse Redis cluster TLS CA certificate")
		}

		// Remove the port if present
		serverName := strings.Split(config.RedisClusterURL, ":")[0]
		clusterOpts.TLSConfig = &tls.Config{
			RootCAs:    certPool,
			MinVersion: tls.VersionTLS12,
			ServerName: serverName,
		}

		zap.L().Info("Redis cluster will be started with TLS enabled")
	}

	return redis.NewClusterClient(clusterOpts), nil
}

func newRedisDeliveryTarget(redisClient redis.UniversalClient) *sharedevents.RedisStreamsDelivery[sharedevents.SandboxEvent] {
	return sharedevents.NewRedisStreamsDelivery[sharedevents.SandboxEvent](
		redisClient, sharedevents.SandboxEventsStreamName,
	)
}
