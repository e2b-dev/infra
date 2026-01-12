package utils

import "context"

// Promise represents an asynchronous computation that will eventually produce a value or error.
// The computation starts immediately when the promise is created.
// Multiple goroutines can safely wait on the same promise.
type Promise[T any] struct {
	result *SetOnce[T]
}

// NewPromise creates a new Promise that immediately starts executing the given function
// in a goroutine. The result (value or error) will be available via Wait.
func NewPromise[T any](fn func() (T, error)) *Promise[T] {
	p := &Promise[T]{
		result: NewSetOnce[T](),
	}

	go func() {
		value, err := fn()
		p.result.SetResult(value, err)
	}()

	return p
}

// Wait blocks until the promise is resolved and returns the result.
// Returns the value and nil error on success, or zero value and the error on failure.
// If the context is cancelled before the promise resolves, returns ctx.Err().
func (p *Promise[T]) Wait(ctx context.Context) (T, error) {
	return p.result.WaitWithContext(ctx)
}

// Done returns a channel that's closed when the promise is resolved.
// This allows using Promise in select statements.
func (p *Promise[T]) Done() <-chan struct{} {
	return p.result.Done
}

// Result returns the current result without blocking.
// Returns NotSetError if the promise hasn't resolved yet.
func (p *Promise[T]) Result() (T, error) {
	return p.result.Result()
}
