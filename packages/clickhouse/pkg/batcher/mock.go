package batcher

import (
	"context"
)

type NoopBatcher[T any] struct{}

func NewNoopBatcher[T any]() *NoopBatcher[T] {
	return &NoopBatcher[T]{}
}

func (m *NoopBatcher[T]) Push(event T) error {
	return nil
}

func (m *NoopBatcher[T]) Close(ctx context.Context) error {
	return nil
}
