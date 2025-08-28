package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStreams[PayloadT, SubMetaDataT any] struct {
	redisClient  redis.UniversalClient
	streamName   string
	groupName    string
	consumerName string
}

func NewRedisStreams[PayloadT, SubMetaDataT any](redisClient redis.UniversalClient, streamName, groupName, consumerName string) *RedisStreams[PayloadT, SubMetaDataT] {
	return &RedisStreams[PayloadT, SubMetaDataT]{
		redisClient:  redisClient,
		streamName:   streamName,
		groupName:    groupName,
		consumerName: consumerName,
	}
}

func (r *RedisStreams[PayloadT, SubMetaDataT]) ShouldPublish(ctx context.Context, key string) (bool, error) {
	if r.redisClient == nil {
		return false, fmt.Errorf("redis client is not initialized")
	}
	exists, err := r.redisClient.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

func (r *RedisStreams[PayloadT, SubMetaDataT]) GetSubMetaData(ctx context.Context, key string) (SubMetaDataT, error) {
	var metadata SubMetaDataT
	if r.redisClient == nil {
		return metadata, fmt.Errorf("redis client is not initialized")
	}
	metaDataRaw, err := r.redisClient.Get(ctx, key).Result()
	if err != nil {
		return metadata, err
	}

	err = decodeMetaData(metaDataRaw, &metadata)
	if err != nil {
		return metadata, err
	}
	return metadata, nil
}

func (r *RedisStreams[PayloadT, SubMetaDataT]) SetSubMetaData(ctx context.Context, key string, metaData SubMetaDataT) error {
	if r.redisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	data, err := encodeMetaData(metaData)
	if err != nil {
		return err
	}

	return (r.redisClient).Set(ctx, key, data, 0).Err()
}

func (r *RedisStreams[PayloadT, SubMetaDataT]) DeleteSubMetaData(ctx context.Context, key string) error {
	if r.redisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}
	return (r.redisClient).Del(ctx, key).Err()
}

func (r *RedisStreams[PayloadT, SubMetaDataT]) Publish(ctx context.Context, payload PayloadT) error {
	if r.redisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	mapData, err := structToMapJSON(payload)
	if err != nil {
		return err
	}

	// Use XADD to add entry to stream with auto-generated ID
	_, err = r.redisClient.XAdd(ctx, &redis.XAddArgs{
		Stream: r.streamName,
		ID:     "*", // Auto-generate ID
		Values: mapData,
	}).Result()

	return err
}

func (r *RedisStreams[PayloadT, SubMetaDataT]) Subscribe(ctx context.Context, pubSubQueue chan<- PayloadT) error {
	if r.redisClient == nil {
		return fmt.Errorf("redis client is not initialized")
	}

	// Ensure consumer group exists
	err := r.ensureConsumerGroup(ctx)
	if err != nil {
		return fmt.Errorf("failed to ensure consumer group: %w", err)
	}

	// Start consuming messages from the stream
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Read pending messages first
			pending, err := r.redisClient.XPending(ctx, r.streamName, r.groupName).Result()
			if err != nil {
				return fmt.Errorf("failed to get pending messages: %w", err)
			}

			if pending.Count > 0 {
				// Process pending messages
				err = r.processPendingMessages(ctx, pubSubQueue)
				if err != nil {
					return fmt.Errorf("failed to process pending messages: %w", err)
				}
			}

			// Read new messages with blocking
			err = r.readNewMessages(ctx, pubSubQueue)
			if err != nil {
				return fmt.Errorf("failed to read new messages: %w", err)
			}
		}
	}
}

func (r *RedisStreams[PayloadT, SubMetaDataT]) ensureConsumerGroup(ctx context.Context) error {
	// Check if group exists
	groups, err := r.redisClient.XInfoGroups(ctx, r.streamName).Result()
	if err != nil {
		// Stream doesn't exist, create it with the group
		_, err = r.redisClient.XGroupCreate(ctx, r.streamName, r.groupName, "0").Result()
		return err
	}

	// Check if our group exists
	for _, group := range groups {
		if group.Name == r.groupName {
			return nil
		}
	}

	// Group doesn't exist, create it
	_, err = r.redisClient.XGroupCreate(ctx, r.streamName, r.groupName, "0").Result()
	return err
}

func (r *RedisStreams[PayloadT, SubMetaDataT]) processPendingMessages(ctx context.Context, pubSubQueue chan<- PayloadT) error {
	// Get pending messages for this consumer
	pending, err := r.redisClient.XPending(ctx, r.streamName, r.groupName).Result()
	if err != nil {
		return err
	}

	if pending.Count == 0 {
		return nil
	}

	// Get pending message details
	pendingMsgs, err := r.redisClient.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: r.streamName,
		Group:  r.groupName,
		Start:  "-",
		End:    "+",
		Count:  pending.Count,
	}).Result()
	if err != nil {
		return err
	}

	if len(pendingMsgs) == 0 {
		return nil
	}

	// Claim pending messages that have been idle for more than 1 minute
	var messageIDs []string
	for _, msg := range pendingMsgs {
		messageIDs = append(messageIDs, msg.ID)
	}

	claimed, err := r.redisClient.XClaim(ctx, &redis.XClaimArgs{
		Stream:   r.streamName,
		Group:    r.groupName,
		Consumer: r.consumerName,
		MinIdle:  time.Minute,
		Messages: messageIDs,
	}).Result()
	if err != nil {
		return err
	}

	// Process claimed messages
	for _, msg := range claimed {
		err = r.processMessage(msg, pubSubQueue)
		if err != nil {
			return err
		}
		// Acknowledge the message
		_, err = r.redisClient.XAck(ctx, r.streamName, r.groupName, msg.ID).Result()
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *RedisStreams[PayloadT, SubMetaDataT]) readNewMessages(ctx context.Context, pubSubQueue chan<- PayloadT) error {
	// Read new messages with blocking
	streams, err := r.redisClient.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    r.groupName,
		Consumer: r.consumerName,
		Streams:  []string{r.streamName, ">"}, // ">" means only new messages
		Count:    1,
		Block:    time.Second, // Block for 1 second
	}).Result()
	if err != nil {
		if err == redis.Nil {
			// No messages, continue
			return nil
		}
		return err
	}

	// Process messages
	for _, stream := range streams {
		for _, msg := range stream.Messages {
			err = r.processMessage(msg, pubSubQueue)
			if err != nil {
				return err
			}
			// Acknowledge the message
			_, err = r.redisClient.XAck(ctx, r.streamName, r.groupName, msg.ID).Result()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *RedisStreams[PayloadT, SubMetaDataT]) processMessage(msg redis.XMessage, pubSubQueue chan<- PayloadT) error {
	// Extract payload from message
	payloadStr, ok := msg.Values["payload"].(string)
	if !ok {
		return fmt.Errorf("invalid payload format in message %s", msg.ID)
	}

	var payload PayloadT
	err := decodePayload(payloadStr, &payload)
	if err != nil {
		return err
	}

	// Send to queue
	select {
	case pubSubQueue <- payload:
		return nil
	case <-time.After(time.Second):
		return fmt.Errorf("timeout sending message to queue")
	}
}

func (r *RedisStreams[PayloadT, SubMetaDataT]) Close(ctx context.Context) error {
	return r.redisClient.Close()
}

// Private helper functions
func structToMapJSON(obj any) (map[string]any, error) {
	var result map[string]interface{}
	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(jsonBytes, &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}
