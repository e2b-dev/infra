package pubsub

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RedisPubSub[T, M any] struct {
	redisClient *redis.UniversalClient
	queueName   string
}

func NewRedisPubSub[T, M any](ctx context.Context, redisClient *redis.UniversalClient, queueName string) *RedisPubSub[T, M] {
	return &RedisPubSub[T, M]{
		redisClient: redisClient,
		queueName:   queueName,
	}
}
func (r *RedisPubSub[T, M]) ShouldPublish(ctx context.Context, key string) (bool, error) {
	if r.redisClient == nil {
		return false, fmt.Errorf("redis client is not initialized")
	}
	exists, err := (*r.redisClient).Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

func (r *RedisPubSub[T, M]) GetSubscriptionMetaData(ctx context.Context, key string) (*M, error) {
	if r.redisClient == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}
	var m M
	err := (*r.redisClient).Get(ctx, key).Scan(&m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *RedisPubSub[T, M]) SetSubscriptionMetaData(ctx context.Context, key string, metaData M) error {
	if r.redisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}
	return (*r.redisClient).Set(ctx, key, metaData, 0).Err()
}

func (r *RedisPubSub[T, M]) Publish(ctx context.Context, payload T) error {
	if r.redisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	data, err := encodeMessage(payload)
	if err != nil {
		return err
	}

	return (*r.redisClient).Publish(ctx, r.queueName, data).Err()
}

func (r *RedisPubSub[T, M]) Subscribe(ctx context.Context, pubSubQueue chan<- T) error {
	if r.redisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	redisPubSub := (*r.redisClient).Subscribe(ctx, r.queueName)
	redisPubSubChan := redisPubSub.Channel()

	// Loop forever until the context is done,
	// receiveing messages from Redis and sending them to pubSubQueue.
	for {
		select {
		case msg := <-redisPubSubChan:
			var t T
			err := decodeMessage(msg.Payload, &t)
			if err != nil {
				return err
			}
			pubSubQueue <- t
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (r *RedisPubSub[T, M]) Close() error {
	return (*r.redisClient).Close()
}

func encodeMessage[T any](msg T) ([]byte, error) {
	return json.Marshal(msg)
}

func decodeMessage[T any](data string, out *T) error {
	return json.Unmarshal([]byte(data), out)
}
