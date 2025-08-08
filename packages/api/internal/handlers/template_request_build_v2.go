package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
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
	team, tier, apiErr := a.GetTeamAndTier(c, body.TeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and tier", apiErr.Err)
		return
	}

	// Create the build, find the template ID by alias or generate a new one
	_, span := a.Tracer.Start(ctx, "find-template-alias")
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

	builderNodeID, err := a.templateManager.GetAvailableBuildClient(ctx, team.ClusterID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting available build client")
		telemetry.ReportCriticalError(ctx, "error when getting available build client", err, telemetry.WithTemplateID(templateID))
		return
	}

	buildReq := BuildTemplateRequest{
		ClusterID:     team.ClusterID,
		BuilderNodeID: builderNodeID,
		IsNew:         isNew,
		TemplateID:    templateID,
		UserID:        nil,
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
	a.posthog.CreateAnalyticsTeamEvent(team.ID.String(), "submitted environment build request", properties.
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

func (a *APIStore) GetTeamAndTier(
	c *gin.Context,
	// Deprecated: use API Token authentication instead.
	teamID *string,
) (*queries.Team, *queries.Tier, *api.APIError) {
	_, span := a.Tracer.Start(c.Request.Context(), "get-team-and-tier")
	defer span.End()

	if c.Value(auth.TeamContextKey) != nil {
		teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)

		return teamInfo.Team, teamInfo.Tier, nil
	} else if c.Value(auth.UserIDContextKey) != nil {
		_, teams, err := a.GetUserAndTeams(c)
		if err != nil {
			return nil, nil, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: "Error when getting user and teams",
				Err:       err,
			}
		}
		team, tier, err := findTeamAndTier(teams, teamID)
		if err != nil {
			return nil, nil, &api.APIError{
				Code:      http.StatusForbidden,
				ClientMsg: "You are not allowed to access this team",
				Err:       err,
			}
		}

		return team, tier, nil
	}

	return nil, nil, &api.APIError{
		Code:      http.StatusUnauthorized,
		ClientMsg: "You are not authenticated",
		Err:       errors.New("invalid authentication context for team and tier"),
	}
}
