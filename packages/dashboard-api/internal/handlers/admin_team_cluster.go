package handlers

import (
	"context"
	"errors"
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

var errInvalidClusterRegistration = errors.New("invalid cluster registration")

type clusterRegistration struct {
	ClusterID          *uuid.UUID
	Name               string
	Endpoint           string
	EndpointTLS        bool
	Token              string
	SandboxProxyDomain *string
	AuthOrgID          *string
}

func (s *APIStore) PostAdminClusters(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := ginutils.ParseBody[api.AdminClusterCreateRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		return
	}

	clusterID, err := s.createCluster(ctx, clusterRegistration{
		ClusterID:          body.ClusterId,
		Name:               body.Name,
		Endpoint:           body.Endpoint,
		EndpointTLS:        body.EndpointTls,
		Token:              body.Token,
		SandboxProxyDomain: body.SandboxProxyDomain,
		AuthOrgID:          body.AuthOrgId,
	})
	if errors.Is(err, errInvalidClusterRegistration) {
		s.sendAPIStoreError(c, http.StatusBadRequest, "name, endpoint and token are required")

		return
	}
	if err != nil {
		if dberrors.IsUniqueConstraintViolation(err) || dberrors.IsNotFoundError(err) {
			s.sendAPIStoreError(c, http.StatusConflict, "Cluster ID or auth organization is already registered")
		} else {
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to create cluster")
		}

		return
	}

	logger.L().Info(ctx, "admin cluster created", logger.WithClusterID(clusterID))
	c.JSON(http.StatusCreated, api.AdminClusterCreateResponse{ClusterId: clusterID})
}

func (s *APIStore) createCluster(ctx context.Context, registration clusterRegistration) (uuid.UUID, error) {
	registration.Name = strings.TrimSpace(registration.Name)
	registration.Endpoint = strings.TrimSpace(registration.Endpoint)
	registration.Token = strings.TrimSpace(registration.Token)
	registration.SandboxProxyDomain = trimmedOptional(registration.SandboxProxyDomain)
	registration.AuthOrgID = trimmedOptional(registration.AuthOrgID)
	if registration.Name == "" || registration.Endpoint == "" || registration.Token == "" ||
		registration.ClusterID != nil && *registration.ClusterID == uuid.Nil {
		return uuid.Nil, errInvalidClusterRegistration
	}

	return s.db.Dashboard.CreateCluster(ctx, dashboardqueries.CreateClusterParams{
		ClusterID:          registration.ClusterID,
		Name:               registration.Name,
		Endpoint:           registration.Endpoint,
		EndpointTls:        registration.EndpointTLS,
		Token:              registration.Token,
		SandboxProxyDomain: registration.SandboxProxyDomain,
		AuthOrgID:          registration.AuthOrgID,
	})
}

func (s *APIStore) DeleteAdminClustersClusterID(c *gin.Context, clusterID api.ClusterID) {
	ctx := c.Request.Context()

	_, err := s.db.Dashboard.DeleteCluster(ctx, clusterID)
	if err != nil {
		if dberrors.IsForeignKeyViolation(err) {
			s.sendAPIStoreError(c, http.StatusConflict, "Cluster is still referenced by a team or environment")
		} else {
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to delete cluster")
		}

		return
	}

	logger.L().Info(ctx, "admin cluster deleted", logger.WithClusterID(clusterID))
	c.Status(http.StatusNoContent)
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

	if clusterID == nil {
		s.sendAPIStoreError(c, http.StatusNotFound, "Team cluster assignment not found")

		return
	}

	c.JSON(http.StatusOK, api.AdminTeamClusterAssignmentResponse{ClusterId: *clusterID})
}

func (s *APIStore) PutAdminTeamsTeamIDCluster(c *gin.Context, teamID api.TeamID) {
	body, err := ginutils.ParseBody[api.AdminTeamClusterAssignmentRequest](c.Request.Context(), c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		return
	}
	s.assignTeamCluster(c, teamID, body.ClusterId, body.PreserveExisting != nil && *body.PreserveExisting)
}

func (s *APIStore) assignTeamCluster(c *gin.Context, teamID, clusterID uuid.UUID, preserveExisting bool) {
	if clusterID == uuid.Nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "cluster_id is required")

		return
	}

	ctx := c.Request.Context()
	result, err := s.db.Dashboard.AssignTeamCluster(ctx, dashboardqueries.AssignTeamClusterParams{
		ClusterID:        clusterID,
		TeamID:           teamID,
		PreserveExisting: preserveExisting,
	})
	if dberrors.IsNotFoundError(err) {
		s.sendAPIStoreError(c, http.StatusNotFound, "Team not found")

		return
	}
	if err != nil {
		if dberrors.IsForeignKeyViolation(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Cluster not found")
		} else {
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to assign cluster to team")
		}

		return
	}
	if !result.Assigned {
		s.sendAPIStoreError(c, http.StatusConflict, "Team is already assigned to a different cluster")

		return
	}

	logger.L().Info(ctx, "admin team cluster assigned",
		logger.WithTeamID(teamID.String()), logger.WithClusterID(clusterID))

	if err := s.authService.InvalidateTeamCache(ctx, teamID); err != nil {
		logger.L().Error(ctx, "invalidating team cache after cluster assignment",
			logger.WithTeamID(teamID.String()), zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to invalidate team cache after cluster assignment")

		return
	}

	c.Status(http.StatusNoContent)
}

func (s *APIStore) DeleteAdminTeamsTeamIDClusterClusterID(
	c *gin.Context,
	teamID api.TeamID,
	clusterID api.ClusterID,
) {
	ctx := c.Request.Context()

	result, err := s.db.Dashboard.DetachTeamCluster(ctx, dashboardqueries.DetachTeamClusterParams{
		TeamID:    teamID,
		ClusterID: clusterID,
	})
	if dberrors.IsNotFoundError(err) {
		s.sendAPIStoreError(c, http.StatusNotFound, "Team not found")

		return
	}
	if err != nil {
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to detach cluster from team")

		return
	}
	if result.ClusterID != nil && !result.Detached {
		s.sendAPIStoreError(c, http.StatusConflict, "Team is assigned to a different cluster")

		return
	}

	logger.L().Info(ctx, "admin team cluster detached",
		logger.WithTeamID(teamID.String()), logger.WithClusterID(clusterID))

	if err := s.authService.InvalidateTeamCache(ctx, teamID); err != nil {
		logger.L().Error(ctx, "invalidating team cache after cluster detachment",
			logger.WithTeamID(teamID.String()), zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to invalidate team cache after cluster detachment")

		return
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
