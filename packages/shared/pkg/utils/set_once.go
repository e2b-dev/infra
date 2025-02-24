package utils

import (
	"context"
	"fmt"
	"sync"
)

type result[T any] struct {
	value T
	err   error
}

type SetOnce[T any] struct {
	wait   func() (T, error)
	result chan result[T]
	done   chan struct{}
}

func NewSetOnce[T any]() *SetOnce[T] {
	result := make(chan result[T], 1)
	done := make(chan struct{})

	wait := sync.OnceValues(func() (T, error) {
		defer close(done)

		value, ok := <-result
		if !ok {
			return *new(T), fmt.Errorf("init channel was closed")
		}

		return value.value, value.err
	})

	// We need to start the wait function in a new goroutine to avoid deadlocks
	// between the wait function call and done channel close.
	go wait()

	return &SetOnce[T]{
		result: result,
		done:   done,
		wait:   wait,
	}
}

// TODO: Use waitWithContext in all places instead of Wait.
func (o *SetOnce[T]) WaitWithContext(ctx context.Context) (T, error) {
	select {
	case <-o.done:
		return o.wait()
	case <-ctx.Done():
		return *new(T), ctx.Err()
	}
}

// Wait returns the value or error set by SetValue or SetError.
// It can be called multiple times, returning the same value or error.
func (o *SetOnce[T]) Wait() (T, error) {
	return o.wait()
}

func (o *SetOnce[T]) SetValue(value T) error {
	select {
	case o.result <- result[T]{value: value}:
		return nil
	default:
		return fmt.Errorf("error setting value: init channel was closed")
	}
}

func (o *SetOnce[T]) SetError(err error) error {
	select {
	case o.result <- result[T]{err: err}:
		return nil
	default:
		return fmt.Errorf("error setting error: init channel was closed")
	}
}
