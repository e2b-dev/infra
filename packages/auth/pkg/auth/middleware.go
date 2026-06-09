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
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var (
	ErrNoAuthHeader      = errors.New("authorization header is missing")
	ErrInvalidAuthHeader = errors.New("authorization header is malformed")
)

// headerKey describes how to extract an authentication token from an HTTP request header.
type headerKey struct {
	name         string
	prefix       string
	removePrefix string
}

// Authenticator is implemented by types that can authenticate requests against a security scheme.
type Authenticator interface {
	Authenticate(ctx context.Context, ginCtx *gin.Context, input *openapi3filter.AuthenticationInput) error
	SecuritySchemeName() string
}

// commonAuthenticator implements Authenticator using a header-based token with a pluggable validation function.
type commonAuthenticator[T any] struct {
	schemeName     string
	header         headerKey
	validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (T, *APIError)
	setContextFunc func(ginCtx *gin.Context, value T)
	errorMessage   string
}

// getHeaderKeysFromRequest extracts the token from the request header.
func (a *commonAuthenticator[T]) getHeaderKeysFromRequest(req *http.Request) (string, error) {
	key := req.Header.Get(a.header.name)
	if key == "" {
		return "", ErrNoAuthHeader
	}

	if a.header.removePrefix != "" {
		key = strings.TrimSpace(strings.TrimPrefix(key, a.header.removePrefix))
	}

	if a.header.prefix != "" && !strings.HasPrefix(key, a.header.prefix) {
		return "", ErrInvalidAuthHeader
	}

	return key, nil
}

// Authenticate validates the request against the security scheme.
func (a *commonAuthenticator[T]) Authenticate(ctx context.Context, ginCtx *gin.Context, input *openapi3filter.AuthenticationInput) error {
	key, err := a.getHeaderKeysFromRequest(input.RequestValidationInput.Request)
	if err != nil {
		telemetry.ReportEvent(ctx, "auth scheme skipped",
			attribute.String("auth.scheme", a.schemeName),
			attribute.String("auth.reason", err.Error()),
		)

		// stamp 401 so the ErrorHandler's max(writer, 400) resolves to 401
		// when every security group fails. without this, auth failures become 400s.
		ginCtx.Status(http.StatusUnauthorized)

		return err
	}

	telemetry.ReportEvent(ctx, "api key extracted")

	result, validationError := a.validationFunc(ctx, ginCtx, key)
	if validationError != nil {
		telemetry.ReportError(ctx,
			"validation error",
			validationError.Err,
			attribute.String("error.message", a.errorMessage),
			attribute.Int("http.status_code", validationError.Code),
			attribute.String("http.status_text", http.StatusText(validationError.Code)),
		)

		ginCtx.Status(validationError.Code)

		var forbiddenError *TeamForbiddenError
		if errors.As(validationError.Err, &forbiddenError) {
			return validationError.Err
		}

		return fmt.Errorf("%s\n%s", a.errorMessage, validationError.ClientMsg)
	}

	telemetry.ReportEvent(ctx, "api key validated")

	if a.setContextFunc != nil {
		a.setContextFunc(ginCtx, result)
	}

	return nil
}

// SecuritySchemeName returns the name of the security scheme this authenticator handles.
func (a *commonAuthenticator[T]) SecuritySchemeName() string {
	return a.schemeName
}

func adminValidationFunction(adminToken string) func(ctx context.Context, ginCtx *gin.Context, token string) (struct{}, *APIError) {
	return func(_ context.Context, _ *gin.Context, token string) (struct{}, *APIError) {
		if subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) != 1 {
			return struct{}{}, &APIError{
				Code:      http.StatusUnauthorized,
				Err:       errors.New("invalid access token"),
				ClientMsg: "Invalid Access token.",
			}
		}

		return struct{}{}, nil
	}
}

// NewApiKeyAuthenticator creates an authenticator for the ApiKeyAuth security scheme (X-API-Key header, e2b_ prefix).
func NewApiKeyAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (*types.Team, *APIError)) Authenticator {
	return &commonAuthenticator[*types.Team]{
		schemeName: "ApiKeyAuth",
		header: headerKey{
			name:   HeaderAPIKey,
			prefix: PrefixAPIKey,
		},
		validationFunc: validationFunc,
		setContextFunc: setTeamInfo,
		errorMessage:   "Invalid API key, please visit https://e2b.dev/docs/api-key for more information.",
	}
}

