package events

import (
	"context"
)

type EventsService[T any] interface {
	HandleEvent(ctx context.Context, event T) error
	Close(ctx context.Context) error
}
