package placement

import (
	"fmt"
)

// NoNodesAvailableError reports no capacity: no candidate node at all, or
// nothing but ResourceExhausted refusals until the deadline.
type NoNodesAvailableError struct{}

var _ error = NoNodesAvailableError{}

func (NoNodesAvailableError) Error() string {
	return "no nodes available"
}

// PlacementTimeoutError is returned when the request context expires while
// placing; Attempts counts the completed create attempts.
type PlacementTimeoutError struct {
	Attempts int
}

var _ error = PlacementTimeoutError{}

func (e PlacementTimeoutError) Error() string {
	return fmt.Sprintf("request timed out after %d placement attempt(s)", e.Attempts)
}

// SandboxCreateError is returned when every placement attempt failed; LastErr
// carries the last non-ResourceExhausted create error.
type SandboxCreateError struct {
	Attempts int
	LastErr  error
}

var _ error = SandboxCreateError{}

func (e SandboxCreateError) Error() string {
	return fmt.Sprintf("failed to create a new sandbox after %d attempt(s): %v", e.Attempts, e.LastErr)
}

func (e SandboxCreateError) Unwrap() error {
	return e.LastErr
}
