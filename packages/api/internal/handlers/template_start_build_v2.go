package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	jsSDKPrefix     = "e2b-js-sdk/"
	pythonSDKPrefix = "e2b-python-sdk/"
)

type dockerfileStore struct {
	FromImage    *string             `json:"from_image"`
	FromTemplate *string             `json:"from_template"`
	Steps        *[]api.TemplateStep `json:"steps"`
}

// PostV2TemplatesTemplateIDBuildsBuildID triggers a new build
func (a *APIStore) PostV2TemplatesTemplateIDBuildsBuildID(c *gin.Context, templateID api.TemplateID, buildID api.BuildID) {
	ctx := c.Request.Context()

	body, err := apiutils.ParseBody[api.TemplateBuildStartV2](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)

		return
	}

	buildUUID, err := uuid.Parse(buildID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid build ID: %s", buildID))

		telemetry.ReportCriticalError(ctx, "invalid build ID", err)

		return
	}

	telemetry.ReportEvent(ctx, "started environment build")

	// Check if the user has access to the template, load the template with build info
	templateBuildDB, err := a.sqlcDB.GetTemplateBuildWithTemplate(ctx, queries.GetTemplateBuildWithTemplateParams{
		TemplateID: templateID,
		BuildID:    buildUUID,
	})
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error when getting template: %s", err))
		telemetry.ReportCriticalError(ctx, "error when getting env", err, telemetry.WithTemplateID(templateID))

		return
	}

	dbTeamID := templateBuildDB.Env.TeamID.String()
	team, apiErr := a.GetTeam(ctx, c, &dbTeamID)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team and tier", apiErr.Err)

		return
	}

	if team.ID != templateBuildDB.Env.TeamID {
		a.sendAPIStoreError(c, http.StatusForbidden, "User does not have access to the template")

		telemetry.ReportCriticalError(ctx, "user does not have access to the template", err, telemetry.WithTemplateID(templateID))

		return
	}

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(team.ID.String()),
		telemetry.WithTemplateID(templateID),
	)

	// setup launch darkly context
	ctx = featureflags.AddToContext(ctx, featureflags.TemplateContext(templateID))

	// Check and cancel concurrent builds
	if err := a.CheckAndCancelConcurrentBuilds(ctx, templateID, buildUUID, apiutils.WithClusterFallback(team.ClusterID)); err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error during template build request")

		return
	}

	startTime := time.Now()
	build := templateBuildDB.EnvBuild

	// only waiting builds can be triggered
	if build.Status != string(types.BuildStatusWaiting) {
		a.sendAPIStoreError(c, http.StatusBadRequest, "build is not in waiting state")
		telemetry.ReportCriticalError(ctx, "build is not in waiting state", fmt.Errorf("build is not in waiting state: %s", build.Status), telemetry.WithTemplateID(templateID))

		return
	}

	stepsMarshalled, err := json.Marshal(dockerfileStore{
		FromImage:    body.FromImage,
		FromTemplate: body.FromTemplate,
		Steps:        body.Steps,
	})
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when processing steps: %s", err))
		telemetry.ReportCriticalError(ctx, "error when processing steps", err, telemetry.WithTemplateID(templateID))

		return
	}

	version, err := userAgentToTemplateVersion(ctx, logger.L().With(logger.WithTemplateID(templateID), logger.WithBuildID(buildID)), c.Request.UserAgent())
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing user agent: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing user agent", err, telemetry.WithTemplateID(templateID))

		return
	}

	builderNode, err := a.templateManager.GetAvailableBuildClient(ctx, apiutils.WithClusterFallback(team.ClusterID))
	if err != nil {
		a.sendAPIStoreError(c, http.StatusServiceUnavailable, "Error when getting available build client")
		telemetry.ReportCriticalError(ctx, "error when getting available build client", err, telemetry.WithTemplateID(templateID))

		return
	}

	machineInfo := builderNode.GetMachineInfo()
	err = a.sqlcDB.UpdateTemplateBuild(ctx, queries.UpdateTemplateBuildParams{
		StartCmd:        body.StartCmd,
		ReadyCmd:        body.ReadyCmd,
		Dockerfile:      utils.ToPtr(string(stepsMarshalled)),
		ClusterNodeID:   utils.ToPtr(builderNode.NodeID),
		CpuArchitecture: utils.ToPtr(machineInfo.CPUArchitecture),
		CpuFamily:       utils.ToPtr(machineInfo.CPUFamily),
		CpuModel:        utils.ToPtr(machineInfo.CPUModel),
		CpuModelName:    utils.ToPtr(machineInfo.CPUModelName),
		CpuFlags:        machineInfo.CPUFlags,
		BuildUuid:       buildUUID,
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when updating build", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when updating build: %s", err))

		return
	}

	// Call the Template Manager to build the environment
	err = a.templateManager.CreateTemplate(
		ctx,
		team.ID,
		team.Slug,
		templateID,
		buildUUID,
		build.KernelVersion,
		build.FirecrackerVersion,
		body.StartCmd,
		build.Vcpu,
		build.FreeDiskSizeMb,
		build.RamMb,
		body.ReadyCmd,
		body.FromImage,
		body.FromTemplate,
		body.FromImageRegistry,
		body.Force,
		body.Steps,
		apiutils.WithClusterFallback(team.ClusterID),
		builderNode.NodeID,
		version,
	)

	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "built environment", posthog.NewProperties().
		Set("environment", templateID).
		Set("build_id", buildID).
		Set("duration", time.Since(startTime).String()).
		Set("success", err == nil),
	)

	if err != nil {
		telemetry.ReportCriticalError(ctx, "build failed", err, telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when starting template build: %s", err))

		return
	}

	c.Status(http.StatusAccepted)
}

// userAgentToTemplateVersion returns the template semver version based on the user agent string.
// If the user agent is not recognized, it defaults to the latest stable version.
func userAgentToTemplateVersion(ctx context.Context, logger logger.Logger, userAgent string) (string, error) {
	version := templates.TemplateV2LatestVersion

	switch {
	case strings.HasPrefix(userAgent, jsSDKPrefix):
		sdk := strings.TrimPrefix(userAgent, jsSDKPrefix)

		// Check if the SDK version supports the latest template version
		ok, err := utils.IsGTEVersion(sdk, templates.SDKTemplateReleaseVersion)
		if err != nil {
			return "", fmt.Errorf("parsing JS SDK version: %w", err)
		}
		if !ok {
			version = templates.TemplateV2BetaVersion
		}
	case strings.HasPrefix(userAgent, pythonSDKPrefix):
		sdk := strings.TrimPrefix(userAgent, pythonSDKPrefix)

		// Check if the SDK version supports the latest template version
		ok, err := utils.IsGTEVersion(sdk, templates.SDKTemplateReleaseVersion)
		if err != nil {
			return "", fmt.Errorf("parsing Python SDK version: %w", err)
		}
		if !ok {
			version = templates.TemplateV2BetaVersion
		}
	default:
		logger.Debug(ctx, "Unrecognized user agent, defaulting to the latest template version", zap.String("user_agent", userAgent), zap.String("version", version))
	}

	return version, nil
}
