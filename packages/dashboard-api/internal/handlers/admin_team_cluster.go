package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
)

func (s *APIStore) PutAdminTeamsTeamIDCluster(c *gin.Context, teamID api.TeamID) {
	ctx := c.Request.Context()

	body, err := ginutils.ParseBody[api.AdminTeamClusterRegistrationRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		return
	}

	body.Name = strings.TrimSpace(body.Name)
	body.Endpoint = strings.TrimSpace(body.Endpoint)
	body.Token = strings.TrimSpace(body.Token)
	body.SandboxProxyDomain = trimmedOptional(body.SandboxProxyDomain)
	body.AuthOrgId = trimmedOptional(body.AuthOrgId)
	if body.ClusterId == uuid.Nil || body.Name == "" || body.Endpoint == "" || body.Token == "" {
		s.sendAPIStoreError(c, http.StatusBadRequest, "cluster_id, name, endpoint and token are required")

		return
	}

	txDB, tx, err := s.db.WithTx(ctx)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to start cluster registration")

		return
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	team, err := txDB.Dashboard.LockTeamClusterForUpdate(ctx, teamID)
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Team not found")
		} else {
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to lock team")
		}

		return
	}
	if !strings.Contains(strings.ToLower(team.Tier), "enterprise") {
		s.sendAPIStoreError(c, http.StatusConflict, "Only enterprise teams can be assigned to a BYOC cluster")

		return
	}

	existing, clusterErr := txDB.Dashboard.GetClusterForRegistration(ctx, body.ClusterId)
	clusterExists := clusterErr == nil
	if clusterErr != nil && !dberrors.IsNotFoundError(clusterErr) {
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to inspect cluster registration")

		return
	}

	if team.ClusterID != nil && *team.ClusterID == body.ClusterId {
		if !clusterExists || !registrationMatches(existing, body) {
			s.sendAPIStoreError(c, http.StatusConflict, "Cluster ID already exists with different immutable values")

			return
		}

		if err := tx.Commit(ctx); err != nil {
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to complete cluster registration")

			return
		}
		if err := s.authService.InvalidateTeamCache(ctx, teamID); err != nil {
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Cluster is assigned but the team cache could not be invalidated; retry the same request")

			return
		}

		c.JSON(http.StatusOK, clusterRegistrationResponse(teamID, body.ClusterId, body.ExpectedPreviousClusterId, false))

		return
	}

	if !sameOptionalUUID(team.ClusterID, body.ExpectedPreviousClusterId) {
		s.sendAPIStoreError(c, http.StatusConflict, "Team cluster changed since this deployment started")

		return
	}
	if clusterExists {
		s.sendAPIStoreError(c, http.StatusConflict, "Cluster ID already exists and is not assigned to this team")

		return
	}

	_, err = txDB.Dashboard.InsertClusterForRegistration(ctx, dashboardqueries.InsertClusterForRegistrationParams{
		ClusterID:          body.ClusterId,
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
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to register cluster")
		}

		return
	}

	err = txDB.Dashboard.AssignTeamCluster(ctx, dashboardqueries.AssignTeamClusterParams{
		ClusterID: body.ClusterId,
		TeamID:    teamID,
	})
	if err != nil {
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to assign cluster to team")

		return
	}

	if err := tx.Commit(ctx); err != nil {
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to complete cluster registration")

		return
	}
	if err := s.authService.InvalidateTeamCache(ctx, teamID); err != nil {
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Cluster is assigned but the team cache could not be invalidated; retry the same request")

		return
	}

	c.JSON(http.StatusOK, clusterRegistrationResponse(teamID, body.ClusterId, body.ExpectedPreviousClusterId, true))
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

func sameOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return *left == *right
}

func registrationMatches(existing dashboardqueries.GetClusterForRegistrationRow, body api.AdminTeamClusterRegistrationRequest) bool {
	return existing.ID == body.ClusterId &&
		existing.Name == body.Name &&
		existing.Endpoint == body.Endpoint &&
		existing.EndpointTls == body.EndpointTls &&
		existing.Token == body.Token &&
		sameOptionalString(existing.SandboxProxyDomain, body.SandboxProxyDomain) &&
		sameOptionalString(existing.AuthOrgID, body.AuthOrgId)
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}

	return *left == *right
}

func clusterRegistrationResponse(teamID, clusterID uuid.UUID, previousClusterID *uuid.UUID, changed bool) api.AdminTeamClusterRegistrationResponse {
	return api.AdminTeamClusterRegistrationResponse{
		TeamId:            teamID,
		ClusterId:         clusterID,
		PreviousClusterId: previousClusterID,
		Changed:           changed,
	}
}
