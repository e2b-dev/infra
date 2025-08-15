package pubsub

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RedisPubSub[T any] struct {
	redisClient *redis.UniversalClient
	redisPubSub *redis.PubSub
	queueName   string
}

func NewRedisPubSub[T any](ctx context.Context, redisClient *redis.UniversalClient, queueName string) *RedisPubSub[T] {
	return &RedisPubSub[T]{
		redisClient: redisClient,
		queueName:   queueName,
	}
}

func (r *RedisPubSub[T]) Publish(ctx context.Context, payload T) error {
	if r.redisClient == nil {
		return nil
	}

	data, err := encodeMessage(payload)
	if err != nil {
		return err
	}

	return (*r.redisClient).Publish(ctx, r.queueName, data).Err()
}

func (r *RedisPubSub[T]) Subscribe(ctx context.Context, pubSubQueue chan<- T) error {
	if r.redisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	r.redisPubSub = (*r.redisClient).Subscribe(ctx, r.queueName)
	ch := r.redisPubSub.Channel()

	// Loop forever until the context is done, receiving messages and sending them to pubSubQueue
	for {
		select {
		case msg := <-ch:
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

func (r *RedisPubSub[T]) Close() error {
	return (*r.redisClient).Close()
}

func encodeMessage[T any](msg T) ([]byte, error) {
	return json.Marshal(msg)
}

func decodeMessage[T any](data string, out *T) error {
	return json.Unmarshal([]byte(data), out)
}
