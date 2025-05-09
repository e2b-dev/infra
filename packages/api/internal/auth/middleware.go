package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var adminToken = os.Getenv("ADMIN_TOKEN")

type AuthorizationHeaderMissingError struct{}

func (e *AuthorizationHeaderMissingError) Error() string {
	return "authorization header is missing"
}

var (
	ErrNoAuthHeader      = &AuthorizationHeaderMissingError{}
	ErrInvalidAuthHeader = errors.New("authorization header is malformed")
)

type headerKey struct {
	name         string
	prefix       string
	removePrefix string
}

type commonAuthenticator[T any] struct {
	securitySchemeName string
	headerKey          headerKey
	validationFunction func(context.Context, string) (T, *api.APIError)
	contextKey         string
	errorMessage       string
}

type authenticator interface {
	Authenticate(ctx context.Context, input *openapi3filter.AuthenticationInput) error
	SecuritySchemeName() string
}

func (a *commonAuthenticator[T]) getHeaderKeysFromRequest(req *http.Request) (string, error) {
	key := req.Header.Get(a.headerKey.name)
	// Check for the Authorization header.
	if key == "" {
		return "", ErrNoAuthHeader
	}

	// Remove the prefix from the API key
	if a.headerKey.removePrefix != "" {
		key = strings.TrimSpace(strings.TrimPrefix(key, a.headerKey.removePrefix))
	}

	// We expect a header value to be in a special form
	if !strings.HasPrefix(key, a.headerKey.prefix) {
		return "", ErrInvalidAuthHeader
	}

	return key, nil
}

// Authenticate uses the specified validator to ensure an API key is valid.
func (a *commonAuthenticator[T]) Authenticate(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
	// Now, we need to get the API key from the request
	headerKey, err := a.getHeaderKeysFromRequest(input.RequestValidationInput.Request)
	if err != nil {
		telemetry.ReportCriticalError(ctx, fmt.Errorf("%v %w", a.errorMessage, err))

		return fmt.Errorf("%v %w", a.errorMessage, err)
	}

	telemetry.ReportEvent(ctx, "api key extracted")

	// If the API key is valid, we will get a result back
	result, validationError := a.validationFunction(ctx, headerKey)
	if validationError != nil {
		zap.L().Info("validation error", zap.Error(validationError.Err))
		telemetry.ReportError(ctx, fmt.Errorf("%s %w", a.errorMessage, validationError.Err))

		var forbiddenError *db.TeamForbiddenError
		if errors.As(validationError.Err, &forbiddenError) {
			return validationError.Err
		}

		return fmt.Errorf("%s\n%s (%w)", a.errorMessage, validationError.ClientMsg, validationError.Err)
	}

	telemetry.ReportEvent(ctx, "api key validated")

	// Set the property on the gin context
	if a.contextKey != "" {
		middleware.GetGinContext(ctx).Set(a.contextKey, result)
	}

	return nil
}

func (a *commonAuthenticator[T]) SecuritySchemeName() string {
	return a.securitySchemeName
}

func adminValidationFunction(_ context.Context, token string) (struct{}, *api.APIError) {
	if token != adminToken {
		return struct{}{}, &api.APIError{
			Code:      http.StatusUnauthorized,
			Err:       errors.New("invalid access token"),
			ClientMsg: "Invalid Access token.",
		}
	}

	return struct{}{}, nil
}

func CreateAuthenticationFunc(
	tracer trace.Tracer,
	teamValidationFunction func(context.Context, string) (authcache.AuthTeamInfo, *api.APIError),
	userValidationFunction func(context.Context, string) (uuid.UUID, *api.APIError),
	supabaseTokenValidationFunction func(context.Context, string) (uuid.UUID, *api.APIError),
	supabaseTeamValidationFunction func(context.Context, string) (authcache.AuthTeamInfo, *api.APIError),
) openapi3filter.AuthenticationFunc {
	authenticators := []authenticator{
		&commonAuthenticator[authcache.AuthTeamInfo]{
			securitySchemeName: "ApiKeyAuth",
			headerKey: headerKey{
				name:         "X-API-Key",
				prefix:       "e2b_",
				removePrefix: "",
			},
			validationFunction: teamValidationFunction,
			contextKey:         TeamContextKey,
			errorMessage:       "Invalid API key, please visit https://e2b.dev/docs/quickstart/api-key for more information.",
		},
		&commonAuthenticator[uuid.UUID]{
			securitySchemeName: "AccessTokenAuth",
			headerKey: headerKey{
				name:         "Authorization",
				prefix:       "sk_e2b_",
				removePrefix: "Bearer ",
			},
			validationFunction: userValidationFunction,
			contextKey:         UserIDContextKey,
			errorMessage:       "Invalid Access token, try to login again by running `e2b auth login`.",
		},
		&commonAuthenticator[uuid.UUID]{
			securitySchemeName: "Supabase1TokenAuth",
			headerKey: headerKey{
				name:         "X-Supabase-Token",
				prefix:       "",
				removePrefix: "",
			},
			validationFunction: supabaseTokenValidationFunction,
			contextKey:         UserIDContextKey,
			errorMessage:       "Invalid Supabase token.",
		},
		&commonAuthenticator[authcache.AuthTeamInfo]{
			securitySchemeName: "Supabase2TeamAuth",
			headerKey: headerKey{
				name:         "X-Supabase-Team",
				prefix:       "",
				removePrefix: "",
			},
			validationFunction: supabaseTeamValidationFunction,
			contextKey:         TeamContextKey,
			errorMessage:       "Invalid Supabase token teamID.",
		},
		&commonAuthenticator[struct{}]{
			securitySchemeName: "AdminTokenAuth",
			headerKey: headerKey{
				name:         "X-Admin-Token",
				prefix:       "",
				removePrefix: "",
			},
			validationFunction: adminValidationFunction,
			contextKey:         "",
			errorMessage:       "Invalid Access token.",
		},
	}

	return func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
		ginContext := ctx.Value(middleware.GinContextKey).(*gin.Context)
		requestContext := ginContext.Request.Context()

		_, span := tracer.Start(requestContext, "authenticate")
		defer span.End()

		for _, validator := range authenticators {
			if input.SecuritySchemeName == validator.SecuritySchemeName() {
				return validator.Authenticate(ctx, input)
			}
		}

		return fmt.Errorf("invalid security scheme name '%s'", input.SecuritySchemeName)
	}
}
