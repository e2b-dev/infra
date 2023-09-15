package middleware

import (
	"context"
	"errors"
	"fmt"
	"github.com/e2b-dev/api/packages/api/internal/handlers"
	"net/http"
	"strings"

	middleware "github.com/deepmap/oapi-codegen/pkg/gin-middleware"
	"github.com/e2b-dev/api/packages/api/internal/constants"
	"github.com/getkin/kin-openapi/openapi3filter"
)

var (
	ErrNoAuthHeader      = errors.New("authorization header is missing")
	ErrInvalidAuthHeader = errors.New("authorization header is malformed")
)

type authenticator struct {
	securitySchemeName string
	headerKey          string
	prefix             string
	removePrefix       string
	validationFunction func(string) (string, error)
	contextKey         string
	errorMessage       string
}

// getApiKeyFromRequest extracts an API key from the header.
func (a *authenticator) getAPIKeyFromRequest(req *http.Request) (string, error) {
	apiKey := req.Header.Get(a.headerKey)
	// Check for the Authorization header.
	if apiKey == "" {
		return "", ErrNoAuthHeader
	}

	// Remove the prefix from the API key
	if a.removePrefix != "" {
		apiKey = strings.TrimSpace(strings.TrimPrefix(apiKey, a.removePrefix))
	}

	// We expect a header value to be in a special form
	if !strings.HasPrefix(apiKey, a.prefix) {
		return "", ErrInvalidAuthHeader
	}

	return apiKey, nil
}

// Authenticate uses the specified validator to ensure an API key is valid.
func (a *authenticator) Authenticate(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
	// Our security scheme is named ApiKeyAuth, ensure this is the case
	if input.SecuritySchemeName != a.securitySchemeName {
		return fmt.Errorf("security scheme %s != '%s'", a.securitySchemeName, input.SecuritySchemeName)
	}

	// Now, we need to get the API key from the request
	apiKey, err := a.getAPIKeyFromRequest(input.RequestValidationInput.Request)
	if err != nil {
		return fmt.Errorf("%v %w", a.errorMessage, err)
	}

	// If the API key is valid, we will get a ID back
	id, err := a.validationFunction(apiKey)
	if err != nil {
		return fmt.Errorf("%s %w", a.errorMessage, err)
	}
	handlers.ReportEvent(ctx, "Validated "+a.securitySchemeName)
	// Set the property on the gin context
	middleware.GetGinContext(ctx).Set(a.contextKey, id)

	return nil
}

func CreateAuthenticationFunc(teamValidationFunction func(string) (string, error), userValidationFunction func(string) (string, error)) func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
	apiKeyValidator := authenticator{
		securitySchemeName: "ApiKeyAuth",
		headerKey:          "X-API-Key",
		prefix:             "e2b_",
		removePrefix:       "",
		validationFunction: teamValidationFunction,
		contextKey:         constants.TeamIDContextKey,
		errorMessage:       "invalid API key, please visit https://e2b.dev/docs?reason=sdk-missing-api-key to get your API key:",
	}
	accessTokenValidator := authenticator{
		securitySchemeName: "AccessTokenAuth",
		headerKey:          "Authorization",
		prefix:             "sk_e2b_",
		removePrefix:       "Bearer ",
		validationFunction: userValidationFunction,
		contextKey:         constants.UserIDContextKey,
		errorMessage:       "invalid Access token, try to login again by running `e2b login`:",
	}

	return func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
		if input.SecuritySchemeName == apiKeyValidator.securitySchemeName {
			return apiKeyValidator.Authenticate(ctx, input)
		}

		if input.SecuritySchemeName == accessTokenValidator.securitySchemeName {
			return accessTokenValidator.Authenticate(ctx, input)
		}

		return fmt.Errorf("invalid security scheme name '%s'", input.SecuritySchemeName)
	}
}
