package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/cfg"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	sharedauth "github.com/e2b-dev/infra/packages/shared/pkg/auth"
)

var _ api.ServerInterface = (*APIStore)(nil)

type APIStore struct {
	config     cfg.Config
	db         *sqlcdb.Client
	clickhouse clickhouse.Clickhouse
}

func NewAPIStore(config cfg.Config, db *sqlcdb.Client, ch clickhouse.Clickhouse) *APIStore {
	return &APIStore{
		config:     config,
		db:         db,
		clickhouse: ch,
	}
}

func (s *APIStore) GetHealth(c *gin.Context) {
	c.String(http.StatusOK, "Health check successful")
}

func (s *APIStore) GetUserIDFromSupabaseToken(ctx context.Context, _ *gin.Context, supabaseToken string) (uuid.UUID, *api.APIError) {
	userID, err := sharedauth.ParseUserIDFromToken(ctx, s.config.SupabaseJWTSecrets, supabaseToken)
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       err,
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	return userID, nil
}

func (s *APIStore) ValidateTeamID(_ context.Context, _ *gin.Context, teamID string) (uuid.UUID, *api.APIError) {
	parsed, err := uuid.Parse(teamID)
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       fmt.Errorf("failed parsing team uuid: %w", err),
			ClientMsg: "Invalid team ID",
			Code:      http.StatusBadRequest,
		}
	}

	return parsed, nil
}
