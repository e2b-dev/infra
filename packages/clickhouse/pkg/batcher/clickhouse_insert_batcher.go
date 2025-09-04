package batcher

import (
	"context"
)

type ClickhouseInsertBatcher[T any] interface {
	Push(event T) error
	Close(ctx context.Context) error
}
