package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RedisPubSubDelivery[Payload any] struct {
	redisClient redis.UniversalClient
	queueName   string
}

const SandboxEventsQueueName = "sandbox-events"

func NewRedisPubSubDelivery[Payload any](redisClient redis.UniversalClient, queueName string) *RedisPubSubDelivery[Payload] {
	return &RedisPubSubDelivery[Payload]{
		redisClient: redisClient,
		queueName:   queueName,
	}
}

func (r *RedisPubSubDelivery[Payload]) Publish(ctx context.Context, deliveryKey string, payload Payload) error {
	delivery, err := r.shouldPublish(ctx, deliveryKey)
	if err != nil {
		return fmt.Errorf("could not determine if redis pubsub is published: %w", err)
	}

	if !delivery {
		return nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return r.redisClient.Publish(ctx, r.queueName, data).Err()
}

func (r *RedisPubSubDelivery[Payload]) shouldPublish(ctx context.Context, key string) (bool, error) {
	exists, err := r.redisClient.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}

	return exists > 0, nil
}

func (r *RedisPubSubDelivery[Payload]) Close(context.Context) error {
	return nil
}
