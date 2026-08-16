package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (s *APIStore) PostAdminClusters(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := ginutils.ParseBody[api.AdminClusterCreateRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		return
	}

	body.Name = strings.TrimSpace(body.Name)
	body.Endpoint = strings.TrimSpace(body.Endpoint)
	body.Token = strings.TrimSpace(body.Token)
	body.SandboxProxyDomain = trimmedOptional(body.SandboxProxyDomain)
	body.AuthOrgId = trimmedOptional(body.AuthOrgId)
	if body.Name == "" || body.Endpoint == "" || body.Token == "" {
		s.sendAPIStoreError(c, http.StatusBadRequest, "name, endpoint and token are required")

		return
	}

	clusterID, err := s.db.Dashboard.CreateCluster(ctx, dashboardqueries.CreateClusterParams{
		Name:               body.Name,
		Endpoint:           body.Endpoint,
		EndpointTls:        body.EndpointTls,
		Token:              body.Token,
		SandboxProxyDomain: body.SandboxProxyDomain,
		AuthOrgID:          body.AuthOrgId,
	})
	if err != nil {
		if dberrors.IsUniqueConstraintViolation(err) {
			s.sendAPIStoreError(c, http.StatusConflict, "Cluster ID or auth organization is already registered")
		} else {
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to create cluster")
		}

		return
	}

	logger.L().Info(ctx, "admin cluster created", logger.WithClusterID(clusterID))
	c.JSON(http.StatusCreated, api.AdminClusterCreateResponse{ClusterId: clusterID})
}

func (s *APIStore) GetAdminTeamsTeamIDCluster(c *gin.Context, teamID api.TeamID) {
	clusterID, err := s.db.Dashboard.TeamClusterAssignment(
		c.Request.Context(),
		teamID,
	)
	if dberrors.IsNotFoundError(err) {
		s.sendAPIStoreError(c, http.StatusNotFound, "Team cluster assignment not found")

		return
	}
	if err != nil {
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to load team cluster assignment")

		return
	}

	c.JSON(http.StatusOK, api.AdminTeamClusterAssignmentResponse{ClusterId: clusterID})
}

func (s *APIStore) PutAdminTeamsTeamIDCluster(c *gin.Context, teamID api.TeamID) {
	ctx := c.Request.Context()

	body, err := ginutils.ParseBody[api.AdminTeamClusterAssignmentRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		return
	}
	if body.ClusterId == uuid.Nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "cluster_id is required")

		return
	}

	updated, err := s.db.Dashboard.AssignTeamCluster(ctx, dashboardqueries.AssignTeamClusterParams{
		ClusterID: body.ClusterId,
		TeamID:    teamID,
	})
	if err != nil {
		if dberrors.IsForeignKeyViolation(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Cluster not found")
		} else {
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to assign cluster to team")
		}

		return
	}
	if updated == 0 {
		s.sendAPIStoreError(c, http.StatusNotFound, "Team not found")

		return
	}

	logger.L().Info(ctx, "admin team cluster assigned",
		logger.WithTeamID(teamID.String()), logger.WithClusterID(body.ClusterId))

	if err := s.authService.InvalidateTeamCache(ctx, teamID); err != nil {
		logger.L().Error(ctx, "invalidating team cache after cluster assignment",
			logger.WithTeamID(teamID.String()), zap.Error(err))
	}

	c.Status(http.StatusNoContent)
}

func trimmedOptional(value *string) *string {
	if value == nil {
		return nil
	}

	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}
