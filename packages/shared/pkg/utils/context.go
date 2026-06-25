package utils

import "context"

// WithoutCancelPreservingDeadline returns a context that ignores parent
// cancellation while retaining the parent's deadline and values.
func WithoutCancelPreservingDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	detachedCtx := context.WithoutCancel(ctx)
	deadline, ok := ctx.Deadline()
	if !ok {
		return detachedCtx, func() {}
	}

	return context.WithDeadline(detachedCtx, deadline)
}
