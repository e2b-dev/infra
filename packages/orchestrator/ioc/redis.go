package ioc

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	sharedevents "github.com/e2b-dev/infra/packages/shared/pkg/events"
	"github.com/e2b-dev/infra/packages/shared/pkg/factories"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
)

func NewRedisModule() fx.Option {
	return fx.Module(
		"redis",
		fx.Provide(
			newRedisClient,
			AsDeliveryTarget(newRedisDeliveryTarget),
		),
	)
}

func newRedisClient(config cfg.Config) (redis.UniversalClient, error) {
	return factories.NewRedisClient(context.Background(), factories.RedisConfig{
		RedisURL:         config.RedisURL,
		RedisClusterURL:  config.RedisClusterURL,
		RedisTLSCABase64: config.RedisTLSCABase64,
	})
}

func newRedisDeliveryTarget(redisClient redis.UniversalClient) *sharedevents.RedisStreamsDelivery[sharedevents.SandboxEvent] {
	return sharedevents.NewRedisStreamsDelivery[sharedevents.SandboxEvent](
		redisClient, sharedevents.SandboxEventsStreamName,
	)
}
