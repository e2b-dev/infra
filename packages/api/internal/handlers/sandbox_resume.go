package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"
)

func getSandboxIDClient(sandboxID string) (string, bool) {
	parts := strings.Split(sandboxID, "-")
	if len(parts) != 2 {
		return "", false
	}

	return parts[1], true
}

func (a *APIStore) PostSandboxesSandboxIDResume(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	// Get team from context, use TeamContextKey
	teamInfo := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo)

	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	telemetry.ReportEvent(ctx, "Parsed body")

	body, err := utils.ParseBody[api.PostSandboxesSandboxIDResumeJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	timeout := instance.InstanceExpiration
	if body.Timeout != nil {
		timeout = time.Duration(*body.Timeout) * time.Second

		if timeout > time.Duration(teamInfo.Tier.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Tier.MaxLengthHours))

			return
		}
	}

	autoPause := instance.InstanceAutoPauseDefault
	if body.AutoPause != nil {
		autoPause = *body.AutoPause
	}

	clientID, ok := getSandboxIDClient(sandboxID)
	if !ok {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid sandbox ID — missing client ID part: %s", sandboxID))

		return
	}

	sandboxID = utils.ShortID(sandboxID)

	_, err = a.orchestrator.GetSandbox(sandboxID)
	if err == nil {
		a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox %s is already running", sandboxID))

		return
	}

	// Wait for any pausing for this sandbox in progress.
	pausedOnNode, err := a.orchestrator.WaitForPause(ctx, sandboxID)
	if err != nil && !errors.Is(err, instance.ErrPausingInstanceNotFound) {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error while pausing sandbox %s: %s", sandboxID, err))

		return
	}

	if err == nil {
		// If the pausing was in progress, prefer to restore on the node where the pausing happened.
		clientID = pausedOnNode.ID
	}

	snapshot, build, err := a.db.GetLastSnapshot(ctx, sandboxID, teamInfo.Team.ID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error resuming sandbox: %s", err))

		return
	}

	sandboxLogger := logs.NewSandboxLogger(
		sandboxID,
		*build.EnvID,
		teamInfo.Team.ID.String(),
		build.Vcpu,
		build.RAMMB,
		false,
	)
	sandboxLogger.Debugf("Started resuming sandbox")

	sbx, err := a.startSandbox(
		ctx,
		snapshot.SandboxID,
		timeout,
		nil,
		snapshot.Metadata,
		"",
		teamInfo,
		build,
		sandboxLogger,
		&c.Request.Header,
		true,
		&clientID,
		snapshot.BaseEnvID,
		autoPause,
	)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error resuming sandbox: %s", err))

		return
	}

	c.JSON(http.StatusCreated, &sbx)
}
