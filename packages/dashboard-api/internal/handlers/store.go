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
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/apierrors"
)

var _ api.ServerInterface = (*APIStore)(nil)

type APIStore struct {
	config            cfg.Config
	db                *sqlcdb.Client
	authDB            *authdb.Client
	clickhouse        clickhouse.Clickhouse
	authService       sharedauth.Service
	teamProvisionSink internalteamprovision.TeamProvisionSink
	userProfiles      userprofile.Provider
}

func NewAPIStore(
	config cfg.Config,
	db *sqlcdb.Client,
	authDB *authdb.Client,
	ch clickhouse.Clickhouse,
	authService sharedauth.Service,
	teamProvisionSink internalteamprovision.TeamProvisionSink,
	userProfiles userprofile.Provider,
) *APIStore {
	return &APIStore{
		config:            config,
		db:                db,
		authDB:            authDB,
		clickhouse:        ch,
		authService:       authService,
		teamProvisionSink: teamProvisionSink,
		userProfiles:      userProfiles,
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

func (s *APIStore) GetTeamFromAuthProviderToken(ctx context.Context, ginCtx *gin.Context, teamID string) (*types.Team, *sharedauth.APIError) {
	return s.authService.ValidateAuthProviderTeam(ctx, ginCtx, teamID)
}
