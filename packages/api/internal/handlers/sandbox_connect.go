package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	snapshotcache "github.com/e2b-dev/infra/packages/api/internal/cache/snapshots"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) PostSandboxesSandboxIDConnect(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	// Get team from context, use TeamContextKey
	teamInfo := auth.MustGetTeamInfo(c)

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.ReportEvent(ctx, "Parsed body")

	body, err := ginutils.ParseBody[api.PostSandboxesSandboxIDConnectJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	timeout := time.Duration(body.Timeout) * time.Second
	if timeout > time.Duration(teamInfo.Limits.MaxLengthHours)*time.Hour {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Limits.MaxLengthHours))

		return
	}

	teamID := teamInfo.Team.ID

	sandboxID, err = utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	span.SetAttributes(telemetry.WithSandboxID(sandboxID))

	// It could happen that after sandbox transition, it'll be again transitioning, retry up to maxConnectRetries times.
	const maxConnectRetries = 3

	for attempt := range maxConnectRetries {
		sbx, apiErr := a.orchestrator.KeepAliveFor(ctx, teamID, sandboxID, timeout, false)
		if apiErr == nil {
			c.JSON(http.StatusOK, sbx.ToAPISandbox())

			return
		}

		// Sandbox not in store at all → fall through to snapshot resume.
		if errors.Is(apiErr.Err, sandbox.ErrNotFound) {
			break
		}

		// Sandbox exists but isn't running → check which transitional state.
		var notRunningErr *sandbox.NotRunningError
		if !errors.As(apiErr.Err, &notRunningErr) {
			telemetry.ReportErrorByCode(ctx, apiErr.Code, "error keeping sandbox alive", apiErr.Err,
				telemetry.WithSandboxID(sandboxID),
				telemetry.WithTeamID(teamID.String()),
			)
			a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

			return
		}

		if notRunningErr.State == sandbox.StateKilling {
			killInfo := a.orchestrator.WasSandboxKilled(ctx, teamID, sandboxID)
			a.sendAPIStoreError(c, http.StatusGone, utils.SandboxKilledMsg(sandboxID, killInfo))

			return
		}

		logger.L().Info(ctx, "Sandbox not running, waiting for state change",
			logger.WithSandboxID(sandboxID),
			zap.String("state", string(notRunningErr.State)),
			zap.Int("attempt", attempt+1),
		)

		err = a.orchestrator.WaitForStateChange(ctx, teamID, sandboxID)
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error waiting for sandbox state change", err,
				telemetry.WithSandboxID(sandboxID),
				telemetry.WithTeamID(teamID.String()),
			)
			a.sendAPIStoreError(c, http.StatusInternalServerError,
				"Error waiting for sandbox state change")

			return
		}

		continue
	}

	// TODO: ENG-3544 scope GetLastSnapshot query by teamID to avoid post-fetch ownership check.
	lastSnapshot, err := a.snapshotCache.Get(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, snapshotcache.ErrSnapshotNotFound) {
			// Check if the sandbox was killed (return 410 Gone) vs never existed (return 404 Not Found)
			if killInfo := a.orchestrator.WasSandboxKilled(ctx, teamID, sandboxID); killInfo != nil {
				logger.L().Debug(ctx, "Sandbox was killed", logger.WithSandboxID(sandboxID))
				a.sendAPIStoreError(c, http.StatusGone, utils.SandboxKilledMsg(sandboxID, killInfo))

				return
			}

			logger.L().Debug(ctx, "Snapshot not found", logger.WithSandboxID(sandboxID))
			a.sendAPIStoreError(c, http.StatusNotFound, utils.SandboxNotFoundMsg(sandboxID))

			return
		}

		telemetry.ReportCriticalError(ctx, "Error getting last snapshot", err, telemetry.WithSandboxID(sandboxID), telemetry.WithTeamID(teamID.String()))
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
		a.buildResumeSandboxData(sandboxID, nil),
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
