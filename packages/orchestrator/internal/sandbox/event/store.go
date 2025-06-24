package event

import (
	"context"
	"encoding/json"
	"fmt"
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
	GetSandbox(sandboxId string) (*SandboxEvent, error)
	StoreSandbox(sandboxId string, SandboxEvent *SandboxEvent, expiration time.Duration) error
	DeleteSandbox(sandboxId string) error
	Close() error
}

func NewMemorySandboxesEvent(ctx context.Context, tracer trace.Tracer, redisClient redis.UniversalClient) SandboxEventStore {
	return &sandboxEventStore{
		ctx:         ctx,
		tracer:      tracer,
		redisClient: redisClient,
	}
}

func (c *sandboxEventStore) GetSandbox(sandboxId string) (*SandboxEvent, error) {
	_, span := c.tracer.Start(c.ctx, "sandbox-event-get")
	defer span.End()

	body, err := c.redisClient.Get(c.ctx, sandboxId).Result()
	if err != nil {
		return nil, err
	}

	return &SandboxEvent{
		Path: string(body),
		Body: make(map[string]any),
	}, nil
}

func (c *sandboxEventStore) StoreSandbox(sandboxId string, SandboxEvent *SandboxEvent, expiration time.Duration) error {
	_, span := c.tracer.Start(c.ctx, "sandbox-event-store")
	defer span.End()

	body, err := c.redisClient.Get(c.ctx, sandboxId).Result()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("sandbox event not found: %w", err)
		}
		return err
	}

	return c.redisClient.Set(c.ctx, sandboxId, body, expiration).Err()
}

func (c *sandboxEventStore) DeleteSandbox(sandboxId string) error {
	_, span := c.tracer.Start(c.ctx, "sandbox-event-delete")
	defer span.End()

	return c.redisClient.Del(c.ctx, sandboxId).Err()
}

func (c *sandboxEventStore) Close() error {
	return c.redisClient.Close()
}
