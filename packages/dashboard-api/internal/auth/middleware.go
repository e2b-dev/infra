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

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	sharedauth "github.com/e2b-dev/infra/packages/shared/pkg/auth"
)

var (
	ErrNoAuthHeader      = errors.New("authorization header is missing")
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
	if key == "" {
		return "", ErrNoAuthHeader
	}

	if a.headerKey.removePrefix != "" {
		key = strings.TrimSpace(strings.TrimPrefix(key, a.headerKey.removePrefix))
	}

	if a.headerKey.prefix != "" && !strings.HasPrefix(key, a.headerKey.prefix) {
		return "", ErrInvalidAuthHeader
	}

	return key, nil
}

func (a *commonAuthenticator[T]) Authenticate(ctx context.Context, ginCtx *gin.Context, input *openapi3filter.AuthenticationInput) error {
	headerKey, err := a.getHeaderKeysFromRequest(input.RequestValidationInput.Request)
	if err != nil {
		ginCtx.Status(http.StatusUnauthorized)

		return err
	}

	result, validationError := a.validationFunction(ctx, ginCtx, headerKey)
	if validationError != nil {
		ginCtx.Status(validationError.Code)

		return fmt.Errorf("%s\n%s (%w)", a.errorMessage, validationError.ClientMsg, validationError.Err)
	}

	if a.contextKey != "" {
		ginCtx.Set(a.contextKey, result)
	}

	return nil
}

func (a *commonAuthenticator[T]) SecuritySchemeName() string {
	return a.securitySchemeName
}

func CreateAuthenticationFunc(
	supabaseTokenValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *api.APIError),
	supabaseTeamValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *api.APIError),
) openapi3filter.AuthenticationFunc {
	authenticators := []authenticator{
		&commonAuthenticator[uuid.UUID]{
			securitySchemeName: "Supabase1TokenAuth",
			headerKey: headerKey{
				name:         "X-Supabase-Token",
				prefix:       "",
				removePrefix: "",
			},
			validationFunction: supabaseTokenValidationFunction,
			contextKey:         sharedauth.UserIDContextKey,
			errorMessage:       "Invalid Supabase token.",
		},
		&commonAuthenticator[uuid.UUID]{
			securitySchemeName: "Supabase2TeamAuth",
			headerKey: headerKey{
				name:         "X-Supabase-Team",
				prefix:       "",
				removePrefix: "",
			},
			validationFunction: supabaseTeamValidationFunction,
			contextKey:         sharedauth.TeamContextKey,
			errorMessage:       "Invalid Supabase team.",
		},
	}

	return func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
		ginCtx := middleware.GetGinContext(ctx)

		for _, validator := range authenticators {
			if input.SecuritySchemeName == validator.SecuritySchemeName() {
				return validator.Authenticate(ctx, ginCtx, input)
			}
		}

		return fmt.Errorf("invalid security scheme name '%s'", input.SecuritySchemeName)
	}
}
