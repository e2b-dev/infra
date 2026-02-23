package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// AuthorizationHeaderMissingError is returned when the authorization header is missing.
type AuthorizationHeaderMissingError struct{}

func (e *AuthorizationHeaderMissingError) Error() string {
	return "authorization header is missing"
}

var (
	ErrNoAuthHeader      = &AuthorizationHeaderMissingError{}
	ErrInvalidAuthHeader = errors.New("authorization header is malformed")
)

// HeaderKey describes how to extract an authentication token from an HTTP request header.
type HeaderKey struct {
	Name         string
	Prefix       string
	RemovePrefix string
}

// Authenticator is implemented by types that can authenticate requests against a security scheme.
type Authenticator interface {
	Authenticate(ctx context.Context, ginCtx *gin.Context, input *openapi3filter.AuthenticationInput) error
	SecuritySchemeName() string
}

// CommonAuthenticator implements Authenticator using a header-based token with a pluggable validation function.
type CommonAuthenticator[T any] struct {
	SchemeName     string
	Header         HeaderKey
	ValidationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (T, *APIError)
	ContextKey     string
	ErrorMessage   string
}

// GetHeaderKeysFromRequest extracts the token from the request header.
func (a *CommonAuthenticator[T]) GetHeaderKeysFromRequest(req *http.Request) (string, error) {
	key := req.Header.Get(a.Header.Name)
	if key == "" {
		return "", ErrNoAuthHeader
	}

	if a.Header.RemovePrefix != "" {
		key = strings.TrimSpace(strings.TrimPrefix(key, a.Header.RemovePrefix))
	}

	if a.Header.Prefix != "" && !strings.HasPrefix(key, a.Header.Prefix) {
		return "", ErrInvalidAuthHeader
	}

	return key, nil
}

// Authenticate validates the request against the security scheme.
func (a *CommonAuthenticator[T]) Authenticate(ctx context.Context, ginCtx *gin.Context, input *openapi3filter.AuthenticationInput) error {
	headerKey, err := a.GetHeaderKeysFromRequest(input.RequestValidationInput.Request)
	if err != nil {
		telemetry.ReportError(ctx,
			"authorization header is missing",
			err,
			attribute.String("error.message", a.ErrorMessage),
		)

		ginCtx.Status(http.StatusUnauthorized)

		return err
	}

	telemetry.ReportEvent(ctx, "api key extracted")

	result, validationError := a.ValidationFunc(ctx, ginCtx, headerKey)
	if validationError != nil {
		telemetry.ReportError(ctx,
			"validation error",
			validationError.Err,
			attribute.String("error.message", a.ErrorMessage),
			attribute.Int("http.status_code", validationError.Code),
			attribute.String("http.status_text", http.StatusText(validationError.Code)),
		)

		ginCtx.Status(validationError.Code)

		var forbiddenError *TeamForbiddenError
		if errors.As(validationError.Err, &forbiddenError) {
			return fmt.Errorf("forbidden: %w", validationError.Err)
		}

		var blockedError *TeamBlockedError
		if errors.As(validationError.Err, &blockedError) {
			return fmt.Errorf("blocked: %w", validationError.Err)
		}

		return fmt.Errorf("%s\n%s (%w)", a.ErrorMessage, validationError.ClientMsg, validationError.Err)
	}

	telemetry.ReportEvent(ctx, "api key validated")

	if a.ContextKey != "" {
		ginCtx.Set(a.ContextKey, result)
	}

	return nil
}

// SecuritySchemeName returns the name of the security scheme this authenticator handles.
func (a *CommonAuthenticator[T]) SecuritySchemeName() string {
	return a.SchemeName
}
