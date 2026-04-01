package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	snapshotcache "github.com/e2b-dev/infra/packages/api/internal/cache/snapshots"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	orchestratorgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDResume(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	// Get team from context, use TeamContextKey
	teamInfo := auth.MustGetTeamInfo(c)

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	sandboxID, err := utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	span.SetAttributes(telemetry.WithSandboxID(sandboxID))

	body, err := ginutils.ParseBody[api.PostSandboxesSandboxIDResumeJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	telemetry.ReportEvent(ctx, "Parsed body")

	timeout := sandbox.SandboxTimeoutDefault
	if body.Timeout != nil {
		timeout = time.Duration(*body.Timeout) * time.Second

		if timeout > time.Duration(teamInfo.Limits.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Limits.MaxLengthHours))

			return
		}
	}

	teamID := teamInfo.Team.ID
	sandboxData, err := a.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if err == nil {
		if sandboxData.TeamID != teamID {
			logger.L().Debug(ctx, "Sandbox team mismatch on resume", logger.WithSandboxID(sandboxID), logger.WithTeamID(teamID.String()))
			a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(sandboxID))

			return
		}

		switch sandboxData.State {
		case sandbox.StatePausing:
			logger.L().Debug(ctx, "Waiting for sandbox to pause", logger.WithSandboxID(sandboxID))
			err = a.orchestrator.WaitForStateChange(ctx, teamID, sandboxID)
			if err != nil {
				telemetry.ReportCriticalError(ctx, "error waiting for sandbox to pause", err,
					telemetry.WithSandboxID(sandboxID),
					telemetry.WithTeamID(teamID.String()),
				)
				a.sendAPIStoreError(c, http.StatusInternalServerError, "Error waiting for sandbox to pause")

				return
			}
		case sandbox.StateKilling:
			logger.L().Debug(ctx, "Sandbox is being killed, cannot resume", logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(sandboxID))

			return
		case sandbox.StateSnapshotting:
			a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox snapshot is currently being created for sandbox '%s'", sandboxID))

			return
		case sandbox.StateRunning:
			a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox %s is already running", sandboxID))

			logger.L().Debug(ctx, "Sandbox is already running",
				logger.WithSandboxID(sandboxID),
				logger.Time("end_time", sandboxData.EndTime),
				logger.Time("start_time", sandboxData.StartTime),
				zap.String("node_id", sandboxData.NodeID),
			)

			return
		default:
			telemetry.ReportCriticalError(ctx, "Sandbox is in an unknown state", fmt.Errorf("state: %s", sandboxData.State),
				telemetry.WithSandboxID(sandboxID),
				telemetry.WithTeamID(teamID.String()),
			)
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Sandbox is in an unknown state")

			return
		}
	}

	// TODO: ENG-3544 scope GetLastSnapshot query by teamID to avoid post-fetch ownership check.
	lastSnapshot, err := a.snapshotCache.Get(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, snapshotcache.ErrSnapshotNotFound) {
			logger.L().Debug(ctx, "Snapshot not found", logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(sandboxID))

			return
		}

		telemetry.ReportCriticalError(ctx, "Error getting last snapshot", err,
			telemetry.WithSandboxID(sandboxID),
			telemetry.WithTeamID(teamID.String()),
		)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting snapshot")

		return
	}

	if lastSnapshot.Snapshot.TeamID != teamID {
		telemetry.ReportError(ctx, fmt.Sprintf("snapshot for sandbox '%s' doesn't belong to team '%s'", sandboxID, teamID.String()), nil)
		a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(sandboxID))

		return
	}

	sbxlogger.E(&sbxlogger.SandboxMetadata{
		SandboxID:  sandboxID,
		TemplateID: lastSnapshot.Snapshot.EnvID,
		TeamID:     teamID.String(),
	}).Debug(ctx, "Started resuming sandbox")

	sbx, createErr := a.startSandbox(
		ctx,
		sandboxID,
		timeout,
		teamInfo,
		a.buildResumeSandboxData(sandboxID, body.AutoPause),
		&c.Request.Header,
		true,
		nil, // mcp
	)
	if createErr != nil {
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

		return
	}

	c.JSON(http.StatusCreated, &sbx)
}

func convertDatabaseMountsToOrchestratorMounts(volumes []*types.SandboxVolumeMountConfig) []*orchestratorgrpc.SandboxVolumeMount {
	results := make([]*orchestratorgrpc.SandboxVolumeMount, 0, len(volumes))

	for _, item := range volumes {
		results = append(results, &orchestratorgrpc.SandboxVolumeMount{
			Id:   item.ID,
			Type: item.Type,
			Name: item.Name,
			Path: item.Path,
		})
	}

	return results
}

// buildResumeSandboxData returns a SandboxDataFetcher that fetches snapshot data
// from the cache and builds SandboxMetadata for resume operations.
// The returned callback is called inside the sandbox lock to prevent race conditions.
func (a *APIStore) buildResumeSandboxData(sandboxID string, autoPauseOverride *bool) orchestrator.SandboxDataFetcher {
	return func(ctx context.Context) (orchestrator.SandboxMetadata, *api.APIError) {
		lastSnapshot, err := a.snapshotCache.Get(ctx, sandboxID)
		if err != nil {
			return orchestrator.SandboxMetadata{}, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: "Error when getting snapshot",
				Err:       fmt.Errorf("error getting last snapshot for sandbox '%s': %w", sandboxID, err),
			}
		}

		snap := lastSnapshot.Snapshot
		build := lastSnapshot.EnvBuild

		nodeID := snap.OriginNodeID

		alias := ""
		if len(lastSnapshot.Aliases) > 0 {
			alias = lastSnapshot.Aliases[0]
		}

		var envdAccessToken *string
		if snap.EnvSecure {
			accessToken, tokenErr := a.getEnvdAccessToken(build.EnvdVersion, sandboxID)
			if tokenErr != nil {
				return orchestrator.SandboxMetadata{}, tokenErr
			}
			envdAccessToken = &accessToken
		}

		autoPause := snap.AutoPause
		if autoPauseOverride != nil {
			autoPause = *autoPauseOverride
		}

		var network *types.SandboxNetworkConfig
		var autoResume *types.SandboxAutoResumeConfig
		var volumes []*types.SandboxVolumeMountConfig
		if snap.Config != nil {
			network = snap.Config.Network
			autoResume = snap.Config.AutoResume
			volumes = snap.Config.VolumeMounts
		}

		return orchestrator.SandboxMetadata{
			Metadata:            snap.Metadata,
			Build:               build,
			AllowInternetAccess: snap.AllowInternetAccess,
			Network:             network,
			Alias:               alias,
			TemplateID:          snap.EnvID,
			BaseTemplateID:      snap.BaseEnvID,
			AutoPause:           autoPause,
			AutoResume:          autoResume,
			VolumeMounts:        convertDatabaseMountsToOrchestratorMounts(volumes),
			EnvdAccessToken:     envdAccessToken,
			NodeID:              &nodeID,
		}, nil
	}
}
