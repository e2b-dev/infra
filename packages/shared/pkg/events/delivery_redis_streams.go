package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RedisStreamsDelivery[Payload any] struct {
	redisClient redis.UniversalClient
	streamName  string
}

const SandboxEventsStreamName = "sandbox-events-stream"

func NewRedisStreamsDelivery[Payload any](redisClient redis.UniversalClient, streamName string) *RedisStreamsDelivery[Payload] {
	return &RedisStreamsDelivery[Payload]{
		redisClient: redisClient,
		streamName:  streamName,
	}
}

func (r *RedisStreamsDelivery[Payload]) Publish(ctx context.Context, deliveryKey string, payload Payload) error {
	delivery, err := r.shouldPublish(ctx, deliveryKey)
	if err != nil {
		return fmt.Errorf("could not determine if redis stream is published: %w", err)
	}

	if !delivery {
		return nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// Use XADD to add entry to stream with auto-generated ID
	_, err = r.redisClient.
		XAdd(ctx, &redis.XAddArgs{Stream: r.streamName, ID: "*", Values: data}).
		Result()

	return err
}

func (r *RedisStreamsDelivery[Payload]) shouldPublish(ctx context.Context, key string) (bool, error) {
	exists, err := r.redisClient.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}

	return exists > 0, nil
}

func (r *RedisStreamsDelivery[Payload]) Close(context.Context) error {
	return nil
}
