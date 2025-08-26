package events

import (
	"context"
)

// MockEventsService implements EventsService interface for testing
type MockEventsService[T any] struct {
	HandleEventFunc func(ctx context.Context, event T) error
	CloseFunc       func(ctx context.Context) error
}

func NewMockEventsService[T any]() *MockEventsService[T] {
	return &MockEventsService[T]{
		HandleEventFunc: func(ctx context.Context, event T) error { return nil },
		CloseFunc:       func(ctx context.Context) error { return nil },
	}
}

func (m *MockEventsService[T]) HandleEvent(ctx context.Context, event T) error {
	return m.HandleEventFunc(ctx, event)
}

func (m *MockEventsService[T]) Close(ctx context.Context) error {
	return m.CloseFunc(ctx)
}
