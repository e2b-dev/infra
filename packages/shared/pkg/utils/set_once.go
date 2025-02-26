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
	setDone func()
	done    chan struct{}
	res     *result[T]
	mux     sync.RWMutex
}

func NewSetOnce[T any]() *SetOnce[T] {
	done := make(chan struct{})

	return &SetOnce[T]{
		done: done,
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

// SetResult internal method for setting the result only once.
func (s *SetOnce[T]) setResult(r result[T]) error {
	// Should do the action only once
	defer s.setDone()

	select {
	case <-s.done:
		return fmt.Errorf("value already set")
	default:
		// not set yet, so try to set it
		s.mux.Lock()
		defer s.mux.Unlock()

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
	<-s.done

	s.mux.RLock()
	defer s.mux.RUnlock()

	return s.res.value, s.res.err
}

// WaitWithContext TODO: Use waitWithContext in all places instead of Wait.
func (s *SetOnce[T]) WaitWithContext(ctx context.Context) (T, error) {
	select {
	case <-s.done:
		s.mux.RLock()
		defer s.mux.RUnlock()

		return s.res.value, s.res.err
	case <-ctx.Done():
		var zero T

		return zero, ctx.Err()
	}
}
