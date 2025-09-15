package events

import (
	"context"
)

// MockEventService implements EventsService interface for testing
type MockEventService[T any] struct {
	HandleEventFunc func(ctx context.Context, event T) error
	CloseFunc       func(ctx context.Context) error
}

func NewMockEventsService[T any]() *MockEventService[T] {
	return &MockEventService[T]{
		HandleEventFunc: func(ctx context.Context, event T) error { return nil },
		CloseFunc:       func(ctx context.Context) error { return nil },
	}
}

func (m *MockEventService[T]) HandleEvent(ctx context.Context, event T) error {
	return m.HandleEventFunc(ctx, event)
}

func (m *MockEventService[T]) Close(ctx context.Context) error {
	return m.CloseFunc(ctx)
}
