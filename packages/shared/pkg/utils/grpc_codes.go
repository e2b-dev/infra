package utils

import (
	"net/http"

	"google.golang.org/grpc/codes"
)

// GRPCCodeFromHTTPStatus maps an HTTP status code to the closest matching gRPC
// codes.Code.
func GRPCCodeFromHTTPStatus(statusCode int) codes.Code {
	switch statusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return codes.InvalidArgument
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusConflict:
		return codes.AlreadyExists
	case http.StatusTooManyRequests:
		return codes.ResourceExhausted
	case http.StatusPreconditionFailed:
		return codes.FailedPrecondition
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return codes.DeadlineExceeded
	case http.StatusNotImplemented:
		return codes.Unimplemented
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return codes.Unavailable
	default:
		if statusCode >= http.StatusInternalServerError {
			return codes.Internal
		}
		if statusCode >= http.StatusBadRequest {
			return codes.InvalidArgument
		}

		return codes.Internal
	}
}
