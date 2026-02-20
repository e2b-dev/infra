package auth

import (
	"context"
	"fmt"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	middleware "github.com/oapi-codegen/gin-middleware"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	sharedauth "github.com/e2b-dev/infra/packages/shared/pkg/auth"
)

func CreateAuthenticationFunc(
	supabaseTokenValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *api.APIError),
	supabaseTeamValidationFunction func(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *api.APIError),
) openapi3filter.AuthenticationFunc {
	authenticators := []sharedauth.Authenticator{
		&sharedauth.CommonAuthenticator[uuid.UUID]{
			SchemeName: "Supabase1TokenAuth",
			Header: sharedauth.HeaderKey{
				Name:         "X-Supabase-Token",
				Prefix:       "",
				RemovePrefix: "",
			},
			ValidationFunc: supabaseTokenValidationFunction,
			ContextKey:     sharedauth.UserIDContextKey,
			ErrorMessage:   "Invalid Supabase token.",
		},
		&sharedauth.CommonAuthenticator[uuid.UUID]{
			SchemeName: "Supabase2TeamAuth",
			Header: sharedauth.HeaderKey{
				Name:         "X-Supabase-Team",
				Prefix:       "",
				RemovePrefix: "",
			},
			ValidationFunc: supabaseTeamValidationFunction,
			ContextKey:     sharedauth.TeamContextKey,
			ErrorMessage:   "Invalid Supabase team.",
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
