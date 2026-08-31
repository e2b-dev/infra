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

// UnsupportedFeatureError reports that no orchestrator in the cluster is new
// enough for a capability the request asked for; separate from
// NoNodesAvailableError because no retry can succeed.
type UnsupportedFeatureError struct {
	Features   []string
	MinVersion string
}

var _ error = UnsupportedFeatureError{}

func (e UnsupportedFeatureError) Error() string {
	return fmt.Sprintf("no available orchestrator at version %s or above for feature(s) %v", e.MinVersion, e.Features)
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
