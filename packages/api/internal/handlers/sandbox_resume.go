package handlers

import (
	"fmt"
	"net/http"
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
	)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error resuming sandbox: %s", err))

		return
	}

	c.JSON(http.StatusCreated, &sbx)
}
