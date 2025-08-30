package utils

import (
	"context"
	"fmt"
	"sync"
)

type NotSetError struct{}

func (e NotSetError) Error() string {
	return "value not set"
}

type result[T any] struct {
	value T
	err   error
}

type SetOnce[T any] struct {
	setDone func()
	// Don't close the channel from outside, it's used to signal that the value is set.
	Done chan struct{}
	res  *result[T]
	mu   sync.RWMutex
}

func NewSetOnce[T any]() *SetOnce[T] {
	done := make(chan struct{})

	return &SetOnce[T]{
		Done: done,
		setDone: sync.OnceFunc(func() {
			close(done)
		}),
	}
}

func (s *SetOnce[T]) SetValue(value T) error {
	return s.setResult(result[T]{value: value})
}

func (s *SetOnce[T]) SetError(err error) error {
	return s.setResult(result[T]{err: err})
}

var ErrAlreadySet = fmt.Errorf("value already set")

// SetResult internal method for setting the result only once.
func (s *SetOnce[T]) setResult(r result[T]) error {
	// Should do the action only once
	defer s.setDone()

	select {
	case <-s.Done:
		return ErrAlreadySet
	default:
		// not set yet, so try to set it
		s.mu.Lock()
		defer s.mu.Unlock()

		if s.res != nil {
			return fmt.Errorf("value already set")
		}

		s.res = &r

		return nil
	}
}

// Wait returns the value or error set by SetValue or SetError.
// It can be called multiple times, returning the same value or error.
func (s *SetOnce[T]) Wait() (T, error) {
	<-s.Done

	return s.Result()
}

// Result returns the value or error set by SetValue or SetError.
// It can be called multiple times, returning the same value or error.
// If called before the value is set, it will return the zero value and NotSetError error.
func (s *SetOnce[T]) Result() (T, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.res == nil {
		var zero T

		return zero, NotSetError{}
	}

	return s.res.value, s.res.err
}

// WaitWithContext TODO: Use waitWithContext in all places instead of Wait.
func (s *SetOnce[T]) WaitWithContext(ctx context.Context) (T, error) {
	select {
	case <-s.Done:
		return s.Result()
	case <-ctx.Done():
		var zero T

		return zero, ctx.Err()
	}
}
