package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
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

func newHeaderKey(name, prefix, removePrefix string) headerKey {
	return headerKey{
		name:         name,
		prefix:       prefix,
		removePrefix: removePrefix,
	}
}

type commonAuthenticator[T any] struct {
	securitySchemeName        string
	headerKeys                []headerKey
	validationFunction        func(context.Context, []string) (T, *api.APIError)
	contextKeyMappingFunction func(T) map[string]interface{}
	errorMessage              string
}

type authenticator interface {
	Authenticate(ctx context.Context, input *openapi3filter.AuthenticationInput) error
	SecuritySchemeName() string
}

// getHeaderKeysFromRequest extracts header keys from the header.
func (a *commonAuthenticator[T]) getHeaderKeysFromRequest(req *http.Request) ([]string, error) {
	keys := make([]string, 0)

	for _, headerKey := range a.headerKeys {
		key := req.Header.Get(headerKey.name)
		// Check for the Authorization header.
		if key == "" {
			return nil, ErrNoAuthHeader
		}

		// Remove the prefix from the API key
		if headerKey.removePrefix != "" {
			key = strings.TrimSpace(strings.TrimPrefix(key, headerKey.removePrefix))
		}

		// We expect a header value to be in a special form
		if !strings.HasPrefix(key, headerKey.prefix) {
			return nil, ErrInvalidAuthHeader
		}

		keys = append(keys, key)
	}

	return keys, nil
}

// Authenticate uses the specified validator to ensure an API key is valid.
func (a *commonAuthenticator[T]) Authenticate(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
	// Our security scheme is named ApiKeyAuth, ensure this is the case
	if input.SecuritySchemeName != a.securitySchemeName {
		return fmt.Errorf("security scheme %s != '%s'", a.securitySchemeName, input.SecuritySchemeName)
	}

	// Now, we need to get the API key from the request
	headerKeys, err := a.getHeaderKeysFromRequest(input.RequestValidationInput.Request)
	if err != nil {
		telemetry.ReportCriticalError(ctx, fmt.Errorf("%v %w", a.errorMessage, err))

		return fmt.Errorf("%v %w", a.errorMessage, err)
	}

	telemetry.ReportEvent(ctx, "api key extracted")

	// If the API key is valid, we will get a result back
	result, validationError := a.validationFunction(ctx, headerKeys)
	if validationError != nil {
		log.Printf("validation error %v", validationError.Err)
		telemetry.ReportError(ctx, fmt.Errorf("%s %w", a.errorMessage, validationError.Err))

		return fmt.Errorf(a.errorMessage)
	}

	telemetry.ReportEvent(ctx, "api key validated")

	// Set the property on the gin context
	if a.contextKeyMappingFunction != nil {
		for key, value := range a.contextKeyMappingFunction(result) {
			middleware.GetGinContext(ctx).Set(key, value)
		}
	}

	return nil
}

func (a *commonAuthenticator[T]) SecuritySchemeName() string {
	return a.securitySchemeName
}

func adminValidationFunction(_ context.Context, tokens []string) (struct{}, *api.APIError) {
	if tokens[0] != adminToken {
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
	teamValidationFunction func(context.Context, []string) (authcache.AuthTeamInfo, *api.APIError),
	userValidationFunction func(context.Context, []string) (uuid.UUID, *api.APIError),
	supabaseValidationFunction func(context.Context, []string) (*SupabaseInfo, *api.APIError),
) func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
	authenticators := []authenticator{
		&commonAuthenticator[authcache.AuthTeamInfo]{
			securitySchemeName: "ApiKeyAuth",
			headerKeys: []headerKey{
				newHeaderKey("X-API-Key", "e2b_", ""),
			},
			validationFunction: teamValidationFunction,
			contextKeyMappingFunction: func(ti authcache.AuthTeamInfo) map[string]interface{} {
				return map[string]interface{}{
					TeamContextKey: ti,
				}
			},
			errorMessage: "Invalid API key, please visit https://e2b.dev/docs?reason=sdk-missing-api-key to get your API key.",
		},
		&commonAuthenticator[uuid.UUID]{
			securitySchemeName: "AccessTokenAuth",
			headerKeys: []headerKey{
				newHeaderKey("Authorization", "sk_e2b_", "Bearer "),
			},
			validationFunction: userValidationFunction,
			contextKeyMappingFunction: func(uuid uuid.UUID) map[string]interface{} {
				return map[string]interface{}{
					UserIDContextKey: uuid,
				}
			},
			errorMessage: "Invalid Access token, try to login again by running `e2b login`.",
		},
		&commonAuthenticator[*SupabaseInfo]{
			securitySchemeName: "SupabaseTokenAuth",
			headerKeys: []headerKey{
				newHeaderKey("X-Supabase-Token", "", ""),
				newHeaderKey("X-Supabase-Team", "", ""),
			},
			validationFunction: supabaseValidationFunction,
			contextKeyMappingFunction: func(si *SupabaseInfo) map[string]interface{} {
				return map[string]interface{}{
					TeamContextKey:   si.TeamInfo,
					UserIDContextKey: si.UserID,
				}
			},
			errorMessage: "Invalid Supabase token.",
		},
		&commonAuthenticator[struct{}]{
			securitySchemeName: "AdminTokenAuth",
			headerKeys: []headerKey{
				newHeaderKey("X-Admin-Token", "", ""),
			},
			validationFunction: adminValidationFunction,
			contextKeyMappingFunction: func(_ struct{}) map[string]interface{} {
				return map[string]interface{}{}
			},
			errorMessage: "Invalid Access token.",
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
