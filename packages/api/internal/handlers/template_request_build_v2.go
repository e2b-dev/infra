package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// PostV2Templates triggers a new template build
func (a *APIStore) PostV2Templates(c *gin.Context) {
	ctx := c.Request.Context()

	body, err := apiutils.ParseBody[api.TemplateBuildRequestV2](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)

		return
	}

	telemetry.ReportEvent(ctx, "started environment build")

	// Prepare info for rebuilding env
	_, span := a.Tracer.Start(ctx, "get-user-and-teams")
	defer span.End()
	userID, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting user: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting user", err)

		return
	}
	span.End()

	// Find the team and tier
	_, span = a.Tracer.Start(ctx, "find-team-and-tier")
	defer span.End()
	team, tier, err := findTeamAndTier(teams, &body.TeamID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, err.Error())
		telemetry.ReportCriticalError(ctx, "error finding team and tier", err)
		return
	}
	span.End()

	// Create the build, find the template ID by alias or generate a new one
	_, span = a.Tracer.Start(ctx, "find-template-alias")
	defer span.End()
	templateID := id.Generate()
	isNew := true
	templateAlias, err := a.sqlcDB.GetTemplateAliasByAlias(ctx, body.Alias)
	if err != nil {
		var notFoundErr db.ErrNotFound
		if !errors.As(err, &notFoundErr) {
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting template alias: %s", err))
			telemetry.ReportCriticalError(ctx, "error when getting template alias", err)
			return
		}
	} else {
		templateID = templateAlias.EnvID
		isNew = false
	}
	span.End()

	var builderNodeID *string
	if team.ClusterID != nil {
		cluster, found := a.clustersPool.GetClusterById(*team.ClusterID)
		if !found {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Cluster with ID '%s' not found", *team.ClusterID))
			telemetry.ReportCriticalError(ctx, "cluster not found", fmt.Errorf("cluster with ID '%s' not found", *team.ClusterID), telemetry.WithTemplateID(templateID))
			return
		}

		clusterNode, err := cluster.GetAvailableTemplateBuilder(ctx)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting available template builder: %s", err))
			telemetry.ReportCriticalError(ctx, "error when getting available template builder", err, telemetry.WithTemplateID(templateID))
			return
		}

		builderNodeID = &clusterNode.NodeID
	}

	buildReq := BuildTemplateRequest{
		ClusterID:     team.ClusterID,
		BuilderNodeID: builderNodeID,
		IsNew:         isNew,
		TemplateID:    templateID,
		UserID:        *userID,
		Team:          team,
		Tier:          tier,
		Alias:         &body.Alias,
		CpuCount:      body.CpuCount,
		MemoryMB:      body.MemoryMB,
	}

	template, apiError := a.BuildTemplate(ctx, buildReq)
	if apiError != nil {
		a.sendAPIStoreError(c, apiError.Code, apiError.ClientMsg)
		telemetry.ReportCriticalError(ctx, "invalid request body", err)
		return
	}

	_, span = a.Tracer.Start(ctx, "posthog-analytics")
	defer span.End()
	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "submitted environment build request", properties.
		Set("environment", template.TemplateID).
		Set("build_id", template.BuildID).
		Set("alias", body.Alias),
	)
	span.End()

	c.JSON(http.StatusAccepted, &api.Template{
		TemplateID: template.TemplateID,
		BuildID:    template.BuildID,
		Public:     template.Public,
		Aliases:    template.Aliases,
	})
}
