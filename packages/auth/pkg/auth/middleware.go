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
	SetContextFunc func(ginCtx *gin.Context, value T)
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

	if a.SetContextFunc != nil {
		a.SetContextFunc(ginCtx, result)
	}

	return nil
}

// SecuritySchemeName returns the name of the security scheme this authenticator handles.
func (a *CommonAuthenticator[T]) SecuritySchemeName() string {
	return a.SchemeName
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
	return &CommonAuthenticator[*types.Team]{
		SchemeName: "ApiKeyAuth",
		Header: HeaderKey{
			Name:   HeaderAPIKey,
			Prefix: PrefixAPIKey,
		},
		ValidationFunc: validationFunc,
		SetContextFunc: SetTeamInfo,
		ErrorMessage:   "Invalid API key, please visit https://e2b.dev/docs/api-key for more information.",
	}
}

// NewAccessTokenAuthenticator creates an authenticator for the AccessTokenAuth security scheme (Authorization Bearer sk_e2b_).
func NewAccessTokenAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *APIError)) Authenticator {
	return &CommonAuthenticator[uuid.UUID]{
		SchemeName: "AccessTokenAuth",
		Header: HeaderKey{
			Name:         HeaderAuthorization,
			Prefix:       PrefixAccessToken,
			RemovePrefix: PrefixBearer,
		},
		ValidationFunc: validationFunc,
		SetContextFunc: SetUserID,
		ErrorMessage:   "Invalid Access token, try to login again by running `e2b auth login`.",
	}
}

// NewAuthProviderTokenAuthenticator creates an authenticator for AuthProviderTokenAuth (Authorization Bearer token).
func NewAuthProviderTokenAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *APIError)) Authenticator {
	return &CommonAuthenticator[uuid.UUID]{
		SchemeName: "AuthProviderTokenAuth",
		Header: HeaderKey{
			Name:         HeaderAuthorization,
			RemovePrefix: PrefixBearer,
		},
		ValidationFunc: validationFunc,
		SetContextFunc: SetUserID,
		ErrorMessage:   "Invalid auth provider token.",
	}
}

// NewSupabaseTokenAuthenticator creates an authenticator for the Supabase1TokenAuth security scheme (X-Supabase-Token header).
func NewSupabaseTokenAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *APIError)) Authenticator {
	return &CommonAuthenticator[uuid.UUID]{
		SchemeName: "Supabase1TokenAuth",
		Header: HeaderKey{
			Name: HeaderSupabaseToken,
		},
		ValidationFunc: validationFunc,
		SetContextFunc: SetUserID,
		ErrorMessage:   "Invalid Supabase token.",
	}
}

// NewSupabaseTeamAuthenticator creates an authenticator for the Supabase2TeamAuth security scheme (X-Supabase-Team header).
func NewSupabaseTeamAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (*types.Team, *APIError)) Authenticator {
	return &CommonAuthenticator[*types.Team]{
		SchemeName: "Supabase2TeamAuth",
		Header: HeaderKey{
			Name: HeaderSupabaseTeam,
		},
		ValidationFunc: validationFunc,
		SetContextFunc: SetTeamInfo,
		ErrorMessage:   "Invalid Supabase token teamID.",
	}
}

// NewAuthProviderTeamAuthenticator creates an authenticator for the AuthProviderTeamAuth security scheme (X-Team-Id header).
func NewAuthProviderTeamAuthenticator(validationFunc func(ctx context.Context, ginCtx *gin.Context, token string) (*types.Team, *APIError)) Authenticator {
	return &CommonAuthenticator[*types.Team]{
		SchemeName: "AuthProviderTeamAuth",
		Header: HeaderKey{
			Name: HeaderTeamID,
		},
		ValidationFunc: validationFunc,
		SetContextFunc: SetTeamInfo,
		ErrorMessage:   "Invalid auth provider token teamID.",
	}
}

// NewAdminTokenAuthenticator creates an authenticator for the AdminTokenAuth security scheme (X-Admin-Token header).
func NewAdminTokenAuthenticator(adminToken string) Authenticator {
	return &CommonAuthenticator[struct{}]{
		SchemeName: "AdminTokenAuth",
		Header: HeaderKey{
			Name: HeaderAdminToken,
		},
		ValidationFunc: adminValidationFunction(adminToken),
		ErrorMessage:   "Invalid Access token.",
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
