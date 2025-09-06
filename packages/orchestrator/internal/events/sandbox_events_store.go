package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
)

const (
	cacheTL = time.Hour * 24 * 30

	EventPrefix = "ev:"
	IPPrefix    = "ip:"
)

type SandboxEvent struct {
	Path string         `json:"path"`
	Body map[string]any `json:"body"`
}

func (i SandboxEvent) MarshalBinary() ([]byte, error) {
	return json.Marshal(i)
}

type sandboxEventStore struct {
	tracer      trace.Tracer
	redisClient redis.UniversalClient
}

type SandboxEventStore interface {
	SetSandboxIP(ctx context.Context, sandboxID string, sandboxIP string) error
	GetSandboxID(ctx context.Context, sandboxIP string) (string, error)
	DelSandboxIP(ctx context.Context, sandboxIP string) error

	GetLastEvent(ctx context.Context, sandboxID string) (*SandboxEvent, error)
	GetLastNEvents(ctx context.Context, sandboxID string, n int) ([]*SandboxEvent, error)
	AddEvent(ctx context.Context, sandboxID string, SandboxEvent *SandboxEvent, expiration time.Duration) error
	DelEvent(ctx context.Context, sandboxID string) error

	Close(ctx context.Context) error
}

func NewSandboxEventStore(tracer trace.Tracer, redisClient redis.UniversalClient) SandboxEventStore {
	return &sandboxEventStore{
		tracer:      tracer,
		redisClient: redisClient,
	}
}

func (c *sandboxEventStore) SetSandboxIP(ctx context.Context, sandboxID string, sandboxIP string) error {
	return c.redisClient.Set(ctx, IPPrefix+sandboxIP, sandboxID, cacheTL).Err()
}

func (c *sandboxEventStore) GetSandboxID(ctx context.Context, sandboxIP string) (string, error) {
	return c.redisClient.Get(ctx, IPPrefix+sandboxIP).Result()
}

func (c *sandboxEventStore) DelSandboxIP(ctx context.Context, sandboxIP string) error {
	return c.redisClient.Del(ctx, IPPrefix+sandboxIP).Err()
}

func (c *sandboxEventStore) GetLastEvent(ctx context.Context, sandboxID string) (*SandboxEvent, error) {
	_, span := c.tracer.Start(ctx, "sandbox-event-get-last")
	defer span.End()

	result, err := c.redisClient.ZRevRangeWithScores(ctx, EventPrefix+sandboxID, 0, 0).Result()
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, redis.Nil
	}
	rawEvent := result[0].Member.(string)

	var event SandboxEvent
	err = json.Unmarshal([]byte(rawEvent), &event)
	if err != nil {
		return nil, err
	}
	return &event, nil
}

func (c *sandboxEventStore) GetLastNEvents(ctx context.Context, sandboxID string, n int) ([]*SandboxEvent, error) {
	_, span := c.tracer.Start(ctx, "sandbox-event-get-last-n")
	defer span.End()

	result, err := c.redisClient.ZRevRangeWithScores(ctx, EventPrefix+sandboxID, 0, int64(n-1)).Result()
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, redis.Nil
	}

	events := make([]*SandboxEvent, 0, len(result))
	for _, item := range result {
		rawEvent := item.Member.(string)
		var event SandboxEvent
		err = json.Unmarshal([]byte(rawEvent), &event)
		if err != nil {
			return nil, err
		}
		events = append(events, &event)
	}

	return events, nil
}

func (c *sandboxEventStore) AddEvent(ctx context.Context, sandboxID string, event *SandboxEvent, expiration time.Duration) error {
	_, span := c.tracer.Start(ctx, "sandbox-event-store")
	defer span.End()

	return c.redisClient.ZAdd(ctx, EventPrefix+sandboxID, redis.Z{
		Score:  float64(time.Now().UnixNano()),
		Member: event,
	}).Err()
}

func (c *sandboxEventStore) DelEvent(ctx context.Context, sandboxID string) error {
	_, span := c.tracer.Start(ctx, "sandbox-event-delete")
	defer span.End()

	return c.redisClient.Del(ctx, EventPrefix+sandboxID).Err()
}

func (c *sandboxEventStore) Close(ctx context.Context) error {
	return c.redisClient.Close()
}
