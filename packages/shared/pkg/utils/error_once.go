package utils

import "context"

// ErrorOnce is a wrapper around SetOnce that can only be set with an error.
// It's useful for cases where you only need to signal completion with a potential error,
// without carrying any value.
type ErrorOnce struct {
	setOnce *SetOnce[struct{}]
}

// NewErrorOnce creates a new ErrorOnce instance.
func NewErrorOnce() *ErrorOnce {
	return &ErrorOnce{
		setOnce: NewSetOnce[struct{}](),
	}
}

// SetError sets the error once. Subsequent calls will return ErrAlreadySet.
func (e *ErrorOnce) SetError(err error) error {
	return e.setOnce.SetError(err)
}

// SetSuccess marks the operation as completed successfully (no error).
// This is equivalent to SetError(nil).
func (e *ErrorOnce) SetSuccess() error {
	return e.setOnce.SetError(nil)
}

// Wait blocks until an error is set and returns it.
// Returns nil if the operation completed successfully.
func (e *ErrorOnce) Wait() error {
	_, err := e.setOnce.Wait()
	return err
}

// Error returns the error if one has been set, or ErrNotSet if not set yet.
// Unlike Wait, this doesn't block.
func (e *ErrorOnce) Error() error {
	_, err := e.setOnce.Result()
	return err
}

// WaitWithContext waits for an error to be set with context cancellation support.
// Returns the set error or ctx.Err() if the context is cancelled first.
func (e *ErrorOnce) WaitWithContext(ctx context.Context) error {
	_, err := e.setOnce.WaitWithContext(ctx)
	return err
}

// Done returns a channel that's closed when an error is set.
// This allows using ErrorOnce in select statements.
func (e *ErrorOnce) Done() <-chan struct{} {
	return e.setOnce.Done
}
