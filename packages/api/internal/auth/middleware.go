package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cfg"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/api/internal/middleware/otel/metrics"
	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/auth")

func adminValidationFunction(adminToken string) func(ctx context.Context, ginCtx *gin.Context, token string) (struct{}, *api.APIError) {
	return func(_ context.Context, _ *gin.Context, token string) (struct{}, *api.APIError) {
		if token != adminToken {
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
	authenticators := []sharedauth.Authenticator{
		&sharedauth.CommonAuthenticator[*types.Team]{
			SchemeName: "ApiKeyAuth",
			Header: sharedauth.HeaderKey{
				Name:         "X-API-Key",
				Prefix:       "e2b_",
				RemovePrefix: "",
			},
			ValidationFunc: teamValidationFunction,
			ContextKey:     TeamContextKey,
			ErrorMessage:   "Invalid API key, please visit https://e2b.dev/docs/api-key for more information.",
		},
		&sharedauth.CommonAuthenticator[uuid.UUID]{
			SchemeName: "AccessTokenAuth",
			Header: sharedauth.HeaderKey{
				Name:         "Authorization",
				Prefix:       "sk_e2b_",
				RemovePrefix: "Bearer ",
			},
			ValidationFunc: userValidationFunction,
			ContextKey:     UserIDContextKey,
			ErrorMessage:   "Invalid Access token, try to login again by running `e2b auth login`.",
		},
		&sharedauth.CommonAuthenticator[uuid.UUID]{
			SchemeName: "Supabase1TokenAuth",
			Header: sharedauth.HeaderKey{
				Name:         "X-Supabase-Token",
				Prefix:       "",
				RemovePrefix: "",
			},
			ValidationFunc: supabaseTokenValidationFunction,
			ContextKey:     UserIDContextKey,
			ErrorMessage:   "Invalid Supabase token.",
		},
		&sharedauth.CommonAuthenticator[*types.Team]{
			SchemeName: "Supabase2TeamAuth",
			Header: sharedauth.HeaderKey{
				Name:         "X-Supabase-Team",
				Prefix:       "",
				RemovePrefix: "",
			},
			ValidationFunc: supabaseTeamValidationFunction,
			ContextKey:     TeamContextKey,
			ErrorMessage:   "Invalid Supabase token teamID.",
		},
		&sharedauth.CommonAuthenticator[struct{}]{
			SchemeName: "AdminTokenAuth",
			Header: sharedauth.HeaderKey{
				Name:         "X-Admin-Token",
				Prefix:       "",
				RemovePrefix: "",
			},
			ValidationFunc: adminValidationFunction(config.AdminToken),
			ContextKey:     "",
			ErrorMessage:   "Invalid Access token.",
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