// NewAccessTokenAuthenticator creates an authenticator for the AccessTokenAuth security scheme (Authorization Bearer sk_e2b_).
func NewAccessTokenAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *APIError)) Authenticator {
	return &commonAuthenticator[uuid.UUID]{
		schemeName: "AccessTokenAuth",
		header: headerKey{
			name:         HeaderAuthorization,
			prefix:       PrefixAccessToken,
			removePrefix: PrefixBearer,
		},
		validationFunc: validationFunc,
		setContextFunc: setUserID,
		errorMessage:   "Invalid Access token, try to login again by running `e2b auth login`.",
	}
}

// NewAuthProviderBearerAuthenticator creates an authenticator for AuthProviderBearerAuth (Authorization Bearer token).
func NewAuthProviderBearerAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *APIError)) Authenticator {
	return &commonAuthenticator[uuid.UUID]{
		schemeName: "AuthProviderBearerAuth",
		header: headerKey{
			name:         HeaderAuthorization,
			removePrefix: PrefixBearer,
		},
		validationFunc: validationFunc,
		setContextFunc: setUserID,
		errorMessage:   "Invalid auth provider token.",
	}
}

// NewSupabaseTokenAuthenticator creates an authenticator for the Supabase1TokenAuth security scheme (X-Supabase-Token header).
func NewSupabaseTokenAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *APIError)) Authenticator {
	return &commonAuthenticator[uuid.UUID]{
		schemeName: "Supabase1TokenAuth",
		header: headerKey{
			name: HeaderSupabaseToken,
		},
		validationFunc: validationFunc,
		setContextFunc: setUserID,
		errorMessage:   "Invalid Supabase token.",
	}
}

// NewSupabaseTeamAuthenticator creates an authenticator for the Supabase2TeamAuth security scheme (X-Supabase-Team header).
func NewSupabaseTeamAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (*types.Team, *APIError)) Authenticator {
	return &commonAuthenticator[*types.Team]{
		schemeName: "Supabase2TeamAuth",
		header: headerKey{
			name: HeaderSupabaseTeam,
		},
		validationFunc: validationFunc,
		setContextFunc: setTeamInfo,
		errorMessage:   "Invalid Supabase token teamID.",
	}
}

// NewAuthProviderTeamAuthenticator creates an authenticator for the AuthProviderTeamAuth security scheme (X-Team-Id header).
func NewAuthProviderTeamAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (*types.Team, *APIError)) Authenticator {
	return &commonAuthenticator[*types.Team]{
		schemeName: "AuthProviderTeamAuth",
		header: headerKey{
			name: HeaderTeamID,
		},
		validationFunc: validationFunc,
		setContextFunc: setTeamInfo,
		errorMessage:   "Invalid auth provider token teamID.",
	}
}

// NewAdminApiKeyAuthenticator creates an authenticator for the AdminApiKeyAuth security scheme (X-Admin-Token header).
func NewAdminApiKeyAuthenticator(adminToken string) Authenticator {
	return newAdminApiKeyAuthenticator("AdminApiKeyAuth", adminToken)
}

func newAdminApiKeyAuthenticator(schemeName, adminToken string) Authenticator {
	return &commonAuthenticator[struct{}]{
		schemeName: schemeName,
		header: headerKey{
			name: HeaderAdminToken,
		},
		validationFunc: adminValidationFunction(adminToken),
		errorMessage:   "Invalid Access token.",
	}
}

// NewAdminTeamAuthenticator creates an authenticator for AdminTeamAuth (X-Team-ID header).
func NewAdminTeamAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, teamID string) (*types.Team, *APIError)) Authenticator {
	return newAdminTeamAuthenticator("AdminTeamAuth", validationFunc)
}

func newAdminTeamAuthenticator(
	schemeName string,
	validationFunc func(ctx context.Context, ginCtx *gin.Context, teamID string) (*types.Team, *APIError),
) Authenticator {
	return &commonAuthenticator[*types.Team]{
		schemeName: schemeName,
		header: headerKey{
			name: HeaderTeamID,
		},
		validationFunc: validationFunc,
		setContextFunc: setTeamInfo,
		errorMessage:   "Invalid admin token teamID.",
	}
}

// CreateAuthenticationFunc creates an OpenAPI authentication function from a list of authenticators.
func CreateAuthenticationFunc(
	authenticators []Authenticator,
	preAuthHook func(*gin.Context),
) openapi3filter.AuthenticationFunc {
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
