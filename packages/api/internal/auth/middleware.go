package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	"github.com/e2b-dev/infra/packages/api/internal/db"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/middleware/otel/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/auth")

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
	validationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (T, *api.APIError)
	contextKey         string
	errorMessage       string
}

type authenticator interface {
	Authenticate(ctx context.Context, ginCtx *gin.Context, input *openapi3filter.AuthenticationInput) error
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
func (a *commonAuthenticator[T]) Authenticate(ctx context.Context, ginCtx *gin.Context, input *openapi3filter.AuthenticationInput) error {
	// Now, we need to get the API key from the request
	headerKey, err := a.getHeaderKeysFromRequest(input.RequestValidationInput.Request)
	if err != nil {
		telemetry.ReportError(ctx,
			"authorization header is missing",
			err,
			attribute.String("error.message", a.errorMessage),
		)

		ginCtx.Status(http.StatusUnauthorized)

		return err
	}

	telemetry.ReportEvent(ctx, "api key extracted")

	// If the API key is valid, we will get a result back
	result, validationError := a.validationFunction(ctx, ginCtx, headerKey)
	if validationError != nil {
		telemetry.ReportError(ctx,
			"validation error",
			validationError.Err,
			attribute.String("error.message", a.errorMessage),
			attribute.Int("http.status_code", validationError.Code),
			attribute.String("http.status_text", http.StatusText(validationError.Code)),
		)

		ginCtx.Status(validationError.Code)

		var forbiddenError *db.TeamForbiddenError
		if errors.As(validationError.Err, &forbiddenError) {
			return fmt.Errorf("forbidden: %w", validationError.Err)
		}

		var blockedError *db.TeamBlockedError
		if errors.As(validationError.Err, &blockedError) {
			return fmt.Errorf("blocked: %w", validationError.Err)
		}

		return fmt.Errorf("%s\n%s (%w)", a.errorMessage, validationError.ClientMsg, validationError.Err)
	}

	telemetry.ReportEvent(ctx, "api key validated")

	// Set the property on the gin context
	if a.contextKey != "" {
		ginCtx.Set(a.contextKey, result)
	}

	return nil
}

func (a *commonAuthenticator[T]) SecuritySchemeName() string {
	return a.securitySchemeName
}

func adminValidationFunction(adminToken string) func(ctx context.Context, ginCtx *gin.Context, token string) (struct{}, *api.APIError) {
	return func(_ context.Context, _ *gin.Context, token string) (struct{}, *api.APIError) {
		if subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) != 1 {
			return struct{}{}, &api.APIError{
				Code:      http.StatusUnauthorized,
				Err:       errors.New("invalid access token"),
				ClientMsg: "Invalid Access token.",
			}
		}

		return struct{}{}, nil
	}
}

func CreateAuthenticationFunc(
	config cfg.Config,
	teamValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (*types.Team, *api.APIError),
	userValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *api.APIError),
	supabaseTokenValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *api.APIError),
	supabaseTeamValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (*types.Team, *api.APIError),
) openapi3filter.AuthenticationFunc {
	authenticators := []authenticator{
		&commonAuthenticator[*types.Team]{
			securitySchemeName: "ApiKeyAuth",
			headerKey: headerKey{
				name:         "X-API-Key",
				prefix:       "e2b_",
				removePrefix: "",
			},
			validationFunction: teamValidationFunction,
			contextKey:         TeamContextKey,
			errorMessage:       "Invalid API key, please visit https://e2b.dev/docs/api-key for more information.",
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
		&commonAuthenticator[*types.Team]{
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
			validationFunction: adminValidationFunction(config.AdminToken),
			contextKey:         "",
			errorMessage:       "Invalid Access token.",
		},
	}

	return func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
		ginCtx := middleware.GetGinContext(ctx)

		// Set the processing start time after body parsing to exclude slow clients from metrics duration.
		metrics.SetProcessingStartTime(ginCtx)

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
