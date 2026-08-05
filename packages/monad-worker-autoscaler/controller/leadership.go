package controller

import "context"

// Leadership is deliberately narrower than a scaling target. Implementations
// may coordinate observation, but the controller has no mutation interface.
type Leadership interface {
	Observe(ctx context.Context) (bool, error)
	Close(ctx context.Context) error
}
