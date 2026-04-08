package auth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
)

const (
	SecurityErrPrefix  = "error in openapi3filter.SecurityRequirementsError: security requirements failed: "
	ForbiddenErrPrefix = "team forbidden: "
	BlockedErrPrefix   = "team blocked: "
)

// MultiErrorHandler handles wrapped SecurityRequirementsError, so there are no multiple errors returned to the user.
func MultiErrorHandler(me openapi3.MultiError) error {
	if len(me) == 0 {
		return nil
	}
	err := me[0]

	// Recreate logic from oapi-codegen/gin-middleware to handle the error
	// Source: https://github.com/oapi-codegen/gin-middleware/blob/main/oapi_validate.go
	switch e := err.(type) { //nolint:errorlint  // we copied this and don't want it to change
	case *openapi3filter.RequestError:
		// We've got a bad request
		// Split up the verbose error by lines and return the first one
		// openapi errors seem to be multi-line with a decent message on the first
		errorLines := strings.Split(e.Error(), "\n")

		return fmt.Errorf("error in openapi3filter.RequestError: %s", errorLines[0])
	case *openapi3filter.SecurityRequirementsError:
		return processCustomErrors(e) // custom implementation
	default:
		// This should never happen today, but if our upstream code changes,
		// we don't want to crash the server, so handle the unexpected error.
		return fmt.Errorf("error validating request: %w", err)
	}
}

func processCustomErrors(e *openapi3filter.SecurityRequirementsError) error {
	// Return only one security requirement error (there may be multiple securitySchemes)
	unwrapped := e.Errors
	err := unwrapped[0]

	var teamForbidden *TeamForbiddenError
	var teamBlocked *TeamBlockedError
	// Return only the first non-missing authorization header error (if possible)
	for _, errW := range unwrapped {
		if errors.Is(errW, ErrNoAuthHeader) {
			continue
		}

		if errors.As(errW, &teamForbidden) {
			return fmt.Errorf("%s%s", ForbiddenErrPrefix, err.Error())
		}

		if errors.As(errW, &teamBlocked) {
			return fmt.Errorf("%s%s", BlockedErrPrefix, err.Error())
		}

		err = errW

		break
	}

	return fmt.Errorf("%s%s", SecurityErrPrefix, err.Error())
}
