package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	dbapi "github.com/e2b-dev/infra/packages/api/internal/db"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	templatemanager "github.com/e2b-dev/infra/packages/api/internal/template-manager"
	apiutils "github.com/e2b-dev/infra/packages/api/internal/utils"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// CheckAndCancelConcurrentBuilds checks for concurrent builds and cancels them if found
func (a *APIStore) CheckAndCancelConcurrentBuilds(ctx context.Context, templateID api.TemplateID, buildID uuid.UUID, teamClusterID uuid.UUID) error {
	concurrentBuilds, err := a.sqlcDB.GetConcurrentTemplateBuilds(ctx, queries.GetConcurrentTemplateBuildsParams{
		TemplateID:     templateID,
		CurrentBuildID: buildID,
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "Error when getting running builds", err)

		return fmt.Errorf("error when getting running builds: %w", err)
	}

	// make sure there is no other build in progress for the same template
	if len(concurrentBuilds) > 0 {
		concurrentRunningBuilds := utils.Filter(concurrentBuilds, func(b queries.EnvBuild) bool {
			return dbtypes.BuildStatus(b.Status).IsInProgress()
		})
		buildIDs := make([]templatemanager.DeleteBuild, 0, len(concurrentRunningBuilds))
		for _, b := range concurrentRunningBuilds {
			clusterNodeID := b.ClusterNodeID
			if clusterNodeID == nil {
				continue
			}

			buildIDs = append(buildIDs, templatemanager.DeleteBuild{
				TemplateID: templateID,
				BuildID:    b.ID,
				ClusterID:  teamClusterID,
				NodeID:     *clusterNodeID,
			})
		}
		telemetry.ReportEvent(ctx, "canceling running builds", attribute.StringSlice("ids", utils.Map(buildIDs, func(b templatemanager.DeleteBuild) string {
			return fmt.Sprintf("%s/%s", b.TemplateID, b.BuildID)
		})))

		deleteJobErr := a.templateManager.DeleteBuilds(ctx, buildIDs)
		if deleteJobErr != nil {
			telemetry.ReportCriticalError(ctx, "error when canceling running build", deleteJobErr)

			return fmt.Errorf("error when canceling running build: %w", deleteJobErr)
		}
		telemetry.ReportEvent(ctx, "canceled running builds")
	}

	return nil
}

// PostTemplatesTemplateIDBuildsBuildID triggers a new build after the user pushes the Docker image to the registry
func (a *APIStore) PostTemplatesTemplateIDBuildsBuildID(c *gin.Context, templateID api.TemplateID, buildID api.BuildID) {
	ctx := c.Request.Context()

	buildUUID, err := uuid.Parse(buildID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid build ID: %s", buildID))

		telemetry.ReportCriticalError(ctx, "invalid build ID", err)

		return
	}

	userID := a.GetUserID(c)

	teams, err := dbapi.GetTeamsByUser(ctx, a.authDB, userID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting default team: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting default team", err)

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

	var team *types.Team
	// Check if the user has access to the template
	for _, t := range teams {
		if t.Team.ID == templateBuildDB.Env.TeamID {
			team = t.Team

			break
		}
	}

	if team == nil {
		a.sendAPIStoreError(c, http.StatusForbidden, "User does not have access to the template")

		telemetry.ReportCriticalError(ctx, "user does not have access to the template", err, telemetry.WithTemplateID(templateID))

		return
	}

	telemetry.SetAttributes(ctx,
		attribute.String("user.id", userID.String()),
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
	if !dbtypes.BuildStatus(build.Status).IsPending() {
		a.sendAPIStoreError(c, http.StatusBadRequest, "build is not in waiting state")
		telemetry.ReportCriticalError(ctx, "build is not in waiting state", fmt.Errorf("build is not in waiting state: %s", build.Status), telemetry.WithTemplateID(templateID))

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
		StartCmd:        build.StartCmd,
		ReadyCmd:        build.ReadyCmd,
		Dockerfile:      build.Dockerfile,
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
	forceRebuild := true
	fromImage := ""
	err = a.templateManager.CreateTemplate(
		ctx,
		team.ID,
		team.Slug,
		templateID,
		buildUUID,
		build.KernelVersion,
		build.FirecrackerVersion,
		build.StartCmd,
		build.Vcpu,
		build.FreeDiskSizeMb,
		build.RamMb,
		build.ReadyCmd,
		&fromImage,
		nil, // fromTemplate not supported in v1 handler
		nil, // fromImageRegistry not supported in v1 handler
		&forceRebuild,
		nil,
		apiutils.WithClusterFallback(team.ClusterID),
		builderNode.NodeID,
		templates.TemplateV1Version,
	)

	a.posthog.CreateAnalyticsUserEvent(ctx, userID.String(), team.ID.String(), "built environment", posthog.NewProperties().
		Set("user_id", userID).
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
