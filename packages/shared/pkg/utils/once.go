package utils

import (
	"sync"
)

type valueWithErr[T any] struct {
	value T
	err   error
}

type InitOnce[T any] struct {
	getResult func() (T, error)
	ready     chan valueWithErr[T]
}

func NewInitOnce[T any]() *InitOnce[T] {
	ready := make(chan valueWithErr[T], 1)

	return &InitOnce[T]{
		ready: make(chan valueWithErr[T], 1),
		getResult: sync.OnceValues(func() (T, error) {
			result := <-ready

			return result.value, result.err
		}),
	}
}

// Get returns the initialized value.
// It blocks until the value is initialized and after return the same value on each call.
func (o *InitOnce[T]) Get() (T, error) {
	return o.getResult()
}

// Set should be called exactly once by the goroutine that is initializing the value.
// You can use it to set the value directly or to set an error before you call Init().
func (o *InitOnce[T]) Set(value T, err error) {
	o.ready <- valueWithErr[T]{value, err}
}
