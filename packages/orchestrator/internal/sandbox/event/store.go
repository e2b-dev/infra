package event

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
)

type SandboxEvent struct {
	Path string         `json:"path"`
	Body map[string]any `json:"body"`
}

func (i SandboxEvent) MarshalBinary() ([]byte, error) {
	return json.Marshal(i)
}

type sandboxEventStore struct {
	ctx         context.Context
	tracer      trace.Tracer
	redisClient redis.UniversalClient
}

type SandboxEventStore interface {
	GetEvent(sandboxId string) (*SandboxEvent, error)
	SetEvent(sandboxId string, SandboxEvent *SandboxEvent, expiration time.Duration) error
	DelEvent(sandboxId string) error
	Close() error
}

func NewSandboxEventStore(ctx context.Context, tracer trace.Tracer, redisClient redis.UniversalClient) SandboxEventStore {
	return &sandboxEventStore{
		ctx:         ctx,
		tracer:      tracer,
		redisClient: redisClient,
	}
}

func (c *sandboxEventStore) GetEvent(sandboxId string) (*SandboxEvent, error) {
	_, span := c.tracer.Start(c.ctx, "sandbox-event-get")
	defer span.End()

	rawEvent, err := c.redisClient.Get(c.ctx, sandboxId).Result()
	if err != nil {
		return nil, err
	}

	var event SandboxEvent
	err = json.Unmarshal([]byte(rawEvent), &event)
	if err != nil {
		return nil, err
	}
	return &event, nil
}

func (c *sandboxEventStore) SetEvent(sandboxId string, event *SandboxEvent, expiration time.Duration) error {
	_, span := c.tracer.Start(c.ctx, "sandbox-event-store")
	defer span.End()

	return c.redisClient.Set(c.ctx, sandboxId, event, expiration).Err()
}

func (c *sandboxEventStore) DelEvent(sandboxId string) error {
	_, span := c.tracer.Start(c.ctx, "sandbox-event-delete")
	defer span.End()

	return c.redisClient.Del(c.ctx, sandboxId).Err()
}

func (c *sandboxEventStore) Close() error {
	return c.redisClient.Close()
}
