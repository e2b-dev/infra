package placement

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

const clientMessagePrefix = "Failed to place sandbox"

// Codes whose status messages carry no internal detail and are safe to forward.
var safeCreateCodes = map[codes.Code]struct{}{
	codes.FailedPrecondition: {},
	codes.InvalidArgument:    {},
}

// ClientMessage renders the customer-facing message for a PlaceSandbox error;
// the "Failed to place sandbox" prefix is load-bearing (clients may match on it).
func ClientMessage(err error) string {
	var (
		timeoutErr    PlacementTimeoutError
		noNodesErr    NoNodesAvailableError
		noEligibleErr FailedToPlaceSandboxError
		createErr     SandboxCreateError
	)

	switch {
	case errors.As(err, &timeoutErr):
		if timeoutErr.Attempts == 0 {
			return clientMessagePrefix + ": placement timed out, please retry"
		}

		return fmt.Sprintf("%s: placement timed out after %d attempt(s), please retry", clientMessagePrefix, timeoutErr.Attempts)
	case errors.As(err, &noNodesErr):
		return clientMessagePrefix + ": not enough capacity for the requested resources right now, please retry shortly"
	case errors.As(err, &noEligibleErr):
		return clientMessagePrefix + ": no compatible node for this template's requirements"
	case errors.As(err, &createErr):
		if st, ok := status.FromError(createErr.LastErr); ok {
			if _, safe := safeCreateCodes[st.Code()]; safe {
				return fmt.Sprintf("%s: %s", clientMessagePrefix, st.Message())
			}
		}

		return fmt.Sprintf("%s: sandbox creation failed on %d node(s), please retry; if the problem persists, contact us", clientMessagePrefix, createErr.Attempts)
	default:
		return clientMessagePrefix
	}
}
