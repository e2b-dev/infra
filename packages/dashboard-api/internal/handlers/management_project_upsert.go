package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/management"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// ManagementUpsertProject creates or reconciles a project from a
// caller-supplied id, answering 201 or 200 to say which happened.
func (s *APIStore) ManagementUpsertProject(c *gin.Context, projectID api.ProjectID) {
	ctx := c.Request.Context()
	attrs := []attribute.KeyValue{telemetry.WithTeamID(projectID.String())}
	telemetry.SetAttributes(ctx, attrs...)

	body, err := ginutils.ParseBody[api.ManagementProjectUpsertRequest](ctx, c)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "upsert project failed",
			fmt.Errorf("parse project upsert request: %w", err), attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	stored, created, err := s.managementService.UpsertProject(ctx, management.Project{
		ID:    projectID,
		Name:  body.Name,
		Slug:  body.Slug,
		Email: body.Email,
	})
	if err != nil {
		s.sendProjectUpsertError(c, err, attrs...)

		return
	}

	c.JSON(upsertStatus(created), api.ManagementProject{
		Id:    stored.ID,
		Name:  stored.Name,
		Slug:  stored.Slug,
		Email: stored.Email,
	})
}

func (s *APIStore) sendProjectUpsertError(c *gin.Context, err error, attrs ...attribute.KeyValue) {
	ctx := c.Request.Context()

	if message := projectConflictMessage(err); message != "" {
		telemetry.ReportErrorByCode(ctx, http.StatusConflict, "upsert project failed", err, attrs...)
		s.sendAPIStoreError(c, http.StatusConflict, message)

		return
	}

	telemetry.ReportCriticalError(ctx, "upsert project failed", err, attrs...)
	s.sendAPIStoreError(c, http.StatusInternalServerError, "Error upserting project")
}

// projectConflictMessage names the conflict an upsert lost to, or "" when the
// failure is not one the caller can resolve by changing the request.
func projectConflictMessage(err error) string {
	switch {
	case errors.Is(err, management.ErrProjectSlugTaken):
		return "Slug is already taken on this control plane"
	case errors.Is(err, management.ErrProjectRaced):
		return "Project was created concurrently, retry"
	}

	return ""
}

// upsertStatus distinguishes a project this request created from one it found.
func upsertStatus(created bool) int {
	if created {
		return http.StatusCreated
	}

	return http.StatusOK
}
