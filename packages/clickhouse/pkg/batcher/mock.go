package batcher

import (
	"context"
)

type NoopBatcher[T any] struct{}

func NewNoopBatcher[T any]() *NoopBatcher[T] {
	return &NoopBatcher[T]{}
}

func (m *NoopBatcher[T]) Push(T) error {
	return nil
}

func (m *NoopBatcher[T]) Close(context.Context) error {
	return nil
}
