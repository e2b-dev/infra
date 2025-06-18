package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envalias"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type DockerfileSteps struct {
	FromImage string              `json:"from_image"`
	Steps     *[]api.TemplateStep `json:"steps"`
}

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
	userID, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting user: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting user", err)

		return
	}

	// Find the team and tier
	team, tier, err := findTeamAndTier(teams, &body.TeamID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, err.Error())
		telemetry.ReportCriticalError(ctx, "error finding team and tier", err)
		return
	}

	// Create the build, find the template ID by alias or generate a new one
	templateID := id.Generate()
	isNew := true
	templateAlias, err := a.db.Client.EnvAlias.Query().Where(envalias.ID(body.Alias)).Only(ctx)
	if err != nil {
		var notFoundErr *db.ErrNotFound
		if !errors.As(err, notFoundErr) {
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting template alias: %s", err))
			telemetry.ReportCriticalError(ctx, "error when getting template alias", err)
			return
		}
	} else {
		templateID = templateAlias.EnvID
		isNew = false
	}

	stepsMarshal, err := json.Marshal(DockerfileSteps{
		FromImage: body.FromImage,
		Steps:     body.Steps,
	})
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Failed serializing template steps: %s", err))
		telemetry.ReportCriticalError(ctx, "failed serializing template steps", err)
		return
	}
	buildReq := BuildTemplateRequest{
		IsNew:      isNew,
		TemplateID: templateID,
		UserID:     *userID,
		Team:       team,
		Tier:       tier,
		Dockerfile: string(stepsMarshal),
		Alias:      &body.Alias,
		StartCmd:   body.StartCmd,
		ReadyCmd:   body.ReadyCmd,
		CpuCount:   body.CpuCount,
		MemoryMB:   body.MemoryMB,
	}

	template, apiError := a.BuildTemplate(ctx, buildReq)
	if apiError != nil {
		a.sendAPIStoreError(c, apiError.Code, apiError.ClientMsg)
		telemetry.ReportCriticalError(ctx, "invalid request body", err)
		return
	}

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "submitted environment build request", properties.
		Set("environment", template.TemplateID).
		Set("build_id", template.BuildID).
		Set("alias", body.Alias),
	)

	c.JSON(http.StatusAccepted, &api.Template{
		TemplateID: template.TemplateID,
		BuildID:    template.BuildID,
		Public:     template.Public,
		Aliases:    template.Aliases,
	})
}
