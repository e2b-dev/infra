package utils

import (
	"fmt"
	"sync"
)

type result[T any] struct {
	value T
	err   error
}

type SetOnce[T any] struct {
	wait func() (T, error)
	done chan result[T]
}

func NewSetOnce[T any]() *SetOnce[T] {
	done := make(chan result[T], 1)

	return &SetOnce[T]{
		done: done,
		wait: sync.OnceValues(func() (T, error) {
			result, ok := <-done
			if !ok {
				return *new(T), fmt.Errorf("init channel was closed")
			}

			return result.value, result.err
		}),
	}
}

// Wait returns the value or error set by SetValue or SetError.
// It can be called multiple times, returning the same value or error.
func (o *SetOnce[T]) Wait() (T, error) {
	return o.wait()
}

func (o *SetOnce[T]) SetValue(value T) error {
	select {
	case o.done <- result[T]{value: value}:
		return nil
	default:
		return fmt.Errorf("error setting value: init channel was closed")
	}
}

func (o *SetOnce[T]) SetError(err error) error {
	select {
	case o.done <- result[T]{err: err}:
		return nil
	default:
		return fmt.Errorf("error setting error: init channel was closed")
	}
}
