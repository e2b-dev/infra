package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/management"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) ManagementApplyProjectMember(c *gin.Context, projectID api.ProjectID, userID api.UserID) {
	ctx := c.Request.Context()
	attrs := []attribute.KeyValue{telemetry.WithTeamID(projectID.String())}

	body, err := ginutils.ParseBody[api.ManagementProjectMemberApplyRequest](ctx, c)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "apply project member failed", fmt.Errorf("parse project member request: %w", err), attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	var identities []management.ProjectMemberIdentity
	if body.Identities != nil {
		identities = make([]management.ProjectMemberIdentity, 0, len(*body.Identities))
		for _, identity := range *body.Identities {
			identities = append(identities, management.ProjectMemberIdentity{
				Issuer:  identity.Issuer,
				Subject: identity.Subject,
			})
		}
	}

	if err := s.managementService.ApplyProjectMember(ctx, management.ProjectMemberProjection{
		ProjectID:  projectID,
		UserID:     userID,
		Revision:   body.Revision,
		Present:    body.Present,
		Identities: identities,
	}); err != nil {
		s.sendProjectMemberError(c, err, attrs...)

		return
	}

	c.Status(http.StatusNoContent)
}
