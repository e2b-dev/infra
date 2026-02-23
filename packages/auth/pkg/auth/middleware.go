package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
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

func adminValidationFunction(adminToken string) func(ctx context.Context, ginCtx *gin.Context, token string) (struct{}, *APIError) {
	return func(_ context.Context, _ *gin.Context, token string) (struct{}, *APIError) {
		if token != adminToken {
			return struct{}{}, &APIError{
				Code:      http.StatusUnauthorized,
				Err:       errors.New("invalid access token"),
				ClientMsg: "Invalid Access token.",
			}
		}

		return struct{}{}, nil
	}
}

func CreateAuthenticationFunc(
	adminToken string,
	preAuthHook func(*gin.Context),
	teamValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (*types.Team, *APIError),
	userValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *APIError),
	supabaseTokenValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *APIError),
	supabaseTeamValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (*types.Team, *APIError),
) openapi3filter.AuthenticationFunc {
	authenticators := []Authenticator{
		&CommonAuthenticator[*types.Team]{
			SchemeName: "ApiKeyAuth",
			Header: HeaderKey{
				Name:         "X-API-Key",
				Prefix:       "e2b_",
				RemovePrefix: "",
			},
			ValidationFunc: teamValidationFunction,
			ContextKey:     TeamContextKey,
			ErrorMessage:   "Invalid API key, please visit https://e2b.dev/docs/api-key for more information.",
		},
		&CommonAuthenticator[uuid.UUID]{
			SchemeName: "AccessTokenAuth",
			Header: HeaderKey{
				Name:         "Authorization",
				Prefix:       "sk_e2b_",
				RemovePrefix: "Bearer ",
			},
			ValidationFunc: userValidationFunction,
			ContextKey:     UserIDContextKey,
			ErrorMessage:   "Invalid Access token, try to login again by running `e2b auth login`.",
		},
		&CommonAuthenticator[uuid.UUID]{
			SchemeName: "Supabase1TokenAuth",
			Header: HeaderKey{
				Name:         "X-Supabase-Token",
				Prefix:       "",
				RemovePrefix: "",
			},
			ValidationFunc: supabaseTokenValidationFunction,
			ContextKey:     UserIDContextKey,
			ErrorMessage:   "Invalid Supabase token.",
		},
		&CommonAuthenticator[*types.Team]{
			SchemeName: "Supabase2TeamAuth",
			Header: HeaderKey{
				Name:         "X-Supabase-Team",
				Prefix:       "",
				RemovePrefix: "",
			},
			ValidationFunc: supabaseTeamValidationFunction,
			ContextKey:     TeamContextKey,
			ErrorMessage:   "Invalid Supabase token teamID.",
		},
		&CommonAuthenticator[struct{}]{
			SchemeName: "AdminTokenAuth",
			Header: HeaderKey{
				Name:         "X-Admin-Token",
				Prefix:       "",
				RemovePrefix: "",
			},
			ValidationFunc: adminValidationFunction(adminToken),
			ContextKey:     "",
			ErrorMessage:   "Invalid Access token.",
		},
	}

	return func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
		ginCtx := middleware.GetGinContext(ctx)

		if preAuthHook != nil {
			preAuthHook(ginCtx)
		}

		ctx, span := tracer.Start(ginCtx.Request.Context(), "authenticate")
		defer span.End()

		for _, validator := range authenticators {
			if input.SecuritySchemeName == validator.SecuritySchemeName() {
				//nolint:contextcheck // We use the gin request context here by design.
				return validator.Authenticate(ctx, ginCtx, input)
			}
		}

		return fmt.Errorf("invalid security scheme name '%s'", input.SecuritySchemeName)
	}
}
