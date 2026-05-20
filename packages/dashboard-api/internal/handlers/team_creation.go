package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) PostTeams(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "create team")

	userID := auth.MustGetUserID(c)
	attrs := []attribute.KeyValue{
		attribute.String("team.provision.operation", "create team"),
	}
	body, err := ginutils.ParseBody[api.CreateTeamRequest](ctx, c)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "create team failed", fmt.Errorf("parse create team request: %w", err), attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "create team failed", errors.New("team name is required"), attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "Team name is required")

		return
	}

	team, err := s.createTeam(ctx, userID, name)
	if err != nil {
		s.handleProvisioningError(ctx, c, "create team", err)

		return
	}

	c.JSON(http.StatusOK, api.TeamResolveResponse{
		Id:   team.ID,
		Slug: team.Slug,
	})
}
