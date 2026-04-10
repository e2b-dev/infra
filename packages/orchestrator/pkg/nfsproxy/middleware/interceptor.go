package middleware

import "context"

// Interceptor wraps an operation, calling next() to proceed to the next interceptor or the actual operation.
type Interceptor func(ctx context.Context, op string, args []any, next func(context.Context) error) error

// Chain holds the interceptor stack.
type Chain struct {
	interceptors []Interceptor
}

// NewChain creates a new interceptor chain.
func NewChain(interceptors ...Interceptor) *Chain {
	return &Chain{interceptors: interceptors}
}

// Exec runs the operation through all interceptors.
func (c *Chain) Exec(ctx context.Context, op string, args []any, fn func(context.Context) error) error {
	wrapped := fn
	for i := len(c.interceptors) - 1; i >= 0; i-- {
		interceptor := c.interceptors[i]
		inner := wrapped
		wrapped = func(ctx context.Context) error {
			return interceptor(ctx, op, args, inner)
		}
	}

	return wrapped(ctx)
}
