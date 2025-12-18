package utils

import "sync"

// Lazy provides lazy initialization with memoization.
// Similar to sync.OnceValue, but accepts the function at call time rather than initialization,
// allowing the caller to pass context or other parameters.
//
// GetOrInit is safe for concurrent use. Only the first call executes f, and all callers
// receive the same result. Concurrent callers block until f completes.
//
// Implemented as described here: https://goperf.dev/01-common-patterns/lazy-init/
type Lazy[T any] struct {
	once  sync.Once
	value T
}

// GetOrInit returns the stored value, initializing it with f on the first call.
// Subsequent calls return the cached value without calling f.
// Concurrent calls block until the first call to f completes.
func (l *Lazy[T]) GetOrInit(f func() T) T {
	l.once.Do(func() {
		l.value = f()
	})
	return l.value
}
