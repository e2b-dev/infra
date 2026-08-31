package orchestrator

import (
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/placement"
)

// The "Failed to place sandbox" prefix is load-bearing (clients may match on it).
const placementMessagePrefix = "Failed to place sandbox"

// Semantic error codes emitted in APIError.ErrorCode for placement failures.
// error_code is an open string set on the wire (see the generated API type); these
// name the codes this package emits so the supported set has one source of truth.
const (
	errCodePlacementTimeout    = "sandbox_placement_timeout"
	errCodeCapacityUnavailable = "sandbox_capacity_unavailable"
	errCodeNoCompatibleNode    = "sandbox_no_compatible_node"
	errCodeFeatureUnsupported  = "sandbox_feature_unsupported"
	errCodeSandboxCreateFailed = "sandbox_create_failed"
	errCodeInternalServer      = "internal_server_error"
)

// PlacementErrorCodes is every semantic error code placementAPIError can return.
var PlacementErrorCodes = []string{
	errCodePlacementTimeout,
	errCodeCapacityUnavailable,
	errCodeNoCompatibleNode,
	errCodeFeatureUnsupported,
	errCodeSandboxCreateFailed,
	errCodeInternalServer,
}

// Codes whose status messages carry no internal detail and are safe to forward.
var safePlacementCreateCodes = map[codes.Code]struct{}{
	codes.FailedPrecondition: {},
	codes.InvalidArgument:    {},
}

// placementAPIError translates a typed PlaceSandbox error into the
// customer-facing HTTP status, semantic error code and message.
func placementAPIError(err error) *api.APIError {
	apiErr := func(httpCode int, errorCode string, message string) *api.APIError {
		return &api.APIError{
			Code:      httpCode,
			ErrorCode: errorCode,
			ClientMsg: message,
			Err:       fmt.Errorf("failed to place sandbox: %w", err),
		}
	}

	var (
		timeoutErr     placement.PlacementTimeoutError
		noNodesErr     placement.NoNodesAvailableError
		noEligibleErr  placement.FailedToPlaceSandboxError
		unsupportedErr placement.UnsupportedFeatureError
		createErr      placement.SandboxCreateError
	)

	switch {
	case errors.As(err, &timeoutErr):
		if timeoutErr.Attempts == 0 {
			return apiErr(http.StatusGatewayTimeout, errCodePlacementTimeout, placementMessagePrefix+": placement timed out, please retry")
		}

		return apiErr(http.StatusGatewayTimeout, errCodePlacementTimeout, fmt.Sprintf("%s: placement timed out after %d attempt(s), please retry", placementMessagePrefix, timeoutErr.Attempts))
	case errors.As(err, &noNodesErr):
		return apiErr(http.StatusServiceUnavailable, errCodeCapacityUnavailable, placementMessagePrefix+": not enough capacity for the requested resources right now, please retry shortly")
	case errors.As(err, &unsupportedErr):
		// Same status as the sibling refusals; the error code carries the
		// distinction, and the message offers no retry because none helps.
		return apiErr(http.StatusServiceUnavailable, errCodeFeatureUnsupported, fmt.Sprintf("%s: no available orchestrator at version %s or above for a feature the request asked for", placementMessagePrefix, unsupportedErr.MinVersion))
	case errors.As(err, &noEligibleErr):
		return apiErr(http.StatusServiceUnavailable, errCodeNoCompatibleNode, placementMessagePrefix+": no compatible node for this template's requirements")
	case errors.As(err, &createErr):
		if st, ok := status.FromError(createErr.LastErr); ok {
			if _, safe := safePlacementCreateCodes[st.Code()]; safe {
				return apiErr(http.StatusInternalServerError, errCodeSandboxCreateFailed, fmt.Sprintf("%s: %s", placementMessagePrefix, st.Message()))
			}
		}

		return apiErr(http.StatusInternalServerError, errCodeSandboxCreateFailed, fmt.Sprintf("%s: sandbox creation failed on %d node(s), please retry; if the problem persists, contact us", placementMessagePrefix, createErr.Attempts))
	default:
		return apiErr(http.StatusInternalServerError, errCodeInternalServer, placementMessagePrefix)
	}
}
