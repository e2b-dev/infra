package events

import (
	"context"
)

type EventService[T any] interface {
	HandleEvent(ctx context.Context, event T)
	Close(ctx context.Context) error
}
