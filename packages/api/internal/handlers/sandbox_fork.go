package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/pause"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// PostSandboxesSandboxIDFork forks a running sandbox: it pauses the sandbox to
// a snapshot, resumes the original sandbox from that snapshot so it keeps
// running under its original ID and remaining time to live, and creates a new
// sandbox from the same snapshot. It returns the newly created sandbox.
func (a *APIStore) PostSandboxesSandboxIDFork(c *gin.Context, sandboxID api.SandboxID) {
	ctx := c.Request.Context()

	teamInfo := auth.MustGetTeamInfo(c)
	teamID := teamInfo.Team.ID

	sandboxID, err := utils.ShortID(sandboxID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid sandbox ID")

		return
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(telemetry.WithSandboxID(sandboxID))

	traceID := span.SpanContext().TraceID().String()
	c.Set("traceID", traceID)

	body, err := ginutils.ParseOptionalBody[api.PostSandboxesSandboxIDForkJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		return
	}

	forkTimeout := sandbox.SandboxTimeoutDefault
	if body.Timeout != nil {
		forkTimeout = time.Duration(*body.Timeout) * time.Second

		if forkTimeout > time.Duration(teamInfo.Limits.MaxLengthHours)*time.Hour {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Timeout cannot be greater than %d hours", teamInfo.Limits.MaxLengthHours))

			return
		}
	}
	filesystemOnly := body.Memory != nil && !*body.Memory

	// Capture the original sandbox's remaining time to live before pausing it,
	// so resuming it afterwards doesn't change its expiration.
	original, err := a.orchestrator.GetSandbox(ctx, teamID, sandboxID)
	if err != nil {
		apiErr := pauseHandleNotRunningSandbox(ctx, a.snapshotCache, sandboxID, teamID)
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	}
	originalTimeout := time.Until(original.EndTime)
	if originalTimeout <= 0 {
		originalTimeout = sandbox.SandboxTimeoutDefault
	}

	pause.LogInitiated(ctx, sandboxID, teamID.String(), pause.ReasonFork)

	err = a.orchestrator.RemoveSandbox(ctx, teamID, sandboxID, sandbox.RemoveOpts{Action: sandbox.StateActionPause, FilesystemOnly: filesystemOnly})
	var transErr *sandbox.InvalidStateTransitionError

	switch {
	case err == nil:
		pause.LogSuccess(ctx, sandboxID, teamID.String(), pause.ReasonFork)
	case errors.Is(err, orchestrator.ErrSandboxNotFound):
		apiErr := pauseHandleNotRunningSandbox(ctx, a.snapshotCache, sandboxID, teamID)
		switch apiErr.Code {
		case http.StatusConflict:
			pause.LogSkipped(ctx, sandboxID, teamID.String(), pause.ReasonFork, pause.SkipReasonAlreadyPaused)
		case http.StatusNotFound:
			pause.LogSkipped(ctx, sandboxID, teamID.String(), pause.ReasonFork, pause.SkipReasonNotFound)
		default:
			pause.LogFailure(ctx, sandboxID, teamID.String(), pause.ReasonFork, err)
		}
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)

		return
	case errors.As(err, &transErr):
		pause.LogFailure(ctx, sandboxID, teamID.String(), pause.ReasonFork, err)
		a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Sandbox '%s' cannot be forked while in '%s' state", sandboxID, transErr.CurrentState))

		return
	default:
		pause.LogFailure(ctx, sandboxID, teamID.String(), pause.ReasonFork, err)
		telemetry.ReportError(ctx, "error pausing sandbox for fork", err)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error forking sandbox")

		return
	}

	sbxlogger.E(&sbxlogger.SandboxMetadata{
		SandboxID:  sandboxID,
		TemplateID: original.TemplateID,
		TeamID:     teamID.String(),
	}).Debug(ctx, "Resuming original sandbox after fork snapshot")

	// Resume the original sandbox under its original ID so it keeps running.
	_, createErr := a.startSandbox(
		ctx,
		sandboxID,
		originalTimeout,
		teamInfo,
		a.buildResumeSandboxData(sandboxID, nil),
		&c.Request.Header,
		true,
		nil, // mcp
	)
	if createErr != nil {
		telemetry.ReportError(ctx, "error resuming original sandbox after fork", createErr.Err, telemetry.WithSandboxID(sandboxID))
		a.sendAPIStoreError(c, createErr.Code, createErr.ClientMsg)

		return
	}

	forkedSandboxID := InstanceIDPrefix + id.Generate()

	sbxlogger.E(&sbxlogger.SandboxMetadata{
		SandboxID:  forkedSandboxID,
		TemplateID: original.TemplateID,
		TeamID:     teamID.String(),
	}).Debug(ctx, "Creating forked sandbox from snapshot")

	// Create the new forked sandbox from the same snapshot, under a new ID.
	forkedSbx, createErr := a.startSandbox(
		ctx,
		forkedSandboxID,
		forkTimeout,
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

	c.JSON(http.StatusCreated, &forkedSbx)
}
