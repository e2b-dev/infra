package pubsub

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RedisPubSub[PayloadT, SubMetadataT any] struct {
	redisClient *redis.UniversalClient
	queueName   string
}

func NewRedisPubSub[PayloadT, SubMetadataT any](redisClient *redis.UniversalClient, queueName string) *RedisPubSub[PayloadT, SubMetadataT] {
	return &RedisPubSub[PayloadT, SubMetadataT]{
		redisClient: redisClient,
		queueName:   queueName,
	}
}

func (r *RedisPubSub[PayloadT, SubMetadataT]) ShouldPublish(ctx context.Context, key string) (bool, error) {
	if r.redisClient == nil {
		return false, fmt.Errorf("redis client is not initialized")
	}
	exists, err := (*r.redisClient).Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

func (r *RedisPubSub[PayloadT, SubMetadataT]) GetSubscriptionMetaData(ctx context.Context, key string) (*SubMetadataT, error) {
	if r.redisClient == nil {
		return nil, fmt.Errorf("redis client is not initialized")
	}
	var m SubMetadataT
	err := (*r.redisClient).Get(ctx, key).Scan(&m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *RedisPubSub[PayloadT, SubMetadataT]) SetSubscriptionMetaData(ctx context.Context, key string, metaData SubMetadataT) error {
	if r.redisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}
	return (*r.redisClient).Set(ctx, key, metaData, 0).Err()
}

func (r *RedisPubSub[PayloadT, SubMetadataT]) Publish(ctx context.Context, payload PayloadT) error {
	if r.redisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	data, err := encodeMessage(payload)
	if err != nil {
		return err
	}

	return (*r.redisClient).Publish(ctx, r.queueName, data).Err()
}

func (r *RedisPubSub[PayloadT, SubMetadataT]) Subscribe(ctx context.Context, pubSubQueue chan<- PayloadT) error {
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
			var t PayloadT
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

func (r *RedisPubSub[PayloadT, SubMetadataT]) Close() error {
	return (*r.redisClient).Close()
}

func encodeMessage[PayloadT any](msg PayloadT) ([]byte, error) {
	return json.Marshal(msg)
}

func decodeMessage[PayloadT any](data string, out *PayloadT) error {
	return json.Unmarshal([]byte(data), out)
}
