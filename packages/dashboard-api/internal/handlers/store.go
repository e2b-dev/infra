package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/cfg"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
)

var _ api.ServerInterface = (*APIStore)(nil)

type APIStore struct {
	config      cfg.Config
	db          *sqlcdb.Client
	authDB      *authdb.Client
	clickhouse  clickhouse.Clickhouse
	authService *sharedauth.AuthService[*types.Team]
}

func NewAPIStore(config cfg.Config, db *sqlcdb.Client, authDB *authdb.Client, ch clickhouse.Clickhouse, authService *sharedauth.AuthService[*types.Team]) *APIStore {
	return &APIStore{
		config:      config,
		db:          db,
		authDB:      authDB,
		clickhouse:  ch,
		authService: authService,
	}
}

func (s *APIStore) GetHealth(c *gin.Context) {
	c.String(http.StatusOK, "Health check successful")
}

func (s *APIStore) GetTeamFromAPIKey(ctx context.Context, _ *gin.Context, apiKey string) (*types.Team, *sharedauth.APIError) {
	return s.authService.ValidateAPIKey(ctx, apiKey)
}

func (s *APIStore) GetUserFromAccessToken(ctx context.Context, _ *gin.Context, accessToken string) (uuid.UUID, *sharedauth.APIError) {
	return s.authService.ValidateAccessToken(ctx, accessToken)
}

func (s *APIStore) GetUserIDFromSupabaseToken(ctx context.Context, _ *gin.Context, supabaseToken string) (uuid.UUID, *sharedauth.APIError) {
	return s.authService.ValidateSupabaseToken(ctx, supabaseToken)
}

func (s *APIStore) GetTeamFromSupabaseToken(ctx context.Context, ginCtx *gin.Context, teamID string) (*types.Team, *sharedauth.APIError) {
	return s.authService.ValidateSupabaseTeam(ctx, ginCtx, teamID, sharedauth.UserIDContextKey)
}
