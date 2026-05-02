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
	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	supabasedb "github.com/e2b-dev/infra/packages/db/pkg/supabase"
	"github.com/e2b-dev/infra/packages/shared/pkg/apierrors"
)

var _ api.ServerInterface = (*APIStore)(nil)

type APIStore struct {
	config            cfg.Config
	db                *sqlcdb.Client
	authDB            *authdb.Client
	supabaseDB        *supabasedb.Client
	clickhouse        clickhouse.Clickhouse
	authService       *sharedauth.AuthService[*types.Team]
	teamProvisionSink internalteamprovision.TeamProvisionSink
}

func NewAPIStore(config cfg.Config, db *sqlcdb.Client, authDB *authdb.Client, supabaseDB *supabasedb.Client, ch clickhouse.Clickhouse, authService *sharedauth.AuthService[*types.Team], teamProvisionSink internalteamprovision.TeamProvisionSink) *APIStore {
	return &APIStore{
		config:            config,
		db:                db,
		authDB:            authDB,
		supabaseDB:        supabaseDB,
		clickhouse:        ch,
		authService:       authService,
		teamProvisionSink: teamProvisionSink,
	}
}

func (s *APIStore) sendAPIStoreError(c *gin.Context, code int, message string) {
	apierrors.SendAPIStoreError(c, code, message)
}

func (s *APIStore) GetHealth(c *gin.Context) {
	c.JSON(http.StatusOK, api.HealthResponse{
		Message: "Health check successful",
	})
}

func (s *APIStore) GetUserIDFromAuthProviderToken(ctx context.Context, ginCtx *gin.Context, token string) (uuid.UUID, *sharedauth.APIError) {
	return s.authService.ValidateAuthProviderToken(ctx, ginCtx, token)
}

func (s *APIStore) GetTeamFromSupabaseToken(ctx context.Context, ginCtx *gin.Context, teamID string) (*types.Team, *sharedauth.APIError) {
	return s.authService.ValidateSupabaseTeam(ctx, ginCtx, teamID)
}
